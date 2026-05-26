package placer

import (
	"context"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (bp *BlockPlacer) navigateAndTowerToTarget(ctx context.Context, x, y, z int, cx, cz int) bool {
	botPos := bp.bot.GetCoords()
	by := int(math.Floor(float64(botPos.Y())))

	dx := float64(x) + 0.5 - float64(botPos.X())
	dy := float64(y) - float64(botPos.Y())
	dz := float64(z) + 0.5 - float64(botPos.Z())
	distH := math.Sqrt(dx*dx + dz*dz)

	if distH > 4.2 || math.Abs(dy) > 3.0 {
		bp.logger.Info("Target out of range, navigating closer", "distH", distH, "dy", dy)
		reached := bp.bot.NavigateToBlock(int32(x), int32(y), int32(z), 2.5)
		if !reached {
			bp.logger.Warn("Failed to navigate close to block placement target")
			bp.bot.NavigateToBlock(int32(cx), int32(y), int32(cz), 3.0)
			time.Sleep(500 * time.Millisecond)
		}
		botPos = bp.bot.GetCoords()
		by = int(math.Floor(float64(botPos.Y())))
	}

	if y > by+2 {
		bp.logger.Info("Target is too high, towering up", "targetY", y, "botY", by)
		if !bp.TowerUp(ctx, y-1) {
			bp.logger.Warn("Tower up failed")
			return false
		}
	} else if by > y+3 {
		bp.logger.Info("Target is too low, descending safely", "targetY", y, "botY", by)
		if !bp.DescendTo(ctx, y+1) {
			bp.logger.Warn("Descend failed")
			return false
		}
	}
	return true
}

func (bp *BlockPlacer) placeScaffoldScaffolding(scaffSlot uint32, inv map[uint32]protocol.ItemStack, x, y, z int) {
	_ = bp.bot.EquipItem(scaffSlot)
	time.Sleep(100 * time.Millisecond)

	scaffPos := protocol.BlockPos{int32(x), int32(y - 1), int32(z)}
	bp.lookAtBlock(scaffPos)
	time.Sleep(100 * time.Millisecond)

	world := bp.bot.GetLocalWorldModel()
	if world.IsSolid(int32(x), int32(y-2), int32(z)) {
		tx := &packet.InventoryTransaction{
			TransactionData: &protocol.UseItemTransactionData{
				ActionType:      protocol.UseItemActionClickBlock,
				BlockPosition:   protocol.BlockPos{int32(x), int32(y - 2), int32(z)},
				BlockFace:       1,
				HotBarSlot:      int32(bp.bot.GetHeldItemSlot()),
				HeldItem:        protocol.ItemInstance{Stack: inv[scaffSlot]},
				Position:        bp.bot.GetCoords(),
				ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
			},
		}
		_ = bp.bot.WritePacket(tx)
		world.SetSolid(int32(x), int32(y-1), int32(z), true)
		bp.ScaffoldHistory = append(bp.ScaffoldHistory, scaffPos)
		time.Sleep(150 * time.Millisecond)
	}
}
