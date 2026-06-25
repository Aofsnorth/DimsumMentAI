package fishing

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot interface for fishing subsystem
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
	GetEntityRuntimeID() uint64
	GetLocalWorldModel() entity.WorldModel
	GetBlockName(x, y, z int32) (string, bool)
}

// Fisher handles fishing rod operations
type Fisher struct {
	bot       Bot
	logger    *slog.Logger
	mu        sync.Mutex
	isFishing bool
	hookOut   bool
	fishCount int
}

func NewFisher(bot Bot, logger *slog.Logger) *Fisher {
	return &Fisher{
		bot:    bot,
		logger: logger,
	}
}

// FindWater finds the nearest water block within radius
func (f *Fisher) FindWater(radius int32) (protocol.BlockPos, bool) {
	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	var best protocol.BlockPos
	bestDist := math.MaxFloat64
	found := false

	for dx := -radius; dx <= radius; dx++ {
		for dy := int32(-3); dy <= 3; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				name, ok := f.bot.GetBlockName(bx+dx, by+dy, bz+dz)
				if !ok {
					continue
				}
				nameLower := strings.ToLower(name)
				if strings.Contains(nameLower, "water") || strings.Contains(nameLower, "flowing_water") {
					d := float64(dx*dx + dy*dy + dz*dz)
					if d < bestDist {
						bestDist = d
						best = protocol.BlockPos{bx + dx, by + dy, bz + dz}
						found = true
					}
				}
			}
		}
	}
	return best, found
}

// FindFishingRod finds a fishing rod in the inventory
func (f *Fisher) FindFishingRod() (uint32, bool) {
	inv := f.bot.GetInventorySlots()
	names := f.bot.GetItemNames()

	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "fishing_rod") || strings.Contains(name, "fishing rod") {
			return slot, true
		}
	}
	return 0, false
}

// GoFish performs a full fishing cycle: find water, cast, wait, reel in
func (f *Fisher) GoFish(ctx context.Context, maxCatches int) int {
	f.mu.Lock()
	if f.isFishing {
		f.mu.Unlock()
		return 0
	}
	f.isFishing = true
	f.fishCount = 0
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.isFishing = false
		f.mu.Unlock()
	}()

	// Find fishing rod
	rodSlot, found := f.FindFishingRod()
	if !found {
		f.bot.SendChat("Aku gak punya fishing rod!")
		return 0
	}

	// Find water
	waterPos, found := f.FindWater(12)
	if !found {
		f.bot.SendChat("Gak ada air di sekitar sini buat mancing!")
		return 0
	}

	// Navigate to water's edge
	approach := protocol.BlockPos{waterPos.X(), waterPos.Y() + 1, waterPos.Z() + 1}
	if !f.bot.NavigateToBlock(approach.X(), approach.Y(), approach.Z(), 3.0) {
		f.bot.SendChat("Gak bisa sampai ke air buat mancing.")
		return 0
	}
	f.bot.StopMovement()

	// Equip fishing rod
	if err := f.bot.EquipItem(rodSlot); err != nil {
		return 0
	}

	f.bot.SendChat("Aku mancing dulu ya! 🎣")

	// Look at water
	f.bot.LookAt(mgl32.Vec3{float32(waterPos.X()) + 0.5, float32(waterPos.Y()) + 0.5, float32(waterPos.Z()) + 0.5})
	time.Sleep(300 * time.Millisecond)

	caught := 0
	for caught < maxCatches {
		select {
		case <-ctx.Done():
			f.reelIn(rodSlot)
			return caught
		default:
		}

		// Cast the line
		f.castLine(rodSlot)
		time.Sleep(500 * time.Millisecond)

		// Wait for fish bite (random 5-30 seconds)
		waitTime := 8 + time.Duration(caught%5)*3
		select {
		case <-ctx.Done():
			f.reelIn(rodSlot)
			return caught
		case <-time.After(time.Duration(waitTime) * time.Second):
		}

		// Reel in
		f.reelIn(rodSlot)
		caught++
		time.Sleep(1 * time.Second)

		f.logger.Info("Fish caught", "total", caught)
	}

	if caught > 0 {
		f.bot.SendChat(f.fishReport(caught))
	}
	return caught
}

// castLine casts the fishing rod
func (f *Fisher) castLine(rodSlot uint32) {
	inv := f.bot.GetInventorySlots()
	item := inv[rodSlot]

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{0, -1, 0},
			BlockFace:       255,
			HotBarSlot:      int32(rodSlot),
			HeldItem:        protocol.ItemInstance{Stack: item},
			Position:        f.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0, 0, 0},
		},
	}
	_ = f.bot.WritePacket(tx)

	f.mu.Lock()
	f.hookOut = true
	f.mu.Unlock()

	f.logger.Debug("Fishing rod cast")
}

// reelIn retracts the fishing rod
func (f *Fisher) reelIn(rodSlot uint32) {
	inv := f.bot.GetInventorySlots()
	item := inv[rodSlot]

	// Second use to reel in
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{0, -1, 0},
			BlockFace:       255,
			HotBarSlot:      int32(rodSlot),
			HeldItem:        protocol.ItemInstance{Stack: item},
			Position:        f.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0, 0, 0},
		},
	}
	_ = f.bot.WritePacket(tx)

	f.mu.Lock()
	f.hookOut = false
	f.mu.Unlock()

	f.logger.Debug("Fishing rod reeled in")
}

// Stop stops the current fishing operation
func (f *Fisher) Stop() {
	f.mu.Lock()
	f.isFishing = false
	f.mu.Unlock()
	f.bot.StopMovement()
}

// IsFishing returns whether currently fishing
func (f *Fisher) IsFishing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.isFishing
}

func (f *Fisher) fishReport(count int) string {
	return "Selesai mancing! Dapat sekitar " + string(rune('0'+count%10)) + " ikan. 🐟"
}
