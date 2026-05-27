package inventory

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/inventory/chest"
	"bedrock-ai/internal/bot/inventory/furnace"
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
	FindPlayer(username string) (uint64, mgl32.Vec3, bool)
}

type InventoryManager struct {
	bot     Bot
	logger  *slog.Logger
	chest   *chest.Container
	furnace *furnace.Manager
}

func NewInventoryManager(bot Bot, logger *slog.Logger) *InventoryManager {
	im := &InventoryManager{
		bot:    bot,
		logger: logger,
	}
	im.chest = chest.NewContainer(bot, logger)
	im.furnace = furnace.NewManager(bot, logger)
	return im
}

func (im *InventoryManager) Chest() *chest.Container {
	return im.chest
}

func (im *InventoryManager) Furnace() *furnace.Manager {
	return im.furnace
}

func (im *InventoryManager) EquipItem(name string) error {
	inv := im.bot.GetInventorySlots()
	names := im.bot.GetItemNames()

	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		itemName := names[item.NetworkID]
		if strings.Contains(strings.ToLower(itemName), strings.ToLower(name)) {
			return im.bot.EquipItem(slot)
		}
	}
	return fmt.Errorf("item %s not found in inventory", name)
}

func (im *InventoryManager) UnequipItem() error {
	return im.bot.UnequipItem()
}

func (im *InventoryManager) DropItem(name string, count int) error {
	return im.bot.DropItem(name, count)
}

func (im *InventoryManager) Eat(foodName string) error {
	inv := im.bot.GetInventorySlots()
	names := im.bot.GetItemNames()

	var foodSlot uint32
	found := false

	// Common food items
	foodList := []string{
		"apple", "bread", "cooked_beef", "cooked_chicken", "cooked_porkchop",
		"cooked_mutton", "cooked_salmon", "cooked_cod", "baked_potato", "carrot",
	}

	if foodName != "" {
		foodList = []string{foodName}
	}

	for _, food := range foodList {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := names[item.NetworkID]
			if strings.Contains(strings.ToLower(name), food) {
				foodSlot = slot
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return fmt.Errorf("no food found")
	}

	// Equip food
	if err := im.bot.EquipItem(foodSlot); err != nil {
		return err
	}

	// Simulate eating: hold use item packet for 1.6 seconds (approx 32 ticks)
	item := inv[foodSlot]
	useTx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock, // standard use item on self
			BlockPosition:   protocol.BlockPos{0, -1, 0},
			BlockFace:       255, // special face indicating self
			HotBarSlot:      int32(im.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{Stack: item},
			Position:        im.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0, 0, 0},
		},
	}

	_ = im.bot.WritePacket(useTx)
	time.Sleep(1600 * time.Millisecond)

	im.logger.Info("Ate food successfully", "slot", foodSlot)
	return nil
}
