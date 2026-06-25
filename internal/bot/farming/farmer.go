package farming

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
	GetEntityRuntimeID() uint64
	GetLocalWorldModel() entity.WorldModel
	GetBlockName(x, y, z int32) (string, bool)
	DropItem(name string, count int) error
}

// Farmer handles all farming operations: planting, harvesting, hoeing
type Farmer struct {
	bot         Bot
	logger      *slog.Logger
	mu          sync.Mutex
	isFarming   bool

	// Known farm plot positions
	farmPlots   []protocol.BlockPos
}

func NewFarmer(bot Bot, logger *slog.Logger) *Farmer {
	return &Farmer{
		bot:    bot,
		logger: logger,
	}
}

// Crop types and their seed/block mappings
type cropInfo struct {
	name     string
	seedName string
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
	f.mu.Lock()
	f.isFarming = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.isFarming = false
		f.mu.Unlock()
	}()

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
		f.bot.SendChat(f.harvestReport(cropType, harvested))
	}
	return harvested
}

// PlantSeeds plants seeds on nearby farmland
func (f *Farmer) PlantSeeds(ctx context.Context, cropType string, maxCount int) int {
	f.mu.Lock()
	f.isFarming = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.isFarming = false
		f.mu.Unlock()
	}()

	crop, ok := crops[cropType]
	if !ok {
		f.bot.SendChat("Aku gak tau cara tanam " + cropType)
		return 0
	}

	inv := f.bot.GetInventorySlots()
	names := f.bot.GetItemNames()

	// Find seeds in inventory
	var seedSlot uint32
	found := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, crop.seedName) {
			seedSlot = slot
			found = true
			break
		}
	}
	if !found {
		f.bot.SendChat("Aku gak punya benih " + cropType + "!")
		return 0
	}

	if err := f.bot.EquipItem(seedSlot); err != nil {
		return 0
	}

	planted := 0
	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	// Scan for farmland (tilled soil)
	radius := int32(16)
	for dx := -radius; dx <= radius && planted < maxCount; dx++ {
		for dz := -radius; dz <= radius && planted < maxCount; dz++ {
			x, y, z := bx+dx, by-1, bz+dz

			name, ok := f.bot.GetBlockName(x, y, z)
			if !ok {
				continue
			}

			// Check if it's farmland and empty above
			if !strings.Contains(strings.ToLower(name), "farmland") {
				continue
			}

			aboveName, aboveOk := f.bot.GetBlockName(x, y+1, z)
			if aboveOk && aboveName != "" && aboveName != "air" {
				continue // already planted
			}

			// Navigate and plant
			if f.bot.NavigateToBlock(x, y+1, z, 2.5) {
				f.bot.StopMovement()
				f.bot.LookAt(mgl32.Vec3{float32(x) + 0.5, float32(y) + 1.0, float32(z) + 0.5})
				time.Sleep(100 * time.Millisecond)

				// Place seed on farmland
				tx := &packet.InventoryTransaction{
					TransactionData: &protocol.UseItemTransactionData{
						ActionType:      protocol.UseItemActionClickBlock,
						BlockPosition:   protocol.BlockPos{x, y, z},
						BlockFace:       1,
						HotBarSlot:      int32(seedSlot),
						HeldItem:        protocol.ItemInstance{Stack: inv[seedSlot]},
						Position:        f.bot.GetCoords(),
						ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
					},
				}
				_ = f.bot.WritePacket(tx)
				planted++
				time.Sleep(250 * time.Millisecond)
			}

			select {
			case <-ctx.Done():
				return planted
			default:
			}
		}
	}

	if planted > 0 {
		f.bot.SendChat(f.plantReport(cropType, planted))
	}
	return planted
}

// HoeGround tills dirt/grass blocks into farmland
func (f *Farmer) HoeGround(ctx context.Context, radius int32) int {
	inv := f.bot.GetInventorySlots()
	names := f.bot.GetItemNames()

	// Find hoe in inventory
	var hoeSlot uint32
	found := false
	hoeTypes := []string{"netherite_hoe", "diamond_hoe", "iron_hoe", "stone_hoe", "golden_hoe", "wooden_hoe"}
	for _, hoeName := range hoeTypes {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			if strings.Contains(name, hoeName) {
				hoeSlot = slot
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		f.bot.SendChat("Aku gak punya cangkul (hoe)!")
		return 0
	}

	if err := f.bot.EquipItem(hoeSlot); err != nil {
		return 0
	}

	hoed := 0
	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			x, y, z := bx+dx, by-1, bz+dz

			name, ok := f.bot.GetBlockName(x, y, z)
			if !ok {
				continue
			}

			nameLower := strings.ToLower(name)
			if !strings.Contains(nameLower, "dirt") && !strings.Contains(nameLower, "grass_block") {
				continue
			}

			// Must have air above
			aboveName, aboveOk := f.bot.GetBlockName(x, y+1, z)
			if aboveOk && aboveName != "" && aboveName != "air" {
				continue
			}

			if f.bot.NavigateToBlock(x, y+1, z, 2.5) {
				f.bot.StopMovement()
				f.bot.LookAt(mgl32.Vec3{float32(x) + 0.5, float32(y) + 1.0, float32(z) + 0.5})
				time.Sleep(100 * time.Millisecond)

				tx := &packet.InventoryTransaction{
					TransactionData: &protocol.UseItemTransactionData{
						ActionType:      protocol.UseItemActionClickBlock,
						BlockPosition:   protocol.BlockPos{x, y, z},
						BlockFace:       1,
						HotBarSlot:      int32(hoeSlot),
						HeldItem:        protocol.ItemInstance{Stack: inv[hoeSlot]},
						Position:        f.bot.GetCoords(),
						ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
					},
				}
				_ = f.bot.WritePacket(tx)
				hoed++
				time.Sleep(250 * time.Millisecond)
			}

			select {
			case <-ctx.Done():
				return hoed
			default:
			}
		}
	}

	if hoed > 0 {
		f.bot.SendChat("Selesai mencangkul tanah!")
	}
	return hoed
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
			HotBarSlot:      int32(f.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{},
			Position:        f.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 0.5, 0.5},
		},
	}
	_ = f.bot.WritePacket(tx)
}

func (f *Farmer) harvestReport(cropType string, count int) string {
	if cropType == "" {
		cropType = "semua crops"
	}
	return "Panen " + cropType + " selesai! Dapat " + string(rune('0'+count%10)) + " block."
}

func (f *Farmer) plantReport(cropType string, count int) string {
	return "Selesai menanam " + string(rune('0'+count%10)) + " " + cropType + "!"
}
