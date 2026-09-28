package placer

import (
	"context"
	"strings"
	"time"

	"bedrock-ai/internal/bot/interact"
	"bedrock-ai/internal/bot/movement/animation"
	"bedrock-ai/internal/bot/storage"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// clearObstructions makes room at a build site by breaking whatever is in the
// way, and reports whether it did.
//
// The default used to be "dig anything solid", which is how a bot ends up
// silently destroying a chest of somebody's storage because a schematic wanted
// that cell. A player clears a site for the things in their way; they do not
// quarry their own furniture. Storage, signage and anything else with a real
// interaction is therefore left alone, and the placement is abandoned instead —
// failing a build is a visible annoyance, quietly deleting someone's storage is
// a betrayal.
//
// A cell the bot has solidity data for but no name for is still dug, because
// that is ordinary terrain in a chunk whose names have not been decoded yet.
// The trade is deliberate: refusing every unnamed block would make building
// fail constantly, and the classifiers below already cover the blocks that
// actually hold anything worth keeping.
func (bp *BlockPlacer) clearObstructions(ctx context.Context, x, y, z int) bool {
	world := bp.bot.GetLocalWorldModel()
	px, py, pz := safecast.To[int32](x), safecast.To[int32](y), safecast.To[int32](z)

	if !world.IsSolid(px, py, pz) {
		return true
	}

	if name, known := bp.bot.GetBlockName(px, py, pz); known {
		if blockIsWorthKeeping(name) {
			bp.logger.Warn("Refusing to clear a build site: the block there is worth keeping",
				"x", x, "y", y, "z", z, "block", name)
			return false
		}
	}

	pos := protocol.BlockPos{px, py, pz}
	bp.logger.Info("Clearing block obstruction at placement site", "x", x, "y", y, "z", z)
	bp.digBlock(ctx, pos)
	world.SetSolid(px, py, pz, false)
	return true
}

// blockIsWorthKeeping reports whether a block should survive the bot clearing a
// build site.
//
// It reuses the two classifiers the rest of the bot already trusts rather than
// keeping a third list to drift out of date: storage matches chests, barrels,
// shulkers, hoppers and modded storage by suffix, and the interaction
// vocabulary covers doors, signage, workbenches and everything else with a
// behaviour attached to it.
func blockIsWorthKeeping(name string) bool {
	normalised := strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexByte(normalised, ':'); i >= 0 {
		normalised = normalised[i+1:]
	}
	return storage.IsContainerBlock(normalised) || interact.IsInteractiveBlockName(normalised)
}

func (bp *BlockPlacer) digBlock(ctx context.Context, pos protocol.BlockPos) {
	_ = bp.bot.WritePacket(animation.MineSwing(bp.bot.GetEntityRuntimeID()))
	_ = bp.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   pos,
		BlockFace:       1,
	})
	time.Sleep(300 * time.Millisecond)
	_ = bp.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionCrackBreak,
		BlockPosition:   pos,
		BlockFace:       1,
	})
	_ = bp.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionPredictDestroyBlock,
		BlockPosition:   pos,
		BlockFace:       1,
	})
	_ = bp.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStopBreak,
		BlockPosition:   pos,
		BlockFace:       1,
	})
	time.Sleep(100 * time.Millisecond)
}

func (bp *BlockPlacer) lookAtBlock(pos protocol.BlockPos) {
	bp.bot.LookAt(mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5})
}

func (bp *BlockPlacer) placeSpecialBlock(ctx context.Context, x, y, z int, name string) bool {
	inv := bp.bot.GetInventorySlots()
	names := bp.bot.GetItemNames()

	var toolName string
	if name == "farmland" {
		toolName = "hoe"
	} else if name == "dirt_path" {
		toolName = "shovel"
	}

	var toolSlot uint32
	found := false

	for slot, stack := range inv {
		if stack.Count > 0 {
			n := strings.ToLower(names[stack.NetworkID])
			if strings.Contains(n, toolName) {
				toolSlot = slot
				found = true
				break
			}
		}
	}

	if !found {
		bp.logger.Warn("Special block requested but tool not found in inventory", "block", name, "tool", toolName)
		return false
	}

	_ = bp.bot.EquipItem(toolSlot)
	time.Sleep(150 * time.Millisecond)

	targetPos := protocol.BlockPos{safecast.To[int32](x), safecast.To[int32](y - 1), safecast.To[int32](z)}
	bp.lookAtBlock(targetPos)
	time.Sleep(100 * time.Millisecond)

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   targetPos,
			BlockFace:       1,
			HotBarSlot:      safecast.To[int32](bp.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{Stack: inv[toolSlot]},
			Position:        bp.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	_ = bp.bot.WritePacket(tx)
	time.Sleep(200 * time.Millisecond)

	bp.bot.GetLocalWorldModel().SetSolid(safecast.To[int32](x), safecast.To[int32](y), safecast.To[int32](z), true)
	return true
}

func (bp *BlockPlacer) findSupportFace(x, y, z int) (protocol.BlockPos, int32) {
	world := bp.bot.GetLocalWorldModel()
	faces := []struct {
		offset protocol.BlockPos
		face   int32
	}{
		{protocol.BlockPos{0, -1, 0}, 1},
		{protocol.BlockPos{0, 1, 0}, 0},
		{protocol.BlockPos{0, 0, -1}, 3},
		{protocol.BlockPos{0, 0, 1}, 2},
		{protocol.BlockPos{-1, 0, 0}, 5},
		{protocol.BlockPos{1, 0, 0}, 4},
	}

	for _, f := range faces {
		adjX := safecast.To[int32](x) + f.offset.X()
		adjY := safecast.To[int32](y) + f.offset.Y()
		adjZ := safecast.To[int32](z) + f.offset.Z()

		if world.IsSolid(adjX, adjY, adjZ) {
			return protocol.BlockPos{adjX, adjY, adjZ}, f.face
		}
	}
	return protocol.BlockPos{}, -1
}

func (bp *BlockPlacer) isInteractableBlock(name string) bool {
	interactables := []string{"chest", "door", "furnace", "crafting_table", "hopper", "anvil", "trapdoor", "button", "lever"}
	for _, in := range interactables {
		if strings.Contains(strings.ToLower(name), in) {
			return true
		}
	}
	return false
}
