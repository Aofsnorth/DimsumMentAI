package exploration

import (
	"context"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot interface for exploration subsystem
type Bot interface {
	GetCoords() mgl32.Vec3
	WritePacket(pk packet.Packet) error
	GetEntities() map[uint64]*entity.Info
	NavigateTo(pos mgl32.Vec3)
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	EquipItem(slot uint32) error
	SendChat(msg string)
	ReportActionStatus(user string, status event.ActionStatus)
	GetEntityRuntimeID() uint64
	GetLocalWorldModel() entity.WorldModel
	GetBlockName(x, y, z int32) (string, bool)
}

// Explorer handles systematic exploration of the world
type Explorer struct {
	bot         Bot
	logger      *slog.Logger
	mu          sync.Mutex
	isExploring bool

	// Visited chunks/areas to avoid revisiting
	visitedAreas map[string]bool

	// Exploration origin (where we started)
	originPos mgl32.Vec3

	// Current exploration direction (degrees, 0-360)
	currentDir float64
}

func NewExplorer(bot Bot, logger *slog.Logger) *Explorer {
	return &Explorer{
		bot:          bot,
		logger:       logger,
		visitedAreas: make(map[string]bool),
	}
}

// ExploreSpiral explores outward in a spiral pattern from the current position
func (e *Explorer) ExploreSpiral(ctx context.Context, maxRadius int, waypointInterval int) int {
	e.mu.Lock()
	e.isExploring = true
	e.originPos = e.bot.GetCoords()
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.isExploring = false
		e.mu.Unlock()
	}()

	waypoints := 0
	origin := e.originPos
	angle := 0.0
	radius := float64(waypointInterval)

	for radius <= float64(maxRadius) {
		select {
		case <-ctx.Done():
			e.bot.ReportActionStatus("", event.ActionStatus{
				Action:  "explore",
				Item:    "spiral",
				Count:   waypoints,
				Success: false,
				Error:   "dihentikan",
			})
			return waypoints
		default:
		}

		// Calculate next position in spiral
		x := origin.X() + float32(math.Cos(angle*math.Pi/180))*float32(radius)
		z := origin.Z() + float32(math.Sin(angle*math.Pi/180))*float32(radius)
		target := mgl32.Vec3{x, origin.Y(), z}

		// Check if area already visited
		key := areaKey(target)
		e.mu.Lock()
		_, visited := e.visitedAreas[key]
		if !visited {
			e.visitedAreas[key] = true
		}
		e.mu.Unlock()

		if !visited {
			e.bot.NavigateTo(target)
			time.Sleep(3 * time.Second) // walk for a bit
			e.bot.StopMovement()
			waypoints++

			e.logger.Info("Exploration waypoint reached", "pos", target, "waypoints", waypoints)
		}

		// Advance spiral
		angle += 30.0
		if angle >= 360.0 {
			angle -= 360.0
			radius += float64(waypointInterval)
		}
	}

	e.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "explore",
		Item:    "spiral",
		Count:   waypoints,
		Success: true,
	})
	return waypoints
}

// ExploreRandom explores randomly in different directions
func (e *Explorer) ExploreRandom(ctx context.Context, duration time.Duration) int {
	e.mu.Lock()
	e.isExploring = true
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.isExploring = false
		e.mu.Unlock()
	}()

	waypoints := 0
	deadline := time.Now().Add(duration)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			e.bot.ReportActionStatus("", event.ActionStatus{
				Action:  "explore",
				Item:    "random",
				Count:   waypoints,
				Success: false,
				Error:   "dihentikan",
			})
			return waypoints
		default:
		}

		pos := e.bot.GetCoords()
		// Random direction, 20-60 blocks away
		angle := rand.Float64() * math.Pi * 2
		dist := 20.0 + rand.Float64()*40.0

		x := pos.X() + float32(math.Cos(angle))*float32(dist)
		z := pos.Z() + float32(math.Sin(angle))*float32(dist)
		target := mgl32.Vec3{x, pos.Y(), z}

		e.bot.NavigateTo(target)
		time.Sleep(5 * time.Second)
		e.bot.StopMovement()
		waypoints++

		e.logger.Info("Random exploration waypoint", "pos", target)
	}

	e.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "explore",
		Item:    "random",
		Count:   waypoints,
		Success: true,
	})
	return waypoints
}

// ExploreDirection explores in a specific compass direction
func (e *Explorer) ExploreDirection(ctx context.Context, direction string, distance int) int {
	e.mu.Lock()
	e.isExploring = true
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.isExploring = false
		e.mu.Unlock()
	}()

	// Map direction to angle
	var angle float64
	switch direction {
	case "north", "utara":
		angle = 270
	case "south", "selatan":
		angle = 90
	case "east", "timur":
		angle = 0
	case "west", "barat":
		angle = 180
	case "northeast", "timur_laut":
		angle = 315
	case "northwest", "barat_laut":
		angle = 225
	case "southeast", "tenggara":
		angle = 45
	case "southwest", "barat_daya":
		angle = 135
	default:
		e.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "exploredir",
			Item:    direction,
			Count:   0,
			Success: false,
			Error:   "arah tidak dikenal",
		})
		return 0
	}

	waypoints := 0
	pos := e.bot.GetCoords()
	stepSize := 30

	for traveled := 0; traveled < distance; traveled += stepSize {
		select {
		case <-ctx.Done():
			return waypoints
		default:
		}

		x := pos.X() + float32(math.Cos(angle*math.Pi/180))*float32(traveled)
		z := pos.Z() + float32(math.Sin(angle*math.Pi/180))*float32(traveled)
		target := mgl32.Vec3{x, pos.Y(), z}

		e.bot.NavigateTo(target)
		time.Sleep(4 * time.Second)
		e.bot.StopMovement()
		waypoints++
	}

	e.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "exploredir",
		Item:    direction,
		Count:   distance,
		Success: true,
	})
	return waypoints
}

// ReturnToOrigin navigates back to where exploration started
func (e *Explorer) ReturnToOrigin(ctx context.Context) bool {
	e.mu.Lock()
	origin := e.originPos
	e.mu.Unlock()

	if origin.X() == 0 && origin.Y() == 0 && origin.Z() == 0 {
		e.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "returnhome",
			Item:    "origin",
			Count:   0,
			Success: false,
			Error:   "tidak tahu posisi asal",
		})
		return false
	}

	e.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "returnhome",
		Item:    "origin",
		Count:   0,
		Success: true,
	})
	e.bot.NavigateTo(origin)

	deadline := time.After(60 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		case <-ticker.C:
			pos := e.bot.GetCoords()
			if pos.Sub(origin).Len() < 5.0 {
				e.bot.StopMovement()
				e.bot.ReportActionStatus("", event.ActionStatus{
					Action:  "returnhome",
					Item:    "origin",
					Count:   0,
					Success: true,
				})
				return true
			}
		}
	}
}

// IsExploring returns whether currently exploring
func (e *Explorer) IsExploring() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isExploring
}

// Stop stops exploration
func (e *Explorer) Stop() {
	e.mu.Lock()
	e.isExploring = false
	e.mu.Unlock()
	e.bot.StopMovement()
}

func areaKey(pos mgl32.Vec3) string {
	// Chunk-level key (16x16 blocks)
	cx := int(math.Floor(float64(pos.X()))) >> 4
	cz := int(math.Floor(float64(pos.Z()))) >> 4
	return string(rune(cx)) + "," + string(rune(cz))
}
