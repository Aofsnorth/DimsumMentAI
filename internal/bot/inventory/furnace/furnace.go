package furnace

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type Bot interface {
	GetCoords() mgl32.Vec3
	WritePacket(pk packet.Packet) error
	GetEntities() map[uint64]*entity.Info
	NavigateTo(pos mgl32.Vec3)
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	InjectAIEvent(msg string)
	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	EquipItem(slot uint32) error
	UnequipItem() error
	SendChat(msg string)
	GetEntityRuntimeID() uint64
	GetLocalWorldModel() entity.WorldModel
	DropItem(name string, count int) error
}

type Manager struct {
	bot    Bot
	logger *slog.Logger
}

func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{
		bot:    bot,
		logger: logger,
	}
}

func (fm *Manager) SmeltItem(ctx context.Context, itemName string) bool {
	furnacePos := fm.findNearbyFurnace()
	if furnacePos == (protocol.BlockPos{}) {
		fm.logger.Warn("SmeltItem: no furnace found nearby")
		return false
	}

	botPos := fm.bot.GetCoords()
	dist := fm.distance(botPos, mgl32.Vec3{float32(furnacePos.X()), float32(furnacePos.Y()), float32(furnacePos.Z())})
	if dist > 3.5 {
		reached := fm.bot.NavigateToBlock(furnacePos.X(), furnacePos.Y(), furnacePos.Z(), 3.0)
		if !reached {
			return false
		}
		fm.bot.StopMovement()
	}

	fm.bot.LookAt(mgl32.Vec3{float32(furnacePos.X()) + 0.5, float32(furnacePos.Y()) + 0.5, float32(furnacePos.Z()) + 0.5})
	time.Sleep(200 * time.Millisecond)

	_ = fm.bot.WritePacket(&packet.Interact{
		ActionType:            6,
		TargetEntityRuntimeID: fm.bot.GetEntityRuntimeID(),
		Position:              protocol.Option(mgl32.Vec3{float32(furnacePos.X()), float32(furnacePos.Y()), float32(furnacePos.Z())}),
	})
	time.Sleep(500 * time.Millisecond)

	inv := fm.bot.GetInventorySlots()
	names := fm.bot.GetItemNames()

	var rawSlot uint32
	foundRaw := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := names[item.NetworkID]
		if strings.Contains(strings.ToLower(name), strings.ToLower(itemName)) {
			rawSlot = slot
			foundRaw = true
			break
		}
	}

	if !foundRaw {
		_ = fm.bot.WritePacket(&packet.ContainerClose{WindowID: 0})
		return false
	}

	rawItem := inv[rawSlot]
	tx1 := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				InventorySlot: rawSlot,
				OldItem:       protocol.ItemInstance{Stack: rawItem},
				NewItem:       protocol.ItemInstance{},
			},
		},
		TransactionData: &protocol.NormalTransactionData{},
	}
	_ = fm.bot.WritePacket(tx1)
	time.Sleep(100 * time.Millisecond)

	fuels := []string{"coal", "charcoal", "oak_planks", "spruce_planks", "birch_planks", "jungle_planks", "acacia_planks", "dark_oak_planks", "planks"}
	var fuelSlot uint32
	foundFuel := false
	for _, fName := range fuels {
		for slot, item := range inv {
			if item.Count <= 0 || slot == rawSlot {
				continue
			}
			name := names[item.NetworkID]
			if strings.Contains(strings.ToLower(name), fName) {
				fuelSlot = slot
				foundFuel = true
				break
			}
		}
		if foundFuel {
			break
		}
	}

	if foundFuel {
		fuelItem := inv[fuelSlot]
		tx2 := &packet.InventoryTransaction{
			Actions: []protocol.InventoryAction{
				{
					SourceType:    protocol.InventoryActionSourceContainer,
					InventorySlot: fuelSlot,
					OldItem:       protocol.ItemInstance{Stack: fuelItem},
					NewItem:       protocol.ItemInstance{},
				},
			},
			TransactionData: &protocol.NormalTransactionData{},
		}
		_ = fm.bot.WritePacket(tx2)
		time.Sleep(100 * time.Millisecond)
	}

	_ = fm.bot.WritePacket(&packet.ContainerClose{
		WindowID: 0,
	})

	fm.logger.Info("Smelting item in furnace successfully", "item", itemName)
	return true
}

func (fm *Manager) findNearbyFurnace() protocol.BlockPos {
	botPos := fm.bot.GetCoords()
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	world := fm.bot.GetLocalWorldModel()
	for r := int32(1); r <= 8; r++ {
		for dx := -r; dx <= r; dx++ {
			for dy := -r; dy <= r; dy++ {
				for dz := -r; dz <= r; dz++ {
					tx, ty, tz := bx+dx, by+dy, bz+dz
					if world.IsSolid(tx, ty, tz) {
						return protocol.BlockPos{tx, ty, tz}
					}
				}
			}
		}
	}
	return protocol.BlockPos{}
}

func (fm *Manager) distance(a mgl32.Vec3, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
