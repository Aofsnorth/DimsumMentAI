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

	logBlocks := tc.collectLogBlocks(basePos)
	tc.logger.Debug("Collected log blocks via BFS", "count", len(logBlocks))

	logBlocks = sortLogBlocks(logBlocks)
	tc.equipBestAxe()

	// Baseline before the first swing, not after the last one. Drops that landed
	// while chopping are already in the bag by the time the sweep below starts,
	// so a baseline taken there reported a delta of zero for every tree.
	before := tc.rg.looter.currentItemCount("log")

	// Keep going until the trunk is down. One pass defers every log the bot could
	// not reach or see, and the tree used to be abandoned right there with its
	// upper half standing. Each further round works on what is still a log and
	// stops as soon as a round removes nothing, so an unreachable top cannot
	// loop forever.
	for round := 0; round < maxChopRounds && len(logBlocks) > 0; round++ {
		tc.chopLogBlocks(ctx, logBlocks)
		if !sleepContext(ctx, 300*time.Millisecond) {
			break
		}
		left := tc.stillLogs(logBlocks)
		if len(left) == 0 {
			break
		}
		if len(left) >= len(logBlocks) {
			tc.logger.Warn("Tree not finished: remaining logs could not be reached", "left", len(left), "base", basePos)
			// The upper half is out of arm's reach, and there is more than one
			// sensible answer: pillar up with blocks and take them, or leave the
			// top standing and move to the next tree. The model picks; the
			// gatherer only executes. A model that cannot answer costs nothing —
			// the default is to skip.
			if tc.maybeScaffoldToLogs(ctx, left, basePos) {
				tc.chopLogBlocks(ctx, left)
			}
			break
		}
		tc.logger.Info("Tree not finished, working the remaining logs", "left", len(left), "round", round+1)
		logBlocks = left
	}

	tc.rg.scaffold.DescendFromTower(ctx, float32(basePos.Y()))

	// Give the server a moment to spawn the dropped item entities before we
	// start sweeping — otherwise CollectAllDrops runs before the drops are
	// tracked and finds nothing.
	time.Sleep(400 * time.Millisecond)

	// Verify the broken logs actually enter the inventory before counting them.
	// Previously the bot could break a log, fail to pick it up, and still
	// announce that gathering was done.
	//
	// One sweep raced three separate delays: the drops falling from the top of
	// the trunk, their pickup cooldown, and the item-entity packet arriving.
	// On a live server all three lost the race and a felled tree reported zero
	// logs — so the sweep is retried until something lands in the inventory or
	// the tries run out.
	got := 0
	for try := 0; try < 4 && ctx.Err() == nil; try++ {
		tc.rg.looter.CollectAllDrops(ctx, 8.0)
		if !sleepContext(ctx, 700*time.Millisecond) {
			break
		}
		got = tc.rg.looter.currentItemCount("log") - before
		if got > 0 {
			break
		}
	}
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

// maxChopRounds bounds how many times one trunk is re-worked.
const maxChopRounds = 4

// gatherDecider is the optional view of the bot that can ask the model to
// choose between two gather strategies. It is an assertion rather than a
// method on the gathering Bot interface so the test doubles stay untouched;
// a bot without it always skips, which is the behaviour before this existed.
type gatherDecider interface {
	AskGatherDecision(situation string) string
}

// maybeScaffoldToLogs handles the "the good wood is up there" case. It asks
// the model whether to pillar up to the unreachable logs or leave them; on
// "scaffold" it builds up to just below the lowest unreachable log so the
// next chop rounds can reach it. Reports whether the body is now up at the
// logs (the caller re-chops; DescendFromTower brings it back down after).
func (tc *TreeChopper) maybeScaffoldToLogs(ctx context.Context, left []protocol.BlockPos, basePos protocol.BlockPos) bool {
	if len(left) == 0 {
		return false
	}
	lowest := left[0]
	for _, pos := range left {
		if pos.Y() < lowest.Y() {
			lowest = pos
		}
	}
	// Logs up to 6 blocks high (basePos.Y() through basePos.Y()+5) are reachable
	// from the ground by stepping into or right next to the cleared trunk base;
	// never build a scaffold pillar for a normal-height tree.
	if lowest.Y()-basePos.Y() <= 5 {
		if tc.rg.bot.NavigateToBlock(basePos.X(), basePos.Y(), basePos.Z(), 1.4) {
			tc.haltWalk()
		}
		return withinBreakReach(tc.rg.bot.GetCoords(), lowest)
	}
	situation := fmt.Sprintf(
		"The tree at X:%d Y:%d Z:%d still has %d logs above your reach (lowest unreachable at Y:%d, you stand at Y:%d). "+
			"Answer exactly one word: 'scaffold' to pillar up with blocks and chop them, or 'skip' to leave the top and move to another tree.",
		basePos.X(), basePos.Y(), basePos.Z(), len(left), lowest.Y(), int(tc.rg.bot.GetCoords().Y()))

	decision := "skip"
	if decider, ok := tc.rg.bot.(gatherDecider); ok {
		decision = decider.AskGatherDecision(situation)
	}
	tc.logger.Info("Gather decision for unreachable upper logs",
		"decision", decision, "logs", len(left), "lowest_y", lowest.Y(), "base", basePos)
	if decision != "scaffold" {
		return false
	}
	if _, _, ok := tc.rg.scaffold.FindScaffoldItem(); !ok {
		tc.logger.Info("Scaffold chosen but no blocks to build with; skipping the top")
		return false
	}
	targetFeet := float32(lowest.Y()) - (breakReach - 1.5) - mineEyeHeight
	if targetFeet < float32(basePos.Y())+1 {
		targetFeet = float32(basePos.Y()) + 1
	}
	tc.rg.scaffold.TowerUpTo(ctx, targetFeet)
	return true
}

// stillLogs filters positions down to those the world still reports as logs.
func (tc *TreeChopper) stillLogs(positions []protocol.BlockPos) []protocol.BlockPos {
	var left []protocol.BlockPos
	for _, pos := range positions {
		if name, ok := tc.rg.bot.GetBlockName(pos.X(), pos.Y(), pos.Z()); ok && isLogBlockName(name) {
			left = append(left, pos)
		}
	}
	return left
}

// collectLogBlocks returns every log in the trunk rooted at basePos.
//
// It deliberately does NOT stop once it has found enough logs for the target. It
// used to, and felling a tree then meant cutting off the first N logs of a
// twenty-log trunk and walking away — the bot moved on to the next tree with the
// one it was standing next to still half standing. A player watching that is
// watching a bot that has not finished the job it just announced.
//
// The target belongs to the caller, not here. This function's whole question is
// "what is in this tree", and the answer does not depend on how much wood was
// asked for.
func (tc *TreeChopper) collectLogBlocks(basePos protocol.BlockPos) []protocol.BlockPos {
	bot := tc.rg.bot
	queue := []protocol.BlockPos{basePos}
	visited := map[string]bool{fmt.Sprintf("%d,%d,%d", basePos.X(), basePos.Y(), basePos.Z()): true}
	var logBlocks []protocol.BlockPos

	for len(queue) > 0 {
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
const (
	breakReach          = 4.6
	scaffoldHeightFloor = 4.2
)

// withinBreakReach reports whether the eye is close enough to the log (either
// its centre or its exposed bottom face when overhead) to break it from here.
func withinBreakReach(botPos mgl32.Vec3, pos protocol.BlockPos) bool {
	eye := botPos.Add(mgl32.Vec3{0, mineEyeHeight, 0})
	center := mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5}
	if eye.Sub(center).Len() <= breakReach {
		return true
	}
	bottom := mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()), float32(pos.Z()) + 0.5}
	return eye.Y() < bottom.Y() && eye.Sub(bottom).Len() <= 4.5
}

func (tc *TreeChopper) chopLogBlock(ctx context.Context, pos protocol.BlockPos, rhythm *animation.Chain) bool {
	bot := tc.rg.bot
	botPos := bot.GetCoords()

	if !withinBreakReach(botPos, pos) {
		if !tc.repositionForLog(ctx, pos) {
			return false
		}
		tc.equipBestAxe()
	}

	world := botMineWorld{bot: bot, model: bot.GetLocalWorldModel()}

	// Clearance comes BEFORE the plan, not after it.
	tc.clearObstructions(ctx, pos)

	step, visible := PlanMineStep(world, bot.GetCoords(), pos)
	if !visible {
		// Stepping closer to the cleared trunk column opens the vertical
		// sightline straight up through the broken lower logs without towering.
		tc.returnToReach(ctx, pos)
		tc.clearObstructions(ctx, pos)
		step, visible = PlanMineStep(world, bot.GetCoords(), pos)
	}
	if !visible {
		tc.logger.Debug("log not visible from current spot, deferring", "pos", pos)
		return false
	}

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

// repositionForLog gets the bot back within breaking reach of a log and
// reports whether that log is now reachable. Tall logs get a minimal tower —
// just high enough that the eye reaches the log centre — instead of the old
// climb-to-one-below-the-log that towered far more than the trunk needed; far
// logs get a walk back.
func (tc *TreeChopper) repositionForLog(ctx context.Context, pos protocol.BlockPos) bool {
	bot := tc.rg.bot

	// Always step closer to the trunk base on the ground first. A bot standing
	// 2.5-3.5 blocks to the side can reach logs 4-6 simply by stepping up to
	// the trunk without placing any scaffold blocks.
	tc.returnToReach(ctx, pos)
	botPos := bot.GetCoords()
	if withinBreakReach(botPos, pos) {
		return true
	}

	eyeY := botPos.Y() + mineEyeHeight
	centerY := float32(pos.Y()) + 0.5

	// Only scaffold when the log is genuinely high above eye level (7th log or
	// higher, i.e. >= 6 blocks above feet). Normal 4-6 block trees are never
	// scaffolded.
	if centerY-eyeY <= scaffoldHeightFloor {
		return false
	}

	targetFeet := centerY - (breakReach - 1.5) - mineEyeHeight
	tc.logger.Debug("towering up to high log", "log_y", pos.Y(), "tower_to_y", targetFeet)
	tc.rg.scaffold.TowerUpTo(ctx, targetFeet)
	return withinBreakReach(bot.GetCoords(), pos)
}

// chopReachLimit is the horizontal distance within which the bot is already
// standing right next to/under the trunk column.
const chopReachLimit = 1.6

// returnToReach walks closer to the trunk column at GROUND level (never
// targeting pos.Y()-1 in mid-air, which would cause A* to scaffold upward).
func (tc *TreeChopper) returnToReach(ctx context.Context, pos protocol.BlockPos) {
	bot := tc.rg.bot
	botPos := bot.GetCoords()
	dx := botPos.X() - (float32(pos.X()) + 0.5)
	dz := botPos.Z() - (float32(pos.Z()) + 0.5)
	if float32(math.Sqrt(float64(dx*dx+dz*dz))) <= chopReachLimit {
		return
	}

	groundY := tc.groundLevelBelowLog(pos, int32(math.Floor(float64(botPos.Y()))))
	tc.logger.Debug("Stepping closer to trunk base at ground level", "log_pos", pos, "ground_y", groundY)
	if tc.rg.bot.NavigateToBlock(pos.X(), groundY, pos.Z(), 1.5) {
		tc.haltWalk()
	}
}

func (tc *TreeChopper) groundLevelBelowLog(pos protocol.BlockPos, fallbackY int32) int32 {
	world := tc.rg.bot.GetLocalWorldModel()
	for y := pos.Y() - 1; y >= fallbackY-2 && y >= pos.Y()-10; y-- {
		name, ok := tc.rg.bot.GetBlockName(pos.X(), y, pos.Z())
		if !ok || isLogBlockName(name) {
			continue
		}
		if world != nil && world.IsSolid(pos.X(), y, pos.Z()) {
			return y + 1
		}
	}
	return fallbackY
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

// ChopCadence is the wait between two swings of a break.
func ChopCadence() time.Duration {
	return animation.Cadence()
}

// ChopAim is the block centre jittered so the swing never looks welded to a
// single point.
func ChopAim(center mgl32.Vec3) mgl32.Vec3 {
	return animation.JitteredAim(center)
}

// swingUntilBreak swings until the break time is up, on the chain's rhythm.
//
// The chain is shared by every log of a trunk, so the wind-up is served once and
// the arm works continuously from the first log to the last. The swing is sent
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
		if remaining := time.Until(deadline); remaining > 0 && remaining < animation.SwingFloor {
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

// clearObstructions removes the blocks standing between the bot and a log, and
// reports whether it got through.
//
// The top is cleared on every log: leaves, moss and scaffolding on a trunk hide
// the crack overlay, and the overlay is the entire visible point of a swing.
//
// The four sides are only touched when the log has no open face at all. That is
// the case where the block in the way is not decoration but the difference
// between a tree and a wall, and it is the case that used to have no handler
// worth the name — the log was deferred, retried twice, and abandoned, leaving
// a half-felled tree behind a block the player could see was in the way.
func (tc *TreeChopper) clearObstructions(ctx context.Context, logPos protocol.BlockPos) bool {
	bot := tc.rg.bot
	mineWorld := botMineWorld{bot: bot, model: bot.GetLocalWorldModel()}

	cleared := tc.breakObstruction(ctx, protocol.BlockPos{logPos.X(), logPos.Y() + 1, logPos.Z()})

	// Re-checked each pass rather than once up front: removing one block can open
	// a face, and opening one face is the whole job.
	for _, face := range mineFaces {
		if _, open := PlanMineStep(mineWorld, bot.GetCoords(), logPos); open {
			break
		}
		if face.offset.Y() != 0 {
			continue // the top is already handled, and the bottom is the floor
		}
		pos := protocol.BlockPos{
			logPos.X() + face.offset.X(),
			logPos.Y(),
			logPos.Z() + face.offset.Z(),
		}
		cleared = tc.breakObstruction(ctx, pos) || cleared
		// Also clear low canopy leaves one block below the log that block the
		// upward diagonal sightline from the ground.
		lowPos := protocol.BlockPos{
			logPos.X() + face.offset.X(),
			logPos.Y() - 1,
			logPos.Z() + face.offset.Z(),
		}
		cleared = tc.breakObstruction(ctx, lowPos) || cleared
	}
	return cleared
}

// breakObstruction removes a single non-log block at pos, if it is one the bot
// is willing and able to remove. It reports whether anything was broken.
func (tc *TreeChopper) breakObstruction(ctx context.Context, pos protocol.BlockPos) bool {
	bot := tc.rg.bot
	world := bot.GetLocalWorldModel()

	if !world.IsSolid(pos.X(), pos.Y(), pos.Z()) {
		return false
	}
	name, ok := bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok || isLogBlockName(name) || immovableBlockName(name) {
		return false
	}

	// Same sight-line discipline as the log itself: pick an exposed face the
	// bot can see. If the obstruction is not visible from here, the tower will
	// pass through it on the way up anyway — do not mine blind.
	obstructionStep, visible := PlanMineStep(botMineWorld{bot: bot, model: world}, bot.GetCoords(), pos)
	if !visible {
		return false
	}

	_ = bot.UnequipItem()
	time.Sleep(50 * time.Millisecond)

	bot.LookAt(obstructionStep.Aim)
	if !sleepContext(ctx, 50*time.Millisecond) {
		return false
	}

	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   pos,
		BlockFace:       obstructionStep.Face,
	})

	// The obstruction is broken the same way the log is, rhythm and all. The
	// old code threw one swing and then stood still for the whole break, which
	// is the one pose that reads as a bot: a frozen arm over a block that
	// takes three seconds to fall.
	//
	// It gets its own chain rather than the trunk's: the obstruction is a
	// different block with a different break time, and the trunk's rhythm should
	// not be stretched to fit it. What it must not do is re-wind-up in the middle
	// of a trunk chop, so the chain is created here and discarded with the
	// obstruction.
	obstruction := animation.NewChain(obstructionStep.Aim)
	tc.swingUntilBreak(ctx, obstruction, sabdBreakDuration(serverAuthBreaking(bot), name, ""))
	tc.finishBreakBlock(obstructionStep)

	world.SetSolid(pos.X(), pos.Y(), pos.Z(), false)
	time.Sleep(100 * time.Millisecond)

	tc.equipBestAxe()
	return true
}

// immovableBlockName reports whether a block is one the bot must never break
// while clearing a way, whatever happens to be in its hand.
//
// This is reached far more often than it used to be. Clearing a single block
// above the log was rare and obvious; clearing whatever walls in a log is a
// decision the bot now makes on its own, and a decision it makes on its own can
// be pointed at something a player built.
func immovableBlockName(name string) bool {
	lower := strings.ToLower(name)
	for _, kind := range []string{"bedrock", "barrier", "command_block", "structure_block", "jigsaw", "portal"} {
		if strings.Contains(lower, kind) {
			return true
		}
	}
	return false
}

// IsAxeName reports whether an item name is an axe. The generic tier used a
// bare substring test, and "pickaxe" contains "axe" — a stone pickaxe sat in
// the axe slot and the trunk was chopped with the wrong tool while the bot
// believed it had equipped its best axe. Exported so the matcher rule is
// testable without a live bot.
func IsAxeName(name string) bool {
	n := strings.ToLower(name)
	if strings.Contains(n, "pickaxe") {
		return false
	}
	return strings.Contains(n, "axe") || strings.Contains(n, "kapak")
}

func (tc *TreeChopper) equipBestAxe() {
	bot := tc.rg.bot
	inv := bot.GetInventorySlots()
	names := bot.GetItemNames()

	// Tiered: the exact vanilla tiers first, then any axe-shaped name — custom
	// servers (this one included) rename or add axes, and a matcher keyed to
	// six exact strings declared the gift axe "item custom aneh-aneh" while it
	// sat in the inventory.
	tiers := []func(string) bool{
		func(n string) bool { return strings.Contains(n, "netherite_axe") },
		func(n string) bool { return strings.Contains(n, "diamond_axe") },
		func(n string) bool { return strings.Contains(n, "iron_axe") },
		func(n string) bool { return strings.Contains(n, "stone_axe") },
		func(n string) bool { return strings.Contains(n, "wooden_axe") },
		func(n string) bool { return strings.Contains(n, "golden_axe") },
		// Any axe-shaped name at all, any language — but never a pickaxe.
		IsAxeName,
	}
	bestSlot := ^uint32(0)
	for _, match := range tiers {
		for slot := uint32(0); slot < 64; slot++ {
			item, ok := inv[slot]
			if !ok || item.Count <= 0 {
				continue
			}
			if match(strings.ToLower(names[item.NetworkID])) {
				bestSlot = slot
				break
			}
		}
		if bestSlot != ^uint32(0) {
			break
		}
	}
	if bestSlot == ^uint32(0) {
		// Nothing matched: dump what the inventory DOES hold, so the next run's
		// log says what the server called the axe instead of leaving a silent gap.
		held := make([]string, 0, len(inv))
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			held = append(held, fmt.Sprintf("slot=%d name=%q", slot, names[item.NetworkID]))
		}
		tc.logger.Warn("equipBestAxe: no axe recognised in inventory", "inventory", strings.Join(held, ", "))
		return
	}
	// Re-equipping per log must be a no-op when the axe is already up: the
	// round loop calls this after every detour, and a MobEquipment per call
	// flickers the hand on the server's view.
	if bot.GetHeldItemSlot() == bestSlot {
		return
	}
	_ = bot.EquipItem(bestSlot)
}
