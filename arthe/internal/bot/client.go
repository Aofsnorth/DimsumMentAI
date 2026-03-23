package bot

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"sync"
	"time"

	"elysiabot/internal/config"
	"elysiabot/internal/logger"

	"github.com/google/gophertunnel/minecraft"
	"github.com/google/gophertunnel/minecraft/auth"
	"github.com/google/gophertunnel/minecraft/protocol"
	"github.com/google/gophertunnel/minecraft/protocol/login"
	"github.com/google/gophertunnel/minecraft/world"
	"github.com/google/gophertunnel/minecraft/world/entity"
	"github.com/google/gophertunnel/minecraft/world/player"
	"github.com/google/uuid"
)

// EventHandler is an interface for handling bot events
type EventHandler interface {
	HandlePlayerSpawn(ctx context.Context, p *player.Player) error
	HandlePlayerMessage(ctx context.Context, p *player.Player, message string) error
}

// BotClient represents a Minecraft bot client connected to a server
type BotClient struct {
	cfg      *config.Config
	logger   logger.Logger
	conn     *minecraft.Client
	world    *world.World
	entityID uint64
	handlers []EventHandler
	skin     *protocol.Skin
	onChat   func(playerName, message string)
	onSpawn  func()
	mu       sync.RWMutex

	// Player state for world access
	playerMu   sync.RWMutex
	posX       float64
	posY       float64
	posZ       float64
	yaw        float32
	pitch      float32
}

// New creates a new BotClient instance
func New(cfg *config.Config, log logger.Logger, skin *protocol.Skin) (*BotClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if log == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}

	b := &BotClient{
		cfg:      cfg,
		logger:   log,
		skin:     skin,
		handlers: make([]EventHandler, 0),
	}

	return b, nil
}

// RegisterHandler adds an event handler to the bot
func (b *BotClient) RegisterHandler(h EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
}

// SetOnChat sets the callback for chat messages
func (b *BotClient) SetOnChat(f func(playerName, message string)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onChat = f
}

// SetOnSpawn sets the callback for spawn events
func (b *BotClient) SetOnSpawn(f func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onSpawn = f
}

// Connect establishes a connection to the Minecraft server
func (b *BotClient) Connect(ctx context.Context) error {
	b.logger.Info("Connecting to server", logger.String("address", b.cfg.Server.Address))

	var authenticator auth.Authenticator
	switch b.cfg.Bot.AuthMode {
	case "xbox":
		authenticator = auth.XboxAuthenticator()
	default:
		authenticator = auth.NewAuthenticator(b.cfg.Bot.DisplayName, "")
	}

	statusProvider := &botStatusProvider{
		botName: b.cfg.Bot.DisplayName,
	}

	conn, err := minecraft.NewClient(minecraft.Dialer{
		Address:          b.cfg.Server.Address,
		TLS:              b.cfg.Server.UseTLS,
		Authenticator:    authenticator,
		StatusProvider:   statusProvider,
		DisallowChat:     false,
	})
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	b.conn = conn

	// Handle connection in a goroutine with event dispatching
	go b.handleConnection(ctx)

	return nil
}

// handleConnection processes game events from the connection
func (b *BotClient) handleConnection(ctx context.Context) {
	b.logger.Info("Starting event handler goroutine")

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("Context cancelled, stopping event handler")
			return
		default:
			if b.conn == nil {
				return
			}

			// Read and dispatch events from the connection
			b.dispatchEvents(ctx)
		}
	}
}

// dispatchEvents reads game events and dispatches them to handlers
func (b *BotClient) dispatchEvents(ctx context.Context) {
	// Access the world from the connection
	w := b.conn.World()
	if w != nil {
		b.mu.Lock()
		b.world = w
		b.mu.Unlock()
	}

	// Get player info if available
	if p := b.conn.Player(); p != nil {
		b.updatePlayerPosition(p)
	}
}

// updatePlayerPosition updates cached player position and rotation
func (b *BotClient) updatePlayerPosition(p *player.Player) {
	b.playerMu.Lock()
	defer b.playerMu.Unlock()

	// Get position from player
	pos := p.Position()
	b.posX = pos.X()
	b.posY = pos.Y()
	b.posZ = pos.Z()

	// Get rotation from player
	rot := p.Rotation()
	b.yaw = rot.Yaw()
	b.pitch = rot.Pitch()
}

// Disconnect closes the connection to the server
func (b *BotClient) Disconnect() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.conn == nil {
		return nil
	}

	b.logger.Info("Disconnecting from server")
	err := b.conn.Close()
	b.conn = nil
	b.world = nil

	return err
}

// GetWorld returns the current world state
func (b *BotClient) GetWorld() *world.World {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.world
}

// GetPosition returns the bot's current position
func (b *BotClient) GetPosition() (x, y, z float64) {
	b.playerMu.RLock()
	defer b.playerMu.RUnlock()
	return b.posX, b.posY, b.posZ
}

// GetRotation returns the bot's current yaw and pitch
func (b *BotClient) GetRotation() (yaw, pitch float32) {
	b.playerMu.RLock()
	defer b.playerMu.RUnlock()
	return b.yaw, b.pitch
}

// GetEntities returns all entities within the specified radius of the bot
func (b *BotClient) GetEntities(radius float64) []entity.Entity {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.world == nil {
		return nil
	}

	b.playerMu.RLock()
	pos := world.Matrix{}
	pos[12] = b.posX
	pos[13] = b.posY
	pos[14] = b.posZ
	b.playerMu.RUnlock()

	var entities []entity.Entity
	_ = b.world.IterateEntities(func(e entity.Entity) bool {
		if e == nil {
			return false
		}
		// Get entity position
		posVec := e.Position()
		dx := posVec.X() - pos[12]
		dy := posVec.Y() - pos[13]
		dz := posVec.Z() - pos[14]
		dist := dx*dx + dy*dy + dz*dz
		if dist <= radius*radius {
			entities = append(entities, e)
		}
		return false
	})

	return entities
}

// GetBlock returns the block at the specified coordinates
func (b *BotClient) GetBlock(x, y, z int32) *block.Block {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.world == nil {
		return nil
	}

	return b.world.Block(world.BlockPos{X: x, Y: y, Z: z})
}

// LoadSkin loads a skin from PNG and geometry JSON files
func LoadSkin(skinPath, geometryPath, geometryName string) (*protocol.Skin, error) {
	// Read PNG file
	pngData, err := os.ReadFile(skinPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read skin PNG: %w", err)
	}

	// Decode PNG to get image data
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, fmt.Errorf("failed to decode PNG: %w", err)
	}

	width := img.Bounds().Dx()
	height := img.Bounds().Dy()

	// Validate dimensions (64x64 or 128x64 for single layer)
	if (width != 64 || height != 64) && (width != 128 || height != 64) {
		return nil, fmt.Errorf("invalid skin dimensions: %dx%d (expected 64x64 or 128x64)", width, height)
	}

	// Encode to base64 for SkinData
	skinData := base64.StdEncoding.EncodeToString(pngData)

	// Read and parse geometry JSON
	geometryData, err := os.ReadFile(geometryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read geometry JSON: %w", err)
	}

	// Validate JSON structure
	var geometryJSON map[string]interface{}
	if err := json.Unmarshal(geometryData, &geometryJSON); err != nil {
		return nil, fmt.Errorf("failed to parse geometry JSON: %w", err)
	}

	// Generate skin ID from name
	skinID := uuid.NewMD5(uuid.NameSpaceDNS, []byte(geometryName)).String()

	skin := &protocol.Skin{
		SkinData:       skinData,
		GeometryData:   string(geometryData),
		GeometryVersion: "1.16.0",
		PremiumSkin:    true,
		SkinID:         skinID,
	}

	return skin, nil
}

// botStatusProvider implements minecraft.StatusProvider for server ping responses
type botStatusProvider struct {
	botName string
}

func (b *botStatusProvider) ServerStatus() (protocol.Status, error) {
	return protocol.Status{
		ServerName:  "ElysiaBot",
		ServerUUID:  uuid.New().String(),
		MOTD:        fmt.Sprintf("ElysiaBot - %s", b.botName),
		PlayerCount: 0,
		MaxPlayers:  1,
		GameMode:    "creative",
		GameVersion: "1.20.0",
	}, nil
}

// Ensure type compatibility
var _ = (*minecraft.StatusProvider)(nil)

// Add bytes import for PNG decoding
import "bytes"
