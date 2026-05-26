package gathering

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (tc *TreeChopper) chopTree(ctx context.Context, basePos protocol.BlockPos) {
	bot := tc.rg.bot
	world := bot.GetLocalWorldModel()

	var queue []protocol.BlockPos
	queue = append(queue, basePos)

	visited := make(map[string]bool)
	visited[fmt.Sprintf("%d,%d,%d", basePos.X(), basePos.Y(), basePos.Z())] = true

	var logBlocks []protocol.BlockPos

	for len(queue) > 0 && len(logBlocks) < 80 {
		curr := queue[0]
		queue = queue[1:]

		logBlocks = append(logBlocks, curr)

		for dx := int32(-1); dx <= 1; dx++ {
			for dy := int32(-1); dy <= 2; dy++ {
				for dz := int32(-1); dz <= 1; dz++ {
					if dx == 0 && dy == 0 && dz == 0 {
						continue
					}
					next := protocol.BlockPos{curr.X() + dx, curr.Y() + dy, curr.Z() + dz}
					key := fmt.Sprintf("%d,%d,%d", next.X(), next.Y(), next.Z())
					if visited[key] {
						continue
					}

					dx := next.X() - basePos.X()
					if dx < 0 {
						dx = -dx
					}
					dz := next.Z() - basePos.Z()
					if dz < 0 {
						dz = -dz
					}
					distH := max(dx, dz)
					distV := next.Y() - basePos.Y()

					if distH > 4 || distV > 30 || distV < -1 {
						continue
					}

					if world.IsSolid(next.X(), next.Y(), next.Z()) {
						visited[key] = true
						queue = append(queue, next)
					}
				}
			}
		}
	}

	tc.logger.Info("Collected log blocks via BFS", "count", len(logBlocks))

	tc.equipBestAxe()

	for _, pos := range logBlocks {
		select {
		case <-ctx.Done():
			return
		default:
		}

		botPos := bot.GetCoords()
		dy := float32(pos.Y()) - botPos.Y()

		if dy > 4.0 {
			tc.rg.scaffold.TowerUpTo(ctx, float32(pos.Y())-1.0)
		}

		tc.clearObstructions(ctx, pos)

		targetCenter := mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5}
		bot.LookAt(targetCenter)
		time.Sleep(50 * time.Millisecond)

		tc.logger.Info("Chopping log block", "pos", pos)
		
		_ = bot.WritePacket(&packet.Animate{
			ActionType:      packet.AnimateActionSwingArm,
			EntityRuntimeID: bot.GetEntityRuntimeID(),
		})

		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStartBreak,
			BlockPosition:   pos,
			BlockFace:       1,
		})

		time.Sleep(1200 * time.Millisecond)

		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionCrackBreak,
			BlockPosition:   pos,
			BlockFace:       1,
		})
		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionPredictDestroyBlock,
			BlockPosition:   pos,
			BlockFace:       1,
		})

		world.SetSolid(pos.X(), pos.Y(), pos.Z(), false)

		time.Sleep(100 * time.Millisecond)
	}

	tc.rg.scaffold.DescendFromTower(ctx, float32(basePos.Y()))
	tc.rg.looter.CollectAllDrops(ctx, 8.0)
}

func (tc *TreeChopper) clearObstructions(ctx context.Context, targetPos protocol.BlockPos) {
	bot := tc.rg.bot
	world := bot.GetLocalWorldModel()

	checkPos := protocol.BlockPos{targetPos.X(), targetPos.Y() + 1, targetPos.Z()}
	if world.IsSolid(checkPos.X(), checkPos.Y(), checkPos.Z()) {
		_ = bot.UnequipItem()
		time.Sleep(50 * time.Millisecond)

		bot.LookAt(mgl32.Vec3{float32(checkPos.X()) + 0.5, float32(checkPos.Y()) + 0.5, float32(checkPos.Z()) + 0.5})
		
		_ = bot.WritePacket(&packet.Animate{
			ActionType:      packet.AnimateActionSwingArm,
			EntityRuntimeID: bot.GetEntityRuntimeID(),
		})
		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStartBreak,
			BlockPosition:   checkPos,
			BlockFace:       1,
		})

		time.Sleep(300 * time.Millisecond)

		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionCrackBreak,
			BlockPosition:   checkPos,
			BlockFace:       1,
		})
		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionPredictDestroyBlock,
			BlockPosition:   checkPos,
			BlockFace:       1,
		})

		world.SetSolid(checkPos.X(), checkPos.Y(), checkPos.Z(), false)
		time.Sleep(100 * time.Millisecond)

		tc.equipBestAxe()
	}
}

func (tc *TreeChopper) equipBestAxe() {
	bot := tc.rg.bot
	inv := bot.GetInventorySlots()
	names := bot.GetItemNames()

	axes := []string{"netherite_axe", "diamond_axe", "iron_axe", "stone_axe", "wooden_axe", "golden_axe"}
	for _, axeName := range axes {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := names[item.NetworkID]
			if strings.Contains(strings.ToLower(name), axeName) {
				_ = bot.EquipItem(slot)
				return
			}
		}
	}
}
