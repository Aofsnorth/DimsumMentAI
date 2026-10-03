package placer

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"bedrock-ai/internal/bot/building/common"
	"bedrock-ai/internal/bot/building/schematic"
	"bedrock-ai/internal/bot/interact"
	"bedrock-ai/internal/safecast"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// BlockPlacer handles low-level block placement, scaffolding, tower-up, and descend actions.
type BlockPlacer struct {
	bot             common.BotInterface
	logger          *slog.Logger
	ScaffoldHistory []protocol.BlockPos
}

// NewBlockPlacer creates a new BlockPlacer instance.
func NewBlockPlacer(bot common.BotInterface, logger *slog.Logger) *BlockPlacer {
	return &BlockPlacer{
		bot:    bot,
		logger: logger,
	}
}

// PlaceBlockAt attempts to place a block at the specified coordinates.
func (bp *BlockPlacer) PlaceBlockAt(ctx context.Context, x, y, z int, blockName string, cx, cz int, metadata *int) bool {
	if bp.bot == nil {
		return false
	}

	blockName = strings.ReplaceAll(blockName, "minecraft:", "")
	bp.logger.Info("Attempting to place block", "block", blockName, "x", x, "y", y, "z", z)

	// A site the bot is not willing to clear is a site it must not build into
	// either. Placing anyway would put a block inside somebody's chest.
	if !bp.ClearObstructions(ctx, x, y, z) {
		return false
	}

	if !bp.navigateAndTowerToTarget(ctx, x, y, z, cx, cz) {
		return false
	}

	if blockName == "farmland" || blockName == "dirt_path" {
		return bp.placeSpecialBlock(ctx, x, y, z, blockName)
	}

	inv := bp.bot.GetInventorySlots()
	names := bp.bot.GetItemNames()
	slot, found := schematic.FindItemInSlots(inv, names, blockName)
	if !found {
		var buildItems []common.BuildItem
		for s, stack := range inv {
			if stack.Count > 0 {
				buildItems = append(buildItems, common.BuildItem{Slot: s, Name: names[stack.NetworkID], Count: int(stack.Count)})
			}
		}
		subName := schematic.FindSubstitute(blockName, buildItems)
		slot, found = schematic.FindItemInSlots(inv, names, subName)
		if !found {
			bp.logger.Warn("Required block not found in inventory", "block", blockName)
			return false
		}
		bp.logger.Info("Using substituted material", "original", blockName, "substitute", subName)
	}

	_ = bp.bot.EquipItem(slot)
	time.Sleep(150 * time.Millisecond)

	placeTarget, placeFace := bp.findSupportFace(x, y, z)

	if placeFace == -1 {
		bp.logger.Info("Floating block detected, placing temporary scaffold underneath", "x", x, "y", y-1, "z", z)
		scaffSlot, scaffFound := schematic.FindScaffoldForTower(inv, names)
		if scaffFound {
			bp.placeScaffoldScaffolding(scaffSlot, inv, x, y, z)
			placeTarget = protocol.BlockPos{safecast.To[int32](x), safecast.To[int32](y - 1), safecast.To[int32](z)}
			placeFace = 1

			_ = bp.bot.EquipItem(slot)
			time.Sleep(100 * time.Millisecond)
		}
	}

	if placeFace == -1 {
		bp.logger.Warn("Could not find suitable placement surface for block", "x", x, "y", y, "z", z)
		return false
	}

	bp.lookAtBlock(placeTarget)
	time.Sleep(100 * time.Millisecond)

	targetName := names[inv[slot].NetworkID]
	shouldSneak := bp.isInteractableBlock(targetName)

	if shouldSneak {
		_ = bp.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStartSneak,
		})
		time.Sleep(50 * time.Millisecond)
	}

	itemStack := inv[slot]
	var newItem protocol.ItemInstance
	if itemStack.Count > 1 {
		newStack := itemStack
		newStack.Count--
		newItem = protocol.ItemInstance{Stack: newStack}
	}
	tx := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				WindowID:      protocol.Option(int8(protocol.WindowIDInventory)),
				InventorySlot: slot,
				OldItem:       protocol.ItemInstance{Stack: itemStack},
				NewItem:       newItem,
			},
		},
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:    protocol.UseItemActionClickBlock,
			TriggerType:   protocol.TriggerTypePlayerInput,
			BlockPosition: placeTarget,
			BlockFace:     placeFace,
			HotBarSlot:    safecast.To[int32](bp.bot.GetHeldItemSlot()),
			HeldItem:      protocol.ItemInstance{Stack: itemStack},
			Position:      bp.bot.GetCoords(),
			// On the clicked face, not the middle of the block. A block's
			// centre is inside it and on none of its faces, which is what the
			// two sibling placements in this package were already sending and
			// what the server rejects.
			ClickedPosition: interact.FaceClickedPosition(placeFace),
		},
	}
	err := bp.bot.WritePacket(tx)
	if err != nil {
		bp.logger.Error("Failed to write place block packet", "err", err.Error())
		return false
	}

	if shouldSneak {
		_ = bp.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStopSneak,
		})
	}

	bp.bot.GetLocalWorldModel().SetSolid(safecast.To[int32](x), safecast.To[int32](y), safecast.To[int32](z), true)
	time.Sleep(150 * time.Millisecond)
	return true
}
