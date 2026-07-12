// Package farming implements bot farming operations: planting, harvesting,
// and tilling.
package farming

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot interface for farming subsystem
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
	DropItem(name string, count int) error
}

// Farmer handles all farming operations: planting, harvesting, hoeing
type Farmer struct {
	bot       Bot
	logger    *slog.Logger
	mu        sync.Mutex
	isFarming bool
}

func NewFarmer(bot Bot, logger *slog.Logger) *Farmer {
	return &Farmer{
		bot:    bot,
		logger: logger,
	}
}

// Crop types and their seed/block mappings
type cropInfo struct {
	name       string
	seedName   string
	blockNames []string
	grownName  string
}

var crops = map[string]cropInfo{
	"wheat": {
		name:       "wheat",
		seedName:   "wheat_seeds",
		blockNames: []string{"wheat", "wheat_seeds"},
		grownName:  "wheat",
	},
	"carrot": {
		name:       "carrot",
		seedName:   "carrot",
		blockNames: []string{"carrots"},
		grownName:  "carrots",
	},
	"potato": {
		name:       "potato",
		seedName:   "potato",
		blockNames: []string{"potatoes"},
		grownName:  "potatoes",
	},
	"beetroot": {
		name:       "beetroot",
		seedName:   "beetroot_seeds",
		blockNames: []string{"beetroots"},
		grownName:  "beetroots",
	},
	"pumpkin": {
		name:       "pumpkin",
		seedName:   "pumpkin_seeds",
		blockNames: []string{"pumpkin_stem"},
		grownName:  "pumpkin",
	},
	"melon": {
		name:       "melon",
		seedName:   "melon_seeds",
		blockNames: []string{"melon_stem"},
		grownName:  "melon_block",
	},
	"sugar_cane": {
		name:       "sugar_cane",
		seedName:   "sugar_cane",
		blockNames: []string{"reeds", "sugar_cane"},
		grownName:  "reeds",
	},
	"cactus": {
		name:       "cactus",
		seedName:   "cactus",
		blockNames: []string{"cactus"},
		grownName:  "cactus",
	},
}

// HarvestCrops finds and harvests fully grown crops
func (f *Farmer) HarvestCrops(ctx context.Context, cropType string, maxCount int) int {
	f.setFarmingState(true)
	defer f.setFarmingState(false)

	harvested := 0
	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	// Scan for crops in radius
	radius := int32(24)
	for dx := -radius; dx <= radius && harvested < maxCount; dx++ {
		for dy := int32(-3); dy <= 3 && harvested < maxCount; dy++ {
			for dz := -radius; dz <= radius && harvested < maxCount; dz++ {
				x, y, z := bx+dx, by+dy, bz+dz
				name, ok := f.bot.GetBlockName(x, y, z)
				if !ok {
					continue
				}

				if f.isHarvestableCrop(name, cropType) {
					target := protocol.BlockPos{x, y, z}
					if f.bot.NavigateToBlock(x, y, z, 3.0) {
						f.bot.StopMovement()
						f.bot.LookAt(mgl32.Vec3{float32(x) + 0.5, float32(y) + 0.5, float32(z) + 0.5})
						time.Sleep(100 * time.Millisecond)
						f.breakBlock(target)
						harvested++
						time.Sleep(200 * time.Millisecond)
					}
				}

				select {
				case <-ctx.Done():
					return harvested
				default:
				}
			}
		}
	}

	if harvested > 0 {
		item := cropType
		if item == "" {
			item = "mixed_crops"
		}
		f.bot.ReportActionStatus("", event.ActionStatus{Action: "harvest", Item: item, Count: harvested, Success: true})
	}
	return harvested
}

// PlantSeeds plants seeds on nearby farmland
func (f *Farmer) PlantSeeds(ctx context.Context, cropType string, maxCount int) int {
	f.setFarmingState(true)
	defer f.setFarmingState(false)

	crop, ok := crops[cropType]
	if !ok {
		f.bot.ReportActionStatus("", event.ActionStatus{Action: "plant", Item: cropType, Count: 0, Success: false, Error: "gak tau cara tanam " + cropType})
		return 0
	}

	seedSlot, found := f.findSeedSlot(crop.seedName)
	if !found {
		f.bot.ReportActionStatus("", event.ActionStatus{Action: "plant", Item: crop.seedName, Count: 0, Success: false, Error: "gak punya benih " + cropType})
		return 0
	}

	if err := f.bot.EquipItem(seedSlot); err != nil {
		return 0
	}

	planted := f.plantInFarmland(ctx, seedSlot, maxCount)
	if planted > 0 {
		f.bot.ReportActionStatus("", event.ActionStatus{Action: "plant", Item: cropType, Count: planted, Success: true})
	}
	return planted
}

func (f *Farmer) setFarmingState(active bool) {
	f.mu.Lock()
	f.isFarming = active
	f.mu.Unlock()
}

func (f *Farmer) findSeedSlot(seedName string) (uint32, bool) {
	inv := f.bot.GetInventorySlots()
	names := f.bot.GetItemNames()

	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, seedName) {
			return slot, true
		}
	}
	return 0, false
}

func (f *Farmer) plantInFarmland(ctx context.Context, seedSlot uint32, maxCount int) int {
	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	planted := 0
	radius := int32(16)
	for dx := -radius; dx <= radius && planted < maxCount; dx++ {
		for dz := -radius; dz <= radius && planted < maxCount; dz++ {
			if f.plantAtFarmland(ctx, bx+dx, by-1, bz+dz, seedSlot) {
				planted++
			}
			select {
			case <-ctx.Done():
				return planted
			default:
			}
		}
	}
	return planted
}

func (f *Farmer) plantAtFarmland(ctx context.Context, x, y, z int32, seedSlot uint32) bool {
	name, ok := f.bot.GetBlockName(x, y, z)
	if !ok || !strings.Contains(strings.ToLower(name), "farmland") {
		return false
	}

	aboveName, aboveOk := f.bot.GetBlockName(x, y+1, z)
	if aboveOk && aboveName != "" && aboveName != "air" {
		return false
	}

	if !f.bot.NavigateToBlock(x, y+1, z, 2.5) {
		return false
	}

	f.bot.StopMovement()
	f.bot.LookAt(mgl32.Vec3{float32(x) + 0.5, float32(y) + 1.0, float32(z) + 0.5})
	time.Sleep(100 * time.Millisecond)

	f.useItemAtSlot(x, y, z, seedSlot)
	time.Sleep(250 * time.Millisecond)
	return true
}

// HoeGround tills dirt/grass blocks into farmland
func (f *Farmer) HoeGround(ctx context.Context, radius int32) int {
	hoeSlot, found := f.findHoeSlot()
	if !found {
		f.bot.ReportActionStatus("", event.ActionStatus{Action: "hoe", Item: "hoe", Count: 0, Success: false, Error: "gak punya cangkul"})
		return 0
	}

	if err := f.bot.EquipItem(hoeSlot); err != nil {
		return 0
	}

	hoed := f.hoeDirt(ctx, radius, hoeSlot)
	if hoed > 0 {
		f.bot.ReportActionStatus("", event.ActionStatus{Action: "hoe", Item: "farmland", Count: hoed, Success: true})
	}
	return hoed
}

func (f *Farmer) findHoeSlot() (uint32, bool) {
	inv := f.bot.GetInventorySlots()
	names := f.bot.GetItemNames()
	hoeTypes := []string{"netherite_hoe", "diamond_hoe", "iron_hoe", "stone_hoe", "golden_hoe", "wooden_hoe"}

	for _, hoeName := range hoeTypes {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			if strings.Contains(name, hoeName) {
				return slot, true
			}
		}
	}
	return 0, false
}

func (f *Farmer) hoeDirt(ctx context.Context, radius int32, hoeSlot uint32) int {
	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	hoed := 0
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			if f.hoeAtBlock(ctx, bx+dx, by-1, bz+dz, hoeSlot) {
				hoed++
			}
			select {
			case <-ctx.Done():
				return hoed
			default:
			}
		}
	}
	return hoed
}

func (f *Farmer) hoeAtBlock(ctx context.Context, x, y, z int32, hoeSlot uint32) bool {
	name, ok := f.bot.GetBlockName(x, y, z)
	if !ok {
		return false
	}

	nameLower := strings.ToLower(name)
	if !strings.Contains(nameLower, "dirt") && !strings.Contains(nameLower, "grass_block") {
		return false
	}

	aboveName, aboveOk := f.bot.GetBlockName(x, y+1, z)
	if aboveOk && aboveName != "" && aboveName != "air" {
		return false
	}

	if !f.bot.NavigateToBlock(x, y+1, z, 2.5) {
		return false
	}

	f.bot.StopMovement()
	f.bot.LookAt(mgl32.Vec3{float32(x) + 0.5, float32(y) + 1.0, float32(z) + 0.5})
	time.Sleep(100 * time.Millisecond)

	f.useItemAtSlot(x, y, z, hoeSlot)
	time.Sleep(250 * time.Millisecond)
	return true
}

func (f *Farmer) useItemAtSlot(x, y, z int32, slot uint32) {
	inv := f.bot.GetInventorySlots()
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{x, y, z},
			BlockFace:       1,
			HotBarSlot:      safecast.To[int32](slot),
			HeldItem:        protocol.ItemInstance{Stack: inv[slot]},
			Position:        f.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	_ = f.bot.WritePacket(tx)
}

// Stop stops current farming operation
func (f *Farmer) Stop() {
	f.mu.Lock()
	f.isFarming = false
	f.mu.Unlock()
	f.bot.StopMovement()
}

// IsFarming returns whether the farmer is currently farming
func (f *Farmer) IsFarming() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.isFarming
}

// Helper functions
func (f *Farmer) isHarvestableCrop(blockName, cropType string) bool {
	name := strings.ToLower(blockName)
	if cropType != "" {
		crop, ok := crops[cropType]
		if !ok {
			return false
		}
		for _, bn := range crop.blockNames {
			if strings.Contains(name, bn) {
				return true
			}
		}
		return false
	}
	// Auto-detect any mature crop
	return strings.Contains(name, "wheat") ||
		strings.Contains(name, "carrots") ||
		strings.Contains(name, "potatoes") ||
		strings.Contains(name, "beetroots") ||
		strings.Contains(name, "pumpkin") ||
		strings.Contains(name, "melon_block")
}

func (f *Farmer) breakBlock(pos protocol.BlockPos) {
	f.bot.LookAt(mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5})
	time.Sleep(50 * time.Millisecond)

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionBreakBlock,
			BlockPosition:   pos,
			BlockFace:       1,
			HotBarSlot:      safecast.To[int32](f.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{},
			Position:        f.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 0.5, 0.5},
		},
	}
	_ = f.bot.WritePacket(tx)
}
