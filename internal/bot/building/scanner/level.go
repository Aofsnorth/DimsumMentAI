package scanner

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

// LevelArea clears obstructions and fills holes around the build spot.
func (s *AreaScanner) LevelArea(ctx context.Context, cx, cy, cz, requiredSize int) {
	if s.bot == nil {
		return
	}
	world := s.bot.GetLocalWorldModel()
	clearSize := requiredSize + 2

	var blocksToClear []protocol.BlockPos
	var blocksToFill []protocol.BlockPos

	for x := -clearSize; x <= clearSize; x++ {
		for z := -clearSize; z <= clearSize; z++ {
			tx := int32(cx + x)
			tz := int32(cz + z)

			if !world.IsSolid(tx, int32(cy-1), tz) {
				blocksToFill = append(blocksToFill, protocol.BlockPos{tx, int32(cy - 1), tz})
			}

			for dy := 0; dy <= 4; dy++ {
				ty := int32(cy + dy)
				if world.IsSolid(tx, ty, tz) {
					blocksToClear = append(blocksToClear, protocol.BlockPos{tx, ty, tz})
				}
			}
		}
	}

	if len(blocksToClear) > 5 || len(blocksToFill) > 5 {
		s.logger.Info("Leveling terrain", "to_clear", len(blocksToClear), "to_fill", len(blocksToFill))
		s.bot.ReportActionStatus("", event.ActionStatus{Action: "level", Count: len(blocksToClear) + len(blocksToFill), Success: true})

		for i := 0; i < len(blocksToClear); i++ {
			for j := i + 1; j < len(blocksToClear); j++ {
				if blocksToClear[j].Y() > blocksToClear[i].Y() {
					blocksToClear[i], blocksToClear[j] = blocksToClear[j], blocksToClear[i]
				}
			}
		}

		s.clearBlocksLoop(ctx, blocksToClear)
		s.fillBlocksLoop(ctx, blocksToFill)
	}
}

func (s *AreaScanner) clearBlocksLoop(ctx context.Context, blocksToClear []protocol.BlockPos) {
	world := s.bot.GetLocalWorldModel()
	clearedCount := 0
	for _, b := range blocksToClear {
		select {
		case <-ctx.Done():
			return
		default:
		}

		curCoords := s.bot.GetCoords()
		dx := float32(b.X()) + 0.5 - curCoords.X()
		dy := float32(b.Y()) + 0.5 - curCoords.Y()
		dz := float32(b.Z()) + 0.5 - curCoords.Z()
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

		if dist > 4.5 {
			s.bot.NavigateToBlock(b.X(), b.Y(), b.Z(), 3.0)
			time.Sleep(300 * time.Millisecond)
		}

		targetCenter := mgl32.Vec3{float32(b.X()) + 0.5, float32(b.Y()) + 0.5, float32(b.Z()) + 0.5}
		s.bot.LookAt(targetCenter)
		time.Sleep(100 * time.Millisecond)

		_ = s.bot.WritePacket(&packet.Animate{
			ActionType:      packet.AnimateActionSwingArm,
			EntityRuntimeID: s.bot.GetEntityRuntimeID(),
		})
		_ = s.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: s.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStartBreak,
			BlockPosition:   b,
			BlockFace:       1,
		})
		time.Sleep(500 * time.Millisecond)
		_ = s.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: s.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionCrackBreak,
			BlockPosition:   b,
			BlockFace:       1,
		})
		_ = s.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: s.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionPredictDestroyBlock,
			BlockPosition:   b,
			BlockFace:       1,
		})

		world.SetSolid(b.X(), b.Y(), b.Z(), false)
		clearedCount++
		if clearedCount%10 == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func (s *AreaScanner) fillBlocksLoop(ctx context.Context, blocksToFill []protocol.BlockPos) {
	if len(blocksToFill) == 0 {
		return
	}
	inv := s.bot.GetInventorySlots()
	names := s.bot.GetItemNames()

	fillSlot, found := schematic.FindItemInSlots(inv, names, "dirt")
	if !found {
		fillSlot, found = schematic.FindItemInSlots(inv, names, "cobblestone")
	}

	if found {
		_ = s.bot.EquipItem(fillSlot)
		time.Sleep(100 * time.Millisecond)

		world := s.bot.GetLocalWorldModel()
		for _, pos := range blocksToFill {
			select {
			case <-ctx.Done():
				return
			default:
			}

			curCoords := s.bot.GetCoords()
			dx := float32(pos.X()) + 0.5 - curCoords.X()
			dy := float32(pos.Y()) + 0.5 - curCoords.Y()
			dz := float32(pos.Z()) + 0.5 - curCoords.Z()
			dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

			if dist > 4.5 {
				s.bot.NavigateToBlock(pos.X(), pos.Y(), pos.Z(), 3.0)
				time.Sleep(300 * time.Millisecond)
			}

			targetCenter := mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5}
			s.bot.LookAt(targetCenter)
			time.Sleep(100 * time.Millisecond)

			faces := []struct {
				dir  protocol.BlockPos
				face int32
			}{
				{protocol.BlockPos{0, -1, 0}, 1},
				{protocol.BlockPos{1, 0, 0}, 4},
				{protocol.BlockPos{-1, 0, 0}, 5},
				{protocol.BlockPos{0, 0, 1}, 2},
				{protocol.BlockPos{0, 0, -1}, 3},
			}

			invSlots := s.bot.GetInventorySlots()
			itemStack := invSlots[fillSlot]

			for _, f := range faces {
				adjX := pos.X() + f.dir.X()
				adjY := pos.Y() + f.dir.Y()
				adjZ := pos.Z() + f.dir.Z()

				if world.IsSolid(adjX, adjY, adjZ) {
					tx := &packet.InventoryTransaction{
						TransactionData: &protocol.UseItemTransactionData{
							ActionType:      protocol.UseItemActionClickBlock,
							BlockPosition:   protocol.BlockPos{adjX, adjY, adjZ},
							BlockFace:       f.face,
							HotBarSlot:      int32(s.bot.GetHeldItemSlot()),
							HeldItem:        protocol.ItemInstance{Stack: itemStack},
							Position:        s.bot.GetCoords(),
							ClickedPosition: mgl32.Vec3{0.5, 0.5, 0.5},
						},
					}
					_ = s.bot.WritePacket(tx)
					world.SetSolid(pos.X(), pos.Y(), pos.Z(), true)
					break
				}
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
}
