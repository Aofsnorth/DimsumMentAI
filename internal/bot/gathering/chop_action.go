// Package gathering implements resource collection behaviors such as wood,
// mining, and loot pickup.
package gathering

import (
	"context"
	"fmt"
	"math"
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
	//
	// One rhythm for the whole trunk, not one per log. A tree is a continuous
	// swing, and re-running the wind-up in front of every log put a 120-240ms
	// gap at each log boundary — under the 260ms swing floor — so the arm visibly
	// restarted mid-cycle over and over. The 20ms between logs is gone with it;
	// the cadence now supplies that pause, and it supplies a real one.
	rhythm := animation.NewChain(logAim(logBlocks, 0))

	remaining := logBlocks
	for pass := 0; pass < 2 && len(remaining) > 0; pass++ {
		var deferred []protocol.BlockPos
		for i, pos := range remaining {
			select {
			case <-ctx.Done():
				return
			default:
			}
			// A deferred log was never swung at, so the chain still points at the
			// block it was last aimed at. A log that was swung at re-aims it.
			if i > 0 {
				rhythm.Reaim(blockCentre(pos))
			}
			if !tc.chopLogBlock(ctx, pos, rhythm) {
				deferred = append(deferred, pos)
			}
		}
		if len(deferred) > 0 {
			tc.logger.Debug("logs deferred after pass", "count", len(deferred), "pass", pass+1)
		}
		remaining = deferred
	}
}

// logAim is the centre of a log block, used to seed a chain's aim.
func logAim(logs []protocol.BlockPos, i int) mgl32.Vec3 {
	if i >= len(logs) {
		i = len(logs) - 1
	}
	if i < 0 {
		return mgl32.Vec3{}
	}
	return blockCentre(logs[i])
}

// blockCentre is the middle of a block, which is what the head aims at.
func blockCentre(pos protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5}
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

func (tc *TreeChopper) chopLogBlock(ctx context.Context, pos protocol.BlockPos, rhythm *animation.Chain) bool {
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
	step, visible := PlanMineStep(world, botPos, pos)
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
	tc.swingUntilBreak(ctx, rhythm, sabdBreakDuration(serverAuthBreaking(bot), "oak_log", tc.equippedAxeName()))
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
func (tc *TreeChopper) startBreakBlock(step MineStep) {
	_ = tc.rg.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: tc.rg.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})
}

// Swing rhythm lives in the animation package, shared with the miner, the
// scaffold and the obstacle unstick: see animation/rhythm.go for why the pacing
// and the variation are what they are. The chopper was the first caller and
// still is the reference for how a break should look.
// ChopWindUp is the pause before the first swing of a break.
func ChopWindUp() time.Duration {
	return animation.WindUp()
}

// ChopCadence is the wait before swing number `swing` of a break.
func ChopCadence(swing int) time.Duration {
	return animation.Cadence(swing)
}

// ChopAim is the block centre jittered so the swing never looks welded to a
// single point.
func ChopAim(center mgl32.Vec3) mgl32.Vec3 {
	return animation.JitteredAim(center)
}

// swingUntilBreak swings until the break time is up, on the chain's rhythm.
//
// The chain is shared by every log of a trunk, so the wind-up is served once and
// the burst/recovery pattern runs continuously across the tree. The swing is sent
// before the look, not after: the arm has to be on its way when the head turns,
// and a head that starts moving only once the arm has landed reads as the swing
// being fired at the wrong moment.
func (tc *TreeChopper) swingUntilBreak(ctx context.Context, rhythm *animation.Chain, breakTime time.Duration) {
	bot := tc.rg.bot
	deadline := time.Now().Add(breakTime)

	for {
		wait, swing := rhythm.Next()
		if !sleepContext(ctx, wait) {
			return
		}
		if swing {
			_ = bot.WritePacket(animation.MineSwing(bot.GetEntityRuntimeID()))
			bot.LookAt(rhythm.Aim())
		}
		// The last swing is stretched to land on the break time, the way Beats
		// does, so the arm is never left mid-cycle when the block goes.
		if remaining := time.Until(deadline); remaining > 0 && remaining < animation.SwingMin {
			if !sleepContext(ctx, remaining) {
				return
			}
		}
		if !time.Now().Before(deadline) {
			return
		}
	}
}

func (tc *TreeChopper) finishBreakBlock(step MineStep) {
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
func (tc *TreeChopper) clearObstructions(ctx context.Context, step MineStep) {
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
	obstructionStep, visible := PlanMineStep(botMineWorld{bot: bot, model: world}, bot.GetCoords(), checkPos)
	if !visible {
		return
	}

	_ = bot.UnequipItem()
	time.Sleep(50 * time.Millisecond)

	bot.LookAt(obstructionStep.Aim)
	if !sleepContext(ctx, 50*time.Millisecond) {
		return
	}

	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   checkPos,
		BlockFace:       obstructionStep.Face,
	})

	// The obstruction is broken the same way the log is, rhythm and all. The
	// old code threw one swing and then stood still for the whole break, which
	// is the one pose that reads as a bot: a frozen arm over a block that
	// takes three seconds to fall.
	//
	// It gets its own chain rather than the trunk's: the obstruction is a
	// different block with a different break time, and a log that was already
	// mid-burst should not have its burst carried over onto it. What it must not
	// do is re-wind-up in the middle of a trunk chop, so the chain is created
	// here and discarded with the obstruction.
	obstruction := animation.NewChain(obstructionStep.Aim)
	tc.swingUntilBreak(ctx, obstruction, sabdBreakDuration(serverAuthBreaking(bot), name, ""))
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
