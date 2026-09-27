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
	// Two passes. A trunk's far side is occluded by the near logs while they
	// still stand, so a log skipped for line of sight in the first pass often
	// becomes visible once the lower logs are gone. Never break blind to work
	// around it — that is the through-the-trunk mining a player would never do.
	remaining := logBlocks
	for pass := 0; pass < 2 && len(remaining) > 0; pass++ {
		var deferred []protocol.BlockPos
		for _, pos := range remaining {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if !tc.chopLogBlock(ctx, pos) {
				deferred = append(deferred, pos)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if len(deferred) > 0 {
			tc.logger.Debug("logs deferred after pass", "count", len(deferred), "pass", pass+1)
		}
		remaining = deferred
	}
}

// breakReach is how far the eye may be from a log centre for the log to still
// be breakable. Measured from the eye (feet + 1.62) in 3D, because the old
// feet-dy>4 trigger sent the bot towering — and mining dirt for the tower —
// for upper logs that were comfortably within reach all along.
const breakReach = 4.2

// withinBreakReach reports whether the eye is close enough to the block centre
// to break it.
func withinBreakReach(botPos mgl32.Vec3, pos protocol.BlockPos) bool {
	eye := botPos.Add(mgl32.Vec3{0, mineEyeHeight, 0})
	center := mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5}
	return eye.Sub(center).Len() <= breakReach
}

func (tc *TreeChopper) chopLogBlock(ctx context.Context, pos protocol.BlockPos) bool {
	bot := tc.rg.bot
	botPos := bot.GetCoords()

	if !withinBreakReach(botPos, pos) {
		tc.repositionForLog(ctx, pos)
		botPos = bot.GetCoords()
	}

	// Plan the break the way the miner does: an exposed face the bot can
	// actually see, with the face matching the aim. The old code claimed face 1
	// (top) for every log while aiming at a side, and never checked sight —
	// so it mined through the trunk with no line of sight.
	world := botMineWorld{bot: bot, model: bot.GetLocalWorldModel()}
	step, visible := planMineStep(world, botPos, pos)
	if !visible {
		tc.logger.Debug("log not visible from current spot, deferring", "pos", pos)
		return false
	}

	tc.clearObstructions(ctx, step)

	tc.logger.Debug("Chopping log block", "pos", pos, "face", step.Face)
	bot.LookAt(step.Aim)
	if !sleepContext(ctx, 60*time.Millisecond) {
		return false
	}

	tc.startBreakBlock(step)
	tc.swingUntilBreak(ctx, step.Aim, sabdBreakDuration(serverAuthBreaking(bot), "oak_log", tc.equippedAxeName()))
	tc.finishBreakBlock(step)

	bot.GetLocalWorldModel().SetSolid(pos.X(), pos.Y(), pos.Z(), false)
	return true
}

// repositionForLog gets the bot back within breaking reach of a log. Tall logs
// get a minimal tower — just high enough that the eye reaches the log centre —
// instead of the old climb-to-one-below-the-log that towered far more than the
// trunk needed; far logs get a walk back.
func (tc *TreeChopper) repositionForLog(ctx context.Context, pos protocol.BlockPos) {
	bot := tc.rg.bot
	botPos := bot.GetCoords()
	eyeY := botPos.Y() + mineEyeHeight
	centerY := float32(pos.Y()) + 0.5

	if centerY-eyeY > breakReach-2.0 {
		// The log is too high to reach from here. Tower just enough that the
		// eye lands within reach of the centre (leaving a little horizontal
		// budget), so upper trunk logs stay reachable without re-towering
		// every single block.
		targetFeet := centerY - (breakReach - 2.0) - mineEyeHeight
		tc.logger.Debug("towering up to log", "log_y", pos.Y(), "tower_to_y", targetFeet)
		tc.rg.scaffold.TowerUpTo(ctx, targetFeet)
	} else {
		tc.returnToReach(ctx, pos)
		return
	}
	// After towering the bot may be several blocks out horizontally; walk
	// back before swinging or the break is rejected for range.
	tc.returnToReach(ctx, pos)
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

// startBreakBlock begins the server-auth break of the planned step, using the
// face the plan chose (the old code always claimed face 1/top regardless of
// which side the bot was actually aiming at).
func (tc *TreeChopper) startBreakBlock(step mineStep) {
	_ = tc.rg.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: tc.rg.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})
}

// Swing rhythm. Two tells made the old swing look automated:
//
//  1. Pace faster than the animation. Every Animate packet makes the viewer's
//     client replay the full arm-swing cycle (~300 ms). Swinging again every
//     70-110 ms restarts that cycle before it finishes, so viewers saw the arm
//     vibrate instead of swinging. A human swinging an tool lands around
//     2.5-4 swings per second — 260-400 ms.
//  2. A metronome. The pauses must vary so the beat is organic: quick inside a
//     burst, a longer recovery between bursts, and the aim drifts a little
//     inside the block instead of being welded to its centre.
const (
	chopWindUpMin   = 100 * time.Millisecond
	chopWindUpMax   = 220 * time.Millisecond
	chopSwingMin    = 260 * time.Millisecond
	chopSwingMax    = 400 * time.Millisecond
	chopRecoveryMin = 460 * time.Millisecond
	chopRecoveryMax = 760 * time.Millisecond
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

func (tc *TreeChopper) finishBreakBlock(step mineStep) {
	bot := tc.rg.bot
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionCrackBreak,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})
	// StopBreak MUST be the last packet in the sequence. Sending
	// PredictDestroyBlock here leaves the server in a half-broken state and
	// any block the player later places at this position gets insta-broken.
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStopBreak,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})
}

// clearObstructions removes a non-log block sitting on top of the log being
// chopped (moss, scaffolding, leaves the tower left behind). The obstruction
// is planned through the same visibility check as the log itself.
func (tc *TreeChopper) clearObstructions(ctx context.Context, step mineStep) {
	bot := tc.rg.bot
	world := bot.GetLocalWorldModel()

	checkPos := protocol.BlockPos{step.Position.X(), step.Position.Y() + 1, step.Position.Z()}
	if !world.IsSolid(checkPos.X(), checkPos.Y(), checkPos.Z()) {
		return
	}

	name, ok := bot.GetBlockName(checkPos.X(), checkPos.Y(), checkPos.Z())
	if ok && isLogBlockName(name) {
		return
	}

	// Same sight-line discipline as the log itself: pick an exposed face the
	// bot can see. If the obstruction is not visible from here, the tower will
	// pass through it on the way up anyway — do not mine blind.
	obstructionStep, visible := planMineStep(botMineWorld{bot: bot, model: world}, bot.GetCoords(), checkPos)
	if !visible {
		return
	}

	_ = bot.UnequipItem()
	time.Sleep(50 * time.Millisecond)

	bot.LookAt(obstructionStep.Aim)
	if !sleepContext(ctx, 50*time.Millisecond) {
		return
	}

	_ = bot.WritePacket(animation.MineSwing(bot.GetEntityRuntimeID()))
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   checkPos,
		BlockFace:       obstructionStep.Face,
	})

	if !sleepContext(ctx, sabdBreakDuration(serverAuthBreaking(bot), name, "")) {
		return
	}
	tc.finishBreakBlock(obstructionStep)

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
