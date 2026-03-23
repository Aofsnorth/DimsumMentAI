package bot

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"time"

	"arthe/internal/config"
	"arthe/internal/llm"
	"arthe/internal/logger"
	"arthe/internal/memory"

	"github.com/google/gophertunnel/minecraft/protocol"
	"github.com/google/gophertunnel/minecraft/protocol/packet"
)

// BotClientInterface defines the interface for bot client operations needed by CameraController
type BotClientInterface interface {
	GetPosition() (x, y, z float64)
	GetRotation() (yaw, pitch float32)
	WritePacket(p packet.Packet) error
}

// WorldScannerInterface defines the interface for world scanning
type WorldScannerInterface interface {
	Scan(ctx context.Context) (*WorldData, error)
}

// Entity represents a player or entity in the world
type Entity struct {
	Username   string  `json:"username"`
	Position   Position `json:"position"`
	Yaw        float32 `json:"yaw"`
	Pitch      float32 `json:"pitch"`
	Health     float32 `json:"health"`
	ItemInHand string  `json:"item_in_hand"`
}

// Position represents a 3D position
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// WorldData contains the scanned world state
type WorldData struct {
	Timestamp    int64     `json:"timestamp"`
	Tick         int64     `json:"tick"`
	Weather      string    `json:"weather"`
	Dimension    string    `json:"dimension"`
	Entities     []Entity  `json:"entities"`
	Blocks       []Block   `json:"blocks"`
	DroppedItems []DroppedItem `json:"dropped_items"`
}

// Block represents a block in the world
type Block struct {
	Position Position `json:"position"`
	Type     string  `json:"type"`
}

// DroppedItem represents an item entity in the world
type DroppedItem struct {
	Position  Position `json:"position"`
	Item      string   `json:"item"`
	Count     int32    `json:"count"`
}

// CameraController manages camera/look direction using LLM-based decision making
type CameraController struct {
	cfg       *config.Config
	bot       BotClientInterface
	scanner   WorldScannerInterface
	memory    memory.MemoryStore
	llmClient llm.LLMClient
	model     string
	logger    logger.Logger
	quit      chan struct{}
}

// llm2Tools defines the tools available to the LLM for camera control
var llm2Tools = []llm.Tool{
	{
		Name:        "look_at",
		Description: "Look at a player or block/position in the world.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "description": "Player name to look at, or 'none' to look forward"},
				"x":      map[string]any{"type": "number", "description": "X coordinate to look at (if not targeting player)"},
				"y":      map[string]any{"type": "number", "description": "Y coordinate to look at (if not targeting player)"},
				"z":      map[string]any{"type": "number", "description": "Z coordinate to look at (if not targeting player)"},
			},
		},
	},
	{
		Name:        "silent",
		Description: "Stay idle this tick. No camera movement.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// llm2SystemPrompt is the system prompt for the camera LLM
const llm2SystemPrompt = `You are the **Manager** of a Minecraft bot. Your role is to observe the world through the bot's camera and make decisions. You receive live world data: weather, nearby entities, blocks, dropped items, and player positions within view range. Use this data to decide where to look each tick. Be strategic — the bot needs your guidance to explore and interact. In the future, you will also plan movements, crafting, and other actions. Always think about what the bot should prioritize observing.`

// NewCameraController creates a new CameraController
func NewCameraController(
	cfg *config.Config,
	bot BotClientInterface,
	scanner WorldScannerInterface,
	mem memory.MemoryStore,
	client llm.LLMClient,
	model string,
	log logger.Logger,
) *CameraController {
	return &CameraController{
		cfg:       cfg,
		bot:       bot,
		scanner:   scanner,
		memory:    mem,
		llmClient: client,
		model:     model,
		logger:    log,
		quit:      make(chan struct{}),
	}
}

// Start begins the camera control loop
func (c *CameraController) Start(ctx context.Context) {
	if !c.cfg.Camera.Enabled {
		c.logger.Info("Camera controller disabled")
		return
	}

	c.logger.Info("Starting camera controller")

	go func() {
		for {
			select {
			case <-c.quit:
				c.logger.Info("Camera controller shutting down")
				return
			default:
				c.waitRandomTicks(c.cfg.Camera.TickMin, c.cfg.Camera.TickMax)

				worldData, err := c.scanner.Scan(ctx)
				if err != nil {
					c.logger.Error("Failed to scan world", logger.Err(err))
					continue
				}

				action, details, err := c.observeAndDecide(ctx, worldData)
				if err != nil {
					c.logger.Error("Failed to decide action", logger.Err(err))
					continue
				}

				if action == "look_at" {
					target, _ := details["target"].(string)
					x, _ := details["x"].(float64)
					y, _ := details["y"].(float64)
					z, _ := details["z"].(float64)

					if err := c.ExecuteLookAt(target, x, y, z); err != nil {
						c.logger.Error("Failed to execute look_at", logger.Err(err))
					}
				}

				obs := memory.WorldObservation{
					Timestamp:  time.Now().UnixMilli(),
					Tick:       worldData.Tick,
					Summary:    formatWorldSummary(worldData),
					Structured: worldDataToMap(worldData),
				}
				if err := c.memory.AppendWorldObservation("camera_bot", obs); err != nil {
					c.logger.Error("Failed to store observation", logger.Err(err))
				}
			}
		}
	}()
}

// Stop signals the camera controller to shut down
func (c *CameraController) Stop() {
	close(c.quit)
}

// waitRandomTicks waits for a random number of ticks between min and max
func (c *CameraController) waitRandomTicks(min, max int) {
	range_ := max - min
	if range_ <= 0 {
		range_ = 1
	}
	ticks := min + rand.Intn(range_)
	time.Sleep(time.Duration(ticks*50) * time.Millisecond)
}

// observeAndDecide queries the LLM to decide what the camera should do
func (c *CameraController) observeAndDecide(ctx context.Context, wd *WorldData) (string, map[string]any, error) {
	worldDescription := buildWorldDescription(wd)

	messages := []llm.Message{
		{
			Role:    "system",
			Content: llm2SystemPrompt,
		},
		{
			Role:    "user",
			Content: worldDescription,
		},
	}

	response, toolCalls, err := c.llmClient.Chat(ctx, messages, llm2Tools, c.model)
	if err != nil {
		return "", nil, err
	}

	if len(toolCalls) == 0 {
		if response.Content != "" {
			c.logger.Debug("LLM response (no tool call)", logger.String("content", response.Content))
		}
		return "silent", nil, nil
	}

	toolCall := toolCalls[0]
	switch toolCall.Name {
	case "silent":
		return "silent", nil, nil
	case "look_at":
		args := toolCall.Args
		return "look_at", args, nil
	default:
		c.logger.Warn("Unknown tool call", logger.String("tool", toolCall.Name))
		return "silent", nil, nil
	}
}

// ExecuteLookAt rotates the bot's head to look at a target or coordinates
func (c *CameraController) ExecuteLookAt(target string, x, y, z float64) error {
	var targetX, targetY, targetZ float64

	if target != "" && target != "none" {
		entity, found := c.findEntityByName(target)
		if !found {
			c.logger.Warn("Entity not found", logger.String("target", target))
			return nil
		}
		targetX = entity.Position.X
		targetY = entity.Position.Y
		targetZ = entity.Position.Z
	} else if x == 0 && y == 0 && z == 0 {
		return nil
	} else {
		targetX = x
		targetY = y
		targetZ = z
	}

	botX, botY, botZ := c.bot.GetPosition()

	dx := targetX - botX
	dy := targetY - botY
	dz := targetZ - botZ

	distance := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if distance < 0.001 {
		return nil
	}

	yaw := math.Atan2(-dx, -dz) * 180 / math.Pi
	pitch := math.Asin(dy/distance) * 180 / math.Pi

	if yaw < 0 {
		yaw += 360
	}

	return c.bot.WritePacket(&protocol.MovePlayerPacket{
		Position:      protocol.Vector3{X: float32(botX), Y: float32(botY), Z: float32(botZ)},
		Rotation:      protocol.Vector3{X: float32(pitch), Y: float32(yaw), Z: 0},
		Mode:          packet.MovePlayerModeNormal,
		OnGround:      true,
		TeleportCause: packet.TeleportCauseAnimation,
	})
}

// findEntityByName searches for an entity by username
func (c *CameraController) findEntityByName(name string) (*Entity, bool) {
	return nil, false
}

// buildWorldDescription creates a text description of the world for the LLM
func buildWorldDescription(wd *WorldData) string {
	description := "World State:\n"
	description += "Weather: " + wd.Weather + "\n"
	description += "Dimension: " + wd.Dimension + "\n"
	description += "Tick: " + formatInt64(wd.Tick) + "\n"

	if len(wd.Entities) > 0 {
		description += "\nNearby Entities:\n"
		for _, e := range wd.Entities {
			description += format.Sprintf("  - %s at (%.1f, %.1f, %.1f)\n", e.Username, e.Position.X, e.Position.Y, e.Position.Z)
		}
	}

	if len(wd.Blocks) > 0 {
		description += "\nNotable Blocks:\n"
		for i, b := range wd.Blocks {
			if i >= 20 {
				description += "  ... and more\n"
				break
			}
			description += format.Sprintf("  - %s at (%.1f, %.1f, %.1f)\n", b.Type, b.Position.X, b.Position.Y, b.Position.Z)
		}
	}

	if len(wd.DroppedItems) > 0 {
		description += "\nDropped Items:\n"
		for i, item := range wd.DroppedItems {
			if i >= 10 {
				description += "  ... and more\n"
				break
			}
			description += format.Sprintf("  - %s (x%d) at (%.1f, %.1f, %.1f)\n", item.Item, item.Count, item.Position.X, item.Position.Y, item.Position.Z)
		}
	}

	return description
}

// formatWorldSummary creates a summary string for world data
func formatWorldSummary(wd *WorldData) string {
	summary := format.Sprintf("Tick %d | %s | %d entities | %d blocks", wd.Tick, wd.Weather, len(wd.Entities), len(wd.Blocks))
	return summary
}

// worldDataToMap converts WorldData to a map for storage
func worldDataToMap(wd *WorldData) map[string]any {
	data := map[string]any{
		"timestamp":     wd.Timestamp,
		"tick":          wd.Tick,
		"weather":       wd.Weather,
		"dimension":     wd.Dimension,
		"entities":      wd.Entities,
		"blocks":        wd.Blocks,
		"dropped_items": wd.DroppedItems,
	}
	return data
}

// formatInt64 formats an int64 as string
func formatInt64(n int64) string {
	return format.Sprintf("%d", n)
}
