package placer

import (
	"context"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (bp *BlockPlacer) clearObstructions(ctx context.Context, x, y, z int) {
	world := bp.bot.GetLocalWorldModel()
	pos := protocol.BlockPos{int32(x), int32(y), int32(z)}

	if world.IsSolid(int32(x), int32(y), int32(z)) {
		bp.logger.Info("Clearing block obstruction at placement site", "x", x, "y", y, "z", z)
		bp.digBlock(ctx, pos)
		world.SetSolid(int32(x), int32(y), int32(z), false)
	}
}

func (bp *BlockPlacer) digBlock(ctx context.Context, pos protocol.BlockPos) {
	_ = bp.bot.WritePacket(&packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
	})
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

	targetPos := protocol.BlockPos{int32(x), int32(y - 1), int32(z)}
	bp.lookAtBlock(targetPos)
	time.Sleep(100 * time.Millisecond)

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   targetPos,
			BlockFace:       1,
			HotBarSlot:      int32(bp.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{Stack: inv[toolSlot]},
			Position:        bp.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	_ = bp.bot.WritePacket(tx)
	time.Sleep(200 * time.Millisecond)

	bp.bot.GetLocalWorldModel().SetSolid(int32(x), int32(y), int32(z), true)
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
		adjX := int32(x) + f.offset.X()
		adjY := int32(y) + f.offset.Y()
		adjZ := int32(z) + f.offset.Z()

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
