// Package gathering implements resource collection behaviors such as wood,
// mining, and loot pickup.
package gathering

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"bedrock-ai/internal/bot/movement/animation"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// chopTree fells the trunk at basePos and returns how many logs actually made
// it into the inventory.
//
// Reporting is deliberately not done here: the caller may fell several trunks to
// reach one large target, and one status per trunk would have the LLM announce
// a finished haul three times over.
func (tc *TreeChopper) chopTree(ctx context.Context, basePos protocol.BlockPos, targetCount int) int {
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

	// Verify the broken logs actually enter the inventory before counting them.
	// Previously the bot could break a log, fail to pick it up, and still
	// announce that gathering was done.
	before := tc.rg.looter.currentItemCount("log")
	tc.rg.looter.CollectAllDrops(ctx, 8.0)
	got := tc.rg.looter.currentItemCount("log") - before
	if got <= 0 {
		tc.logger.Warn("Wood broken but not picked up", "target", targetCount)
		return 0
	}
	tc.logger.Info("Wood collected", "count", got, "target", targetCount)
	// Release the last pinned LookAt (topmost log) so the head returns to
	// neutral instead of staying stuck looking up the trunk.
	if rl, ok := tc.rg.bot.(interface{ ResetLook() }); ok {
		rl.ResetLook()
	}
	return got
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
		// Stocking the scaffold mine is a detour: the bot may now be standing
		// several blocks from the trunk it came for. Get back within reach
		// before swinging, otherwise the server just rejects the break and the
		// logs stay put.
		tc.returnToReach(ctx, pos)
	}

	tc.clearObstructions(ctx, pos)

	targetCenter := mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5}
	bot.LookAt(targetCenter)
	time.Sleep(60 * time.Millisecond)

	tc.logger.Debug("Chopping log block", "pos", pos)
	tc.startBreakBlock(pos)
	tc.swingUntilBreak(ctx, targetCenter, sabdBreakDuration(serverAuthBreaking(bot), "oak_log", tc.equippedAxeName()))
	tc.finishBreakBlock(pos)

	bot.GetLocalWorldModel().SetSolid(pos.X(), pos.Y(), pos.Z(), false)
}

// chopReachLimit is how far the bot may stand from a log and still break it.
const chopReachLimit = 4.0

// returnToReach walks back to a log after an interruption (towering, mining
// scaffold stock) left the bot out of breaking range.
func (tc *TreeChopper) returnToReach(ctx context.Context, pos protocol.BlockPos) {
	bot := tc.rg.bot
	botPos := bot.GetCoords()
	dx := botPos.X() - (float32(pos.X()) + 0.5)
	dz := botPos.Z() - (float32(pos.Z()) + 0.5)
	if float32(math.Sqrt(float64(dx*dx+dz*dz))) <= chopReachLimit {
		return
	}

	tc.logger.Debug("Out of reach after towering, walking back to log", "pos", pos)
	if tc.rg.bot.NavigateToBlock(pos.X(), pos.Y()-1, pos.Z(), 2.0) {
		tc.rg.bot.StopMovement()
	}
}

func (tc *TreeChopper) startBreakBlock(pos protocol.BlockPos) {
	_ = tc.rg.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: tc.rg.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   pos,
		BlockFace:       1,
	})
}

// Swing rhythm. A fixed 100 ms tick between swings is the single most obvious
// tell that a bot is working: a human raises the tool, accelerates into a burst
// of a few swings, recovers, and repeats — and their aim drifts a little inside
// the block instead of being welded to its centre.
const (
	chopWindUpMin   = 60 * time.Millisecond
	chopWindUpMax   = 140 * time.Millisecond
	chopSwingMin    = 70 * time.Millisecond
	chopSwingMax    = 110 * time.Millisecond
	chopRecoveryMin = 140 * time.Millisecond
	chopRecoveryMax = 220 * time.Millisecond
	chopBurstLength = 3
	chopAimJitter   = 0.12
)

func chopWindUp() time.Duration {
	return chopWindUpMin + time.Duration(rand.Int63n(int64(chopWindUpMax-chopWindUpMin)))
}

// chopCadence returns the pause after the nth swing of a burst: quick inside a
// burst, a longer recovery between them.
func chopCadence(swing int) time.Duration {
	if swing%chopBurstLength == chopBurstLength-1 {
		return chopRecoveryMin + time.Duration(rand.Int63n(int64(chopRecoveryMax-chopRecoveryMin)))
	}
	return chopSwingMin + time.Duration(rand.Int63n(int64(chopSwingMax-chopSwingMin)))
}

// chopAim jitters the aim point slightly around the block centre so the head
// does not sit perfectly still on one pixel.
func chopAim(center mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{
		center.X() + chopAimJitter*(rand.Float32()*2-1),
		center.Y() + chopAimJitter*(rand.Float32()*2-1),
		center.Z() + chopAimJitter*(rand.Float32()*2-1),
	}
}

func (tc *TreeChopper) swingUntilBreak(ctx context.Context, targetCenter mgl32.Vec3, breakTime time.Duration) {
	bot := tc.rg.bot
	elapsed := time.Duration(0)

	// Wind-up before the first swing: starting instantly looks automated.
	if !sleepContext(ctx, chopWindUp()) {
		return
	}
	elapsed += chopWindUpMin // conservative: never overrun the break time

	for swing := 0; elapsed < breakTime; swing++ {
		_ = bot.WritePacket(animation.MineSwing(bot.GetEntityRuntimeID()))
		bot.LookAt(chopAim(targetCenter))

		wait := chopCadence(swing)
		if elapsed+wait > breakTime {
			wait = breakTime - elapsed
		}
		if !sleepContext(ctx, wait) {
			return
		}
		elapsed += wait
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

	_ = bot.WritePacket(animation.MineSwing(bot.GetEntityRuntimeID()))
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
