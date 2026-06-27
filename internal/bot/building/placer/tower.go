package placer

import (
	"context"
	"math"
	"time"

	"bedrock-ai/internal/bot/building/schematic"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// TowerUp builds a vertical tower under the bot to climb up.
func (bp *BlockPlacer) TowerUp(ctx context.Context, targetY int) bool {
	inv := bp.bot.GetInventorySlots()
	names := bp.bot.GetItemNames()

	scaffSlot, found := schematic.FindScaffoldForTower(inv, names)
	if !found {
		bp.logger.Warn("TowerUp failed: no scaffold materials found in inventory")
		return false
	}

	_ = bp.bot.EquipItem(scaffSlot)
	time.Sleep(100 * time.Millisecond)

	botPos := bp.bot.GetCoords()
	by := int(math.Floor(float64(botPos.Y())))

	for by < targetY {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		bx := int32(math.Floor(float64(botPos.X())))
		bz := int32(math.Floor(float64(botPos.Z())))
		world := bp.bot.GetLocalWorldModel()

		if world.IsSolid(bx, int32(by+2), bz) {
			bp.digBlock(ctx, protocol.BlockPos{bx, int32(by + 2), bz})
		}

		bp.bot.LookAt(mgl32.Vec3{botPos.X(), botPos.Y() - 1.0, botPos.Z()})
		time.Sleep(100 * time.Millisecond)

		_ = bp.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionJump,
		})

		botPos = mgl32.Vec3{botPos.X(), botPos.Y() + 1.1, botPos.Z()}

		scaffPos := protocol.BlockPos{bx, int32(by), bz}
		tx := &packet.InventoryTransaction{
			TransactionData: &protocol.UseItemTransactionData{
				ActionType:      protocol.UseItemActionClickBlock,
				BlockPosition:   protocol.BlockPos{bx, int32(by - 1), bz},
				BlockFace:       1,
				HotBarSlot:      int32(bp.bot.GetHeldItemSlot()),
				HeldItem:        protocol.ItemInstance{Stack: inv[scaffSlot]},
				Position:        botPos,
				ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
			},
		}
		_ = bp.bot.WritePacket(tx)
		world.SetSolid(bx, int32(by), bz, true)
		bp.ScaffoldHistory = append(bp.ScaffoldHistory, scaffPos)

		time.Sleep(250 * time.Millisecond)
		botPos = bp.bot.GetCoords()
		by = int(math.Floor(float64(botPos.Y())))
	}

	return true
}

// DescendTo digs down vertical scaffolding safely to lower height.
func (bp *BlockPlacer) DescendTo(ctx context.Context, targetY int) bool {
	botPos := bp.bot.GetCoords()
	by := int(math.Floor(float64(botPos.Y())))
	bx := int32(math.Floor(float64(botPos.X())))
	bz := int32(math.Floor(float64(botPos.Z())))
	world := bp.bot.GetLocalWorldModel()

	for by > targetY {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		feetPos := protocol.BlockPos{bx, int32(by - 1), bz}
		if world.IsSolid(bx, int32(by-1), bz) {
			bp.digBlock(ctx, feetPos)
			world.SetSolid(bx, int32(by-1), bz, false)
		}

		time.Sleep(300 * time.Millisecond)
		botPos = bp.bot.GetCoords()
		by = int(math.Floor(float64(botPos.Y())))
	}

	return true
}

// CleanupScaffolds removes all placed scaffolding blocks in reverse order of placement.
func (bp *BlockPlacer) CleanupScaffolds(ctx context.Context) {
	if len(bp.ScaffoldHistory) == 0 {
		return
	}

	bp.logger.Info("Starting scaffolding cleanup", "count", len(bp.ScaffoldHistory))
	bp.bot.ReportActionStatus("", event.ActionStatus{Action: "scaffold", Count: len(bp.ScaffoldHistory), Success: true})

	for i := len(bp.ScaffoldHistory) - 1; i >= 0; i-- {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pos := bp.ScaffoldHistory[i]
		botPos := bp.bot.GetCoords()

		dx := float32(pos.X()) + 0.5 - botPos.X()
		dy := float32(pos.Y()) + 0.5 - botPos.Y()
		dz := float32(pos.Z()) + 0.5 - botPos.Z()
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

		if dist > 4.5 {
			bp.bot.NavigateToBlock(pos.X(), pos.Y(), pos.Z(), 3.0)
			time.Sleep(300 * time.Millisecond)
		}

		bp.digBlock(ctx, pos)
		bp.bot.GetLocalWorldModel().SetSolid(pos.X(), pos.Y(), pos.Z(), false)
		time.Sleep(200 * time.Millisecond)
	}

	bp.ScaffoldHistory = nil
	bp.logger.Info("Scaffold cleanup complete")
}
