// Package gathering implements resource collection behaviors such as wood,
// mining, and loot pickup.
package gathering

import (
	"context"
	"fmt"
	"strings"
	"time"

	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (tc *TreeChopper) chopTree(ctx context.Context, basePos protocol.BlockPos, targetCount int) {
	if targetCount <= 0 {
		targetCount = 1
	}

	logBlocks := tc.collectLogBlocks(basePos, targetCount)
	tc.logger.Debug("Collected log blocks via BFS", "count", len(logBlocks))

	logBlocks = sortLogBlocks(logBlocks)
	tc.equipBestAxe()
	tc.chopLogBlocks(ctx, logBlocks)

	tc.rg.scaffold.DescendFromTower(ctx, float32(basePos.Y()))

	// Give the server a moment to spawn the dropped item entities before we
	// start sweeping — otherwise CollectAllDrops runs before the drops are
	// tracked and finds nothing.
	time.Sleep(400 * time.Millisecond)

	// Verify the broken logs actually enter the inventory before reporting
	// success. Previously the bot could break a log, fail to pick it up, and
	// still announce that gathering was done.
	before := tc.rg.looter.currentItemCount("log")
	tc.rg.looter.CollectAllDrops(ctx, 8.0)
	got := tc.rg.looter.currentItemCount("log") - before
	if got <= 0 {
		tc.logger.Warn("Wood broken but not picked up", "target", targetCount)
		tc.rg.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "chop",
			Item:    "log",
			Success: false,
			Error:   "kayu sudah dihancurkan tapi belum terambil",
		})
		return
	}
	tc.logger.Info("Wood collected", "count", got, "target", targetCount)
	tc.rg.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "chop",
		Item:    "log",
		Count:   got,
		Success: true,
	})
	// Release the last pinned LookAt (topmost log) so the head returns to
	// neutral instead of staying stuck looking up the trunk.
	if rl, ok := tc.rg.bot.(interface{ ResetLook() }); ok {
		rl.ResetLook()
	}
}

func (tc *TreeChopper) collectLogBlocks(basePos protocol.BlockPos, targetCount int) []protocol.BlockPos {
	bot := tc.rg.bot
	queue := []protocol.BlockPos{basePos}
	visited := map[string]bool{fmt.Sprintf("%d,%d,%d", basePos.X(), basePos.Y(), basePos.Z()): true}
	logBlocks := make([]protocol.BlockPos, 0, targetCount)

	for len(queue) > 0 && len(logBlocks) < targetCount {
		curr := queue[0]
		queue = queue[1:]

		name, ok := bot.GetBlockName(curr.X(), curr.Y(), curr.Z())
		if !ok || !isLogBlockName(name) {
			continue
		}
		logBlocks = append(logBlocks, curr)
		queue = tc.appendTreeNeighbors(queue, visited, basePos, curr)
	}

	return logBlocks
}

func (tc *TreeChopper) appendTreeNeighbors(queue []protocol.BlockPos, visited map[string]bool, basePos, curr protocol.BlockPos) []protocol.BlockPos {
	bot := tc.rg.bot

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
				if !withinTreeRange(basePos, next) {
					continue
				}
				name, ok := bot.GetBlockName(next.X(), next.Y(), next.Z())
				if ok && isLogBlockName(name) {
					visited[key] = true
					queue = append(queue, next)
				}
			}
		}
	}

	return queue
}

func withinTreeRange(basePos, next protocol.BlockPos) bool {
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
	return distH <= 4 && distV <= 30 && distV >= -1
}

func sortLogBlocks(logs []protocol.BlockPos) []protocol.BlockPos {
	for i := 0; i < len(logs); i++ {
		for j := i + 1; j < len(logs); j++ {
			if logs[j].Y() < logs[i].Y() {
				logs[i], logs[j] = logs[j], logs[i]
			}
		}
	}
	return logs
}

func (tc *TreeChopper) chopLogBlocks(ctx context.Context, logBlocks []protocol.BlockPos) {
	for _, pos := range logBlocks {
		select {
		case <-ctx.Done():
			return
		default:
		}
		tc.chopLogBlock(ctx, pos)
		time.Sleep(20 * time.Millisecond)
	}
}

func (tc *TreeChopper) chopLogBlock(ctx context.Context, pos protocol.BlockPos) {
	bot := tc.rg.bot

	botPos := bot.GetCoords()
	if float32(pos.Y())-botPos.Y() > 4.0 {
		tc.rg.scaffold.TowerUpTo(ctx, float32(pos.Y())-1.0)
	}

	tc.clearObstructions(ctx, pos)

	targetCenter := mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5}
	bot.LookAt(targetCenter)
	time.Sleep(60 * time.Millisecond)

	tc.logger.Debug("Chopping log block", "pos", pos)
	tc.startBreakBlock(pos)
	tc.swingUntilBreak(pos, targetCenter, blockBreakDuration("oak_log", tc.equippedAxeName()))
	tc.finishBreakBlock(pos)

	bot.GetLocalWorldModel().SetSolid(pos.X(), pos.Y(), pos.Z(), false)
}

func (tc *TreeChopper) startBreakBlock(pos protocol.BlockPos) {
	_ = tc.rg.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: tc.rg.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   pos,
		BlockFace:       1,
	})
}

func (tc *TreeChopper) swingUntilBreak(pos protocol.BlockPos, targetCenter mgl32.Vec3, breakTime time.Duration) {
	bot := tc.rg.bot
	elapsed := time.Duration(0)
	for elapsed < breakTime {
		_ = bot.WritePacket(&packet.Animate{
			ActionType:      packet.AnimateActionSwingArm,
			EntityRuntimeID: bot.GetEntityRuntimeID(),
		})
		bot.LookAt(targetCenter)
		time.Sleep(100 * time.Millisecond)
		elapsed += 100 * time.Millisecond
	}
}

func (tc *TreeChopper) finishBreakBlock(pos protocol.BlockPos) {
	bot := tc.rg.bot
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionCrackBreak,
		BlockPosition:   pos,
		BlockFace:       1,
	})
	// StopBreak MUST be the last packet in the sequence. Sending
	// PredictDestroyBlock here leaves the server in a half-broken state and
	// any block the player later places at this position gets insta-broken.
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStopBreak,
		BlockPosition:   pos,
		BlockFace:       1,
	})
}

func (tc *TreeChopper) clearObstructions(ctx context.Context, targetPos protocol.BlockPos) {
	bot := tc.rg.bot
	world := bot.GetLocalWorldModel()

	checkPos := protocol.BlockPos{targetPos.X(), targetPos.Y() + 1, targetPos.Z()}
	if !world.IsSolid(checkPos.X(), checkPos.Y(), checkPos.Z()) {
		return
	}

	name, ok := bot.GetBlockName(checkPos.X(), checkPos.Y(), checkPos.Z())
	if ok && isLogBlockName(name) {
		return
	}

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
	tc.finishBreakBlock(checkPos)

	world.SetSolid(checkPos.X(), checkPos.Y(), checkPos.Z(), false)
	time.Sleep(100 * time.Millisecond)

	tc.equipBestAxe()
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
