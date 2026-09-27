// Package movement implements tick-level bot movement, path following, and look
// direction. It is responsible for steering, physics, collision resolution, and
// the PlayerAuthInput heartbeat sent to the server.
package movement

import (
	"fmt"
	"log/slog"
	"math"
	"time"

	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func (tc *TickContext) performActiveSteering() {
	if tc.scaffoldingBlocksMovement() {
		return
	}
	tc.updateShouldMoveState()
	if !tc.ShouldMove {
		// Standing still. Take the occasional small step a real player takes
		// while idling — see idle_nudge.go for why this is a survival need on
		// AFK-kicking servers, not just flavour.
		tc.maybeIdleNudge()
		return
	}
	if tc.closeFollowTarget() {
		tc.MoveVec = mgl32.Vec2{}
		return
	}

	tc.HasHorizontalMove = true
	tc.advancePathOrArrive()
	tc.refreshTarget()
	tc.handleStuck()
	tc.resetJumpState()
	tc.updateParkourAndAutoJump()
}

func (tc *TickContext) scaffoldingBlocksMovement() bool {
	tc.B.Mu.Lock()
	scaffActive := tc.B.ScaffoldingActive
	tc.B.Mu.Unlock()

	if scaffActive {
		tc.ShouldMove = false
		tc.HasHorizontalMove = false
		tc.MoveVec = mgl32.Vec2{}
		return true
	}
	return false
}

func (tc *TickContext) updateShouldMoveState() {
	tc.ShouldMove = tc.MState != "idle" && tc.AllowDirectSteering
	tc.PlayerHeightDiff = 0.0
	tc.FollowWalking = false
	if tc.MState == "follow" && tc.TPlayer != "" {
		if _, pPos, ok := tc.B.FindPlayer(tc.TPlayer); ok {
			tc.PlayerHeightDiff = float32(math.Abs(float64(pPos.Y() - tc.CurrPos.Y())))
		}
		tc.updateFollowWalkLatch()
	}
	tc.HasHorizontalMove = false
}

// updateFollowWalkLatch advances the follow walk/stop state with hysteresis:
// start walking at followWalkDist/followWalkHeightDiff, stop only once inside
// followStopDist/followStopHeightDiff. Between the two the previous state is
// kept, so a target hovering at the threshold cannot flap the state.
func (tc *TickContext) updateFollowWalkLatch() {
	tc.B.Mu.Lock()
	walking := tc.B.FollowMoving
	tc.B.Mu.Unlock()

	if tc.DistToPlayer >= followWalkDist || tc.PlayerHeightDiff >= followWalkHeightDiff {
		walking = true
	} else if tc.DistToPlayer < followStopDist && tc.PlayerHeightDiff < followStopHeightDiff {
		walking = false
	}

	tc.B.Mu.Lock()
	tc.B.FollowMoving = walking
	tc.B.Mu.Unlock()
	tc.FollowWalking = walking
}

func (tc *TickContext) closeFollowTarget() bool {
	return tc.MState == "follow" && !tc.FollowWalking
}

func (tc *TickContext) advancePathOrArrive() {
	advanceDist, maxHeightDiff := tc.computeAdvanceParams()
	if tc.HasPath && tc.Dist < advanceDist {
		tc.advancePath(maxHeightDiff)
		return
	}
	if !tc.HasPath && tc.MState == "walk_to" {
		tc.checkWalkToArrival()
	}
}

func (tc *TickContext) computeAdvanceParams() (advanceDist, maxHeightDiff float32) {
	if tc.IsLadderActive {
		return 0.25, 1.2
	}
	advanceDist, segmentIsGap := tc.computePathAdvanceDist()
	maxHeightDiff = tc.maxHeightDiffFor(segmentIsGap)
	return
}

func (tc *TickContext) computePathAdvanceDist() (float32, bool) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()

	advanceDist := float32(0.8)
	segmentIsGap := false
	if tc.B.PathIndex >= len(tc.B.CurrentPath) {
		return advanceDist, segmentIsGap
	}

	currNode := tc.B.CurrentPath[tc.B.PathIndex]
	if tc.B.PathIndex > 0 {
		prevNode := tc.B.CurrentPath[tc.B.PathIndex-1]
		dxGap := math.Abs(float64(currNode.X - prevNode.X))
		dzGap := math.Abs(float64(currNode.Z - prevNode.Z))
		if math.Max(dxGap, dzGap) > 1.5 {
			segmentIsGap = true
			advanceDist = 0.35
		}
	}

	if currNode.Y != int32(math.Floor(float64(tc.CurrPos.Y()+0.1))) {
		advanceDist = 0.5
	} else if tc.B.PathIndex > 0 && tc.B.PathIndex+1 < len(tc.B.CurrentPath) {
		prevNode := tc.B.CurrentPath[tc.B.PathIndex-1]
		nextNode := tc.B.CurrentPath[tc.B.PathIndex+1]

		dx1 := currNode.X - prevNode.X
		dz1 := currNode.Z - prevNode.Z
		dx2 := nextNode.X - currNode.X
		dz2 := nextNode.Z - currNode.Z

		if dx1 != dx2 || dz1 != dz2 {
			advanceDist = 0.3
		}
	}

	return advanceDist, segmentIsGap
}

func (tc *TickContext) maxHeightDiffFor(segmentIsGap bool) float32 {
	if tc.IsLadderActive {
		return 1.2
	}
	if segmentIsGap {
		return 0.55
	}
	return 2.5
}

func (tc *TickContext) advancePath(maxHeightDiff float32) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()

	if tc.B.PathIndex >= len(tc.B.CurrentPath) {
		return
	}

	nextNode := tc.B.CurrentPath[tc.B.PathIndex]
	yDiff := float64(nextNode.Y) - float64(tc.CurrPos.Y())
	heightDiff := math.Abs(yDiff)

	canAdvance := heightDiff < float64(maxHeightDiff)
	if yDiff > 0.5 {
		canAdvance = heightDiff < 1.05 && float64(tc.CurrPos.Y())+0.15 >= float64(nextNode.Y)
	}

	if !canAdvance {
		return
	}

	tc.B.PathIndex++
	tc.B.TicksStuck = 0
	tc.B.LastTickPos = tc.CurrPos
	// Reaching a node is proof of progress, so the no-progress window restarts
	// here instead of carrying a stale baseline into the next segment.
	tc.B.StuckWindowStart = time.Time{}
	if tc.B.PathIndex >= len(tc.B.CurrentPath) {
		tc.B.CurrentPath = nil
		tc.B.PathIndex = 0
		if tc.B.MovementState == "walk_to" {
			tc.B.MovementState = "idle"
			tc.B.Logger.Debug("bot arrived at target destination", slog.Float64("x", float64(tc.TPos.X())), slog.Float64("z", float64(tc.TPos.Z())))
		}
	}
}

func (tc *TickContext) checkWalkToArrival() {
	tol := tc.TargetTolerance
	if tol <= 0 {
		tol = 2.0
	}
	dyTarget := tc.CurrPos.Y() - tc.TPos.Y()
	distXZ := float32(math.Sqrt(float64(tc.Dx*tc.Dx + tc.Dz*tc.Dz)))
	if distXZ < tol && math.Abs(float64(dyTarget)) < float64(tol) {
		tc.B.Mu.Lock()
		tc.B.MovementState = "idle"
		tc.B.Mu.Unlock()
		tc.B.Logger.Debug("bot arrived at target destination", slog.Float64("x", float64(tc.TPos.X())), slog.Float64("y", float64(tc.TPos.Y())), slog.Float64("z", float64(tc.TPos.Z())), slog.Float64("tolerance", float64(tol)))
	}
}

func (tc *TickContext) refreshTarget() {
	tc.B.Mu.Lock()
	tc.HasPath = len(tc.B.CurrentPath) > 0 && tc.B.PathIndex < len(tc.B.CurrentPath)
	if tc.HasPath {
		node := tc.B.CurrentPath[tc.B.PathIndex]
		tc.NextTarget = mgl32.Vec3{float32(node.X) + 0.5, float32(node.Y), float32(node.Z) + 0.5}
	} else {
		tc.NextTarget = tc.TPos
	}
	tc.B.Mu.Unlock()

	tc.Dx = tc.NextTarget.X() - tc.CurrPos.X()
	tc.Dz = tc.NextTarget.Z() - tc.CurrPos.Z()
	tc.Dist = float32(math.Sqrt(float64(tc.Dx*tc.Dx + tc.Dz*tc.Dz)))
}

const (
	// stuckIdleTicks is how many consecutive motionless ticks count as wedged.
	// This only catches a hard freeze, where the movement code cancels the step
	// outright and the position stops changing.
	stuckIdleTicks = 12

	// stuckProgressWindow / stuckProgressMinMove catch the cases the per-tick
	// counter is blind to: the host rubberbanding the bot back to the same spot
	// every few ticks, or the bot sliding sideways along a wall. Both keep
	// changing the position each tick, so TicksStuck stays at zero and no
	// recovery ever runs — yet the bot gains no ground. Real progress inside the
	// window is net displacement, not summed movement, so oscillation and
	// rubberbanding both read as "no progress".
	stuckProgressWindow    = 1500 * time.Millisecond
	stuckProgressMinMove   = 0.4
	stuckPenaltyBaseWindow = 3 * time.Second
	stuckPenaltyMaxWindow  = 12 * time.Second
	stuckPenaltyMaxSteps   = 2

	// parkourRejumpCooldown is how long the bot waits before re-attempting a gap
	// jump for the same path node after a missed first jump. Long enough that a
	// landing that simply has not settled yet does not double-fire; short enough
	// that a failed jump recovers in well under the stuck-escalation window.
	parkourRejumpCooldown = 600 * time.Millisecond

	// walkToRepathInterval / walkToRepathMaxInterval bound how often a walk_to
	// that lost its route re-plans. The lower bound keeps the freeze from being
	// noticeable; the backoff keeps an unsolvable target from re-running A* every
	// tick and starving the movement loop.
	walkToRepathInterval    = 500 * time.Millisecond
	walkToRepathMaxInterval = 4 * time.Second
	// stuckProgressMinTargetDist is how close the destination has to be for the
	// forward axis to stop being meaningful.
	stuckProgressMinTargetDist = 0.5
)

// walkToRepathIntervalFor backs the re-path interval off as consecutive attempts
// fail, so a target A* cannot solve costs less and less often.
func walkToRepathIntervalFor(failures int) time.Duration {
	interval := walkToRepathInterval
	for i := 0; i < failures && interval < walkToRepathMaxInterval; i++ {
		interval *= 2
	}
	if interval > walkToRepathMaxInterval {
		return walkToRepathMaxInterval
	}
	return interval
}

// stuckPenaltyWindow grows the temp-solid penalty for the cells the bot is
// pressing into, so a blocker it failed to walk past stays blocked for longer
// after each failed attempt. The doubling is stepped, not looped per count:
// multiplying a time.Duration by two enough times overflows int64 and wraps to a
// negative window, which would expire the penalty immediately.
func stuckPenaltyWindow(consecutiveStuck int) time.Duration {
	window := stuckPenaltyBaseWindow
	for i := 1; i < consecutiveStuck && i <= stuckPenaltyMaxSteps; i++ {
		window *= 2
	}
	if window > stuckPenaltyMaxWindow {
		return stuckPenaltyMaxWindow
	}
	return window
}

// forwardProgress is how many blocks of real ground the bot covered toward the
// destination between two positions.
//
// Displacement on its own is not a progress signal: a bot sliding sideways along
// a wall travels plenty, and a host that keeps rubberbanding the bot can leave
// it back where it started, yet neither makes headway. Projecting the movement
// onto the horizontal axis pointing at the destination catches both, and unlike
// a distance-to-target comparison it does not misfire when the destination is a
// moving player who is simply running away at the bot's own speed.
func forwardProgress(from, to, target mgl32.Vec3) float32 {
	dx := target.X() - from.X()
	dz := target.Z() - from.Z()
	horiz := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if horiz < stuckProgressMinTargetDist {
		// Standing on top of the destination: there is no forward axis left, and
		// the arrival check owns this case.
		return stuckProgressMinMove
	}
	return (to.X()-from.X())*dx/horiz + (to.Z()-from.Z())*dz/horiz
}

// ensureWalkToHasPath re-plans a walk_to that has no usable route.
//
// Without this, losing the path is terminal. The host wipes CurrentPath whenever
// it corrects our position by more than two blocks (CorrectPlayerMovePrediction),
// and a walk_to target farther than directSteerWalkToRadius cannot be walked
// straight at, so AllowDirectSteering goes false, ShouldMove goes false, and the
// bot stands perfectly still until a new chat command restarts it — with no log
// line at all to explain the silence.
func (tc *TickContext) ensureWalkToHasPath() {
	if tc.MState != "walk_to" || tc.HasPath || tc.B.WorldModel == nil {
		return
	}

	tolerance := tc.TargetTolerance
	if tolerance <= 0 {
		tolerance = 2.0
	}
	if tc.Dist <= tolerance {
		return
	}

	tc.B.Mu.Lock()
	if time.Since(tc.B.LastPathRecalcTime) < walkToRepathIntervalFor(tc.B.WalkToRepathFailures) {
		tc.B.Mu.Unlock()
		return
	}
	tc.B.LastPathRecalcTime = time.Now()
	tc.B.Mu.Unlock()

	RecalculatePath(tc.B)

	tc.B.Mu.Lock()
	if len(tc.B.CurrentPath) > 0 {
		tc.B.WalkToRepathFailures = 0
	} else {
		tc.B.WalkToRepathFailures++
	}
	tc.B.Mu.Unlock()
}

func (tc *TickContext) handleStuck() {
	if tc.MState == "idle" {
		return
	}
	tc.B.Mu.Lock()
	frozen := tc.updateStuckCounter()
	stalled := tc.updateStuckProgressWindow()
	if !frozen && !stalled {
		tc.B.Mu.Unlock()
		return
	}

	tc.B.TicksStuck = 0
	tc.B.ConsecutiveStuckCount++
	tc.B.Logger.Debug("Stuck detected",
		"consecutive_count", tc.B.ConsecutiveStuckCount,
		"hasPath", tc.HasPath,
		"frozen", frozen,
		"no_progress", stalled,
	)
	tc.B.Mu.Unlock()

	if tc.tryStuckJump() {
		return
	}

	tc.B.Mu.Lock()
	tc.handleStuckRecalcLocked()
	tc.B.Mu.Unlock()
}

// updateStuckCounter counts consecutive motionless ticks. Returns true once the
// bot has been frozen for stuckIdleTicks.
//
// Note what it no longer does: any per-tick movement used to clear the
// escalation counter, so a bot that jittered or slid while wedged restarted the
// whole recovery ladder on every twitch and could never reach the steps that
// actually break it out. Escalation is now cleared by net progress instead.
func (tc *TickContext) updateStuckCounter() bool {
	moveDeltaX := tc.CurrPos.X() - tc.B.LastTickPos.X()
	moveDeltaZ := tc.CurrPos.Z() - tc.B.LastTickPos.Z()
	moveDeltaY := tc.CurrPos.Y() - tc.B.LastTickPos.Y()
	if moveDeltaX*moveDeltaX+moveDeltaY*moveDeltaY+moveDeltaZ*moveDeltaZ < 0.001 {
		tc.B.TicksStuck++
	} else {
		tc.B.TicksStuck = 0
		tc.B.LastTickPos = tc.CurrPos
	}
	return tc.B.TicksStuck >= stuckIdleTicks
}

// updateStuckProgressWindow reports whether the bot spent a full window trying
// to move without gaining ground toward its destination. Forward progress over
// the window is the signal, so rubberbanding and wall-sliding are both caught
// and real travel clears the escalation ladder.
func (tc *TickContext) updateStuckProgressWindow() bool {
	now := time.Now()
	if tc.B.StuckWindowStart.IsZero() {
		tc.B.StuckWindowStart = now
		tc.B.StuckWindowPos = tc.CurrPos
		return false
	}

	if forwardProgress(tc.B.StuckWindowPos, tc.CurrPos, tc.TPos) >= stuckProgressMinMove {
		// Genuine ground gained: restart the window and drop the escalation
		// ladder, so a bot that struggled once and then got free is not treated
		// as a repeat offender.
		tc.B.StuckWindowStart = now
		tc.B.StuckWindowPos = tc.CurrPos
		tc.B.ConsecutiveStuckCount = 0
		return false
	}

	if now.Sub(tc.B.StuckWindowStart) < stuckProgressWindow {
		return false
	}

	tc.B.StuckWindowStart = now
	tc.B.StuckWindowPos = tc.CurrPos
	return true
}

func (tc *TickContext) tryStuckJump() bool {
	tc.B.Mu.Lock()
	stuckCount := tc.B.ConsecutiveStuckCount
	hasPath := tc.HasPath && tc.B.PathIndex < len(tc.B.CurrentPath)
	var node pathfinder.Node
	if hasPath {
		node = tc.B.CurrentPath[tc.B.PathIndex]
	}
	tc.B.Mu.Unlock()

	if stuckCount != 1 || !hasPath {
		return false
	}

	baseY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.1)))
	if node.Y < baseY {
		return false
	}

	tc.B.Mu.Lock()
	tc.B.Logger.Debug("Stuck-recovery jump triggered", "node_y", node.Y, "base_y", baseY)
	tc.B.Mu.Unlock()

	tc.ShouldJump = true
	tc.JumpReason = "Stuck-recovery jump"
	return true
}

func (tc *TickContext) handleStuckRecalcLocked() {
	consecutive := tc.B.ConsecutiveStuckCount
	if consecutive >= 2 {
		tc.breakPathObstacleLocked()
	}
	// Tell the world model about the cells the bot is physically pressed against
	// before re-planning. A* is deterministic, so re-planning from the same tile
	// against the same model returns the same route and the bot walks into the
	// same blocker again — the recovery loop that made "stuck" permanent. Marking
	// the blocker is what makes the next attempt actually go around it.
	tc.markBlockingCellsTempSolidLocked(stuckPenaltyWindow(consecutive))
	tc.markCurrentPathTempSolidLocked()
	tc.recalculatePathLocked()

	if consecutive < 3 {
		return
	}
	// Dropping the path is only a useful fallback when direct steering can take
	// over. Past that radius a nil path means no steering at all, which is the
	// freeze this ladder is supposed to prevent — so escalate the penalty and
	// keep the route instead of giving up on it.
	if !tc.canDirectSteerToTarget() {
		return
	}
	tc.B.Logger.Warn("Multiple stuck detections, attempting direct movement fallback", "consecutive_count", consecutive)
	tc.B.CurrentPath = nil
	tc.B.PathIndex = 0
	tc.B.ConsecutiveStuckCount = 0
}

// markBlockingCellsTempSolidLocked forces the cells the bot is walking into to
// read as solid for a while, so the next path routes around them.
//
// The cells are chosen from the actual movement direction, not from the path
// node being approached: a smoothed link can span several blocks, so the node
// the bot is "walking toward" is often nowhere near the thing stopping it.
func (tc *TickContext) markBlockingCellsTempSolidLocked(window time.Duration) {
	if tc.B.WorldModel == nil || tc.Dist <= 0.01 {
		return
	}

	dirX := tc.Dx / tc.Dist
	dirZ := tc.Dz / tc.Dist
	feetY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.1)))

	for _, ahead := range []float32{0.5, 1.0} {
		bx := int32(math.Floor(float64(tc.CurrPos.X() + dirX*ahead)))
		bz := int32(math.Floor(float64(tc.CurrPos.Z() + dirZ*ahead)))
		if bx == tc.FeetX && bz == tc.FeetZ {
			// Never fence the bot into its own tile: A* would find no way out.
			continue
		}
		// Anything ahead is penalised, and that deliberately includes a one-block
		// step the bot could have climbed.
		//
		// The blocker is precisely the cell the model misreads as open air, so a
		// "is this a real step or a wall the model decoded wrongly" test cannot
		// tell them apart: both look identical from in here. Penalising the cell
		// and re-routing costs a detour around a staircase; not penalising it
		// costs the thing this whole ladder exists to prevent, which is
		// re-planning the identical route into the identical wedge forever.
		//
		// A legitimate step still gets its jump first: tryStuckJump runs on the
		// first stuck event and only returns here once that has already failed.
		tc.B.WorldModel.SetTempSolid(bx, feetY, bz, window)
	}
}

// canDirectSteerToTarget mirrors the radii in updateAllowDirectSteering: it
// reports whether dropping the path would still leave the bot able to walk.
func (tc *TickContext) canDirectSteerToTarget() bool {
	dx := tc.TPos.X() - tc.CurrPos.X()
	dy := tc.TPos.Y() - tc.CurrPos.Y()
	dz := tc.TPos.Z() - tc.CurrPos.Z()
	dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

	if tc.MState == "walk_to" {
		return dist < directSteerWalkToRadius
	}
	return dist < directSteerFollowRadius && absFloat32(dy) < 1.5
}

func (tc *TickContext) breakPathObstacleLocked() {
	if tc.B.PathIndex >= len(tc.B.CurrentPath) {
		return
	}
	node := tc.B.CurrentPath[tc.B.PathIndex]
	obs := protocol.BlockPos{node.X, node.Y, node.Z}
	tc.B.Mu.Unlock()
	tc.B.BreakObstacleAt(obs)
	tc.B.BreakObstacleAt(protocol.BlockPos{node.X, node.Y + 1, node.Z})
	tc.B.Mu.Lock()
}

func (tc *TickContext) markCurrentPathTempSolidLocked() {
	if tc.B.PathIndex >= len(tc.B.CurrentPath) {
		return
	}
	node := tc.B.CurrentPath[tc.B.PathIndex]
	if node.Y == int32(math.Floor(float64(tc.CurrPos.Y()))) {
		tc.B.WorldModel.SetTempSolid(node.X, node.Y, node.Z, 5*time.Second)
	}
}

func (tc *TickContext) recalculatePathLocked() {
	tc.B.Mu.Unlock()
	RecalculatePath(tc.B)
	tc.B.Mu.Lock()
}

func (tc *TickContext) resetJumpState() {
	tc.ShouldJump = false
	tc.JumpReason = ""
}

func (tc *TickContext) updateParkourAndAutoJump() {
	tc.refreshParkourJump()
	tc.applyParkourJump()
	isNearLadder := tc.isNearLadder()
	tc.applyAutoJump(isNearLadder)
	tc.clampJumpAtHeight()
}

func (tc *TickContext) refreshParkourJump() {
	tc.B.Mu.Lock()
	tc.IsParkourJump = parkourWindowActive(tc.B.ParkourUntil)
	tc.B.Mu.Unlock()
}

func (tc *TickContext) applyParkourJump() {
	if !tc.HasPath {
		return
	}
	tc.B.Mu.Lock()
	if tc.B.PathIndex >= len(tc.B.CurrentPath) || tc.B.PathIndex <= 0 {
		tc.B.Mu.Unlock()
		return
	}

	pathIndex := tc.B.PathIndex
	lastJumpPathIndex := tc.B.LastJumpPathIndex
	prevNode := tc.B.CurrentPath[tc.B.PathIndex-1]
	nextNode := tc.B.CurrentPath[tc.B.PathIndex]
	tc.B.Mu.Unlock()

	baseY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.1)))
	dxPath := math.Abs(float64(nextNode.X - prevNode.X))
	dzPath := math.Abs(float64(nextNode.Z - prevNode.Z))
	horizDistance := float32(math.Max(dxPath, dzPath))

	isParkourLink := nextNode.LinkType == pathfinder.LinkJump ||
		(nextNode.LinkType == pathfinder.LinkStepJump && horizDistance > 1.5)

	// A* sometimes emits the final approach to the target with an empty
	// LinkType (the destination node carries no link annotation). When that
	// node is exactly one block higher than the bot, it is still a step-up —
	// without this fallback the bot walks into the ledge and never jumps.
	yDiff := nextNode.Y - baseY
	unannotatedStepUp := nextNode.LinkType == "" && yDiff == 1 && horizDistance <= 1.5

	if isParkourLink {
		tc.handleParkourLinkJump(nextNode, pathIndex, lastJumpPathIndex, baseY, horizDistance)
	} else if nextNode.LinkType == pathfinder.LinkStepJump || unannotatedStepUp {
		tc.handleStepUpJump(nextNode, baseY)
	}
}

func (tc *TickContext) handleParkourLinkJump(nextNode pathfinder.Node, pathIndex, lastJumpPathIndex int, baseY int32, horizDistance float32) {
	jumpTriggerDist := horizDistance - 0.5
	isApproaching := tc.IsGrounded && tc.Dist >= jumpTriggerDist-0.2
	isJumpingOrMidAir := !tc.IsGrounded

	if isApproaching || isJumpingOrMidAir {
		tc.IsParkourJump = true
		tc.B.Mu.Lock()
		tc.B.ParkourUntil = time.Now().Add(1200 * time.Millisecond)
		tc.B.Mu.Unlock()
	}

	if lastJumpPathIndex == pathIndex {
		// This node already got one jump. If it missed (lag-clip, corner catch),
		// the old guard refused a second attempt for the same node index, so the
		// bot walked into the gap lip until the stuck ladder escalated seconds
		// later — the visible "ngestuck" during gather walks. Once the bot has
		// landed and a beat has passed, allow one fresh attempt.
		tc.B.Mu.Lock()
		elapsed := time.Since(tc.B.LastJumpTime)
		tc.B.Mu.Unlock()
		if !tc.IsGrounded || elapsed < parkourRejumpCooldown {
			return
		}
	}
	if tc.Dist > jumpTriggerDist+0.25 || tc.Dist < 0.2 {
		return
	}
	if !tc.shouldJumpForYaw() {
		return
	}

	tc.ShouldJump = true
	tc.B.Mu.Lock()
	tc.B.LastJumpPathIndex = pathIndex
	tc.B.LastJumpTime = time.Now()
	tc.B.Mu.Unlock()
	tc.JumpReason = fmt.Sprintf("Parkour Gap (%s): dist %.2f, gap %.2f", nextNode.LinkType, tc.Dist, horizDistance)
}

func (tc *TickContext) shouldJumpForYaw() bool {
	yawRad := math.Atan2(float64(tc.Dz), float64(tc.Dx))
	idealYaw := float32(yawRad*180/math.Pi) - 90
	yawDiff := math.Mod(float64(tc.Yaw-idealYaw+540), 360) - 180
	return math.Abs(yawDiff) < 90.0
}

func (tc *TickContext) handleStepUpJump(nextNode pathfinder.Node, baseY int32) {
	if tc.Dist >= 1.4 {
		return
	}
	// No yaw gate here: the pathfinder already validated this 1-block step is
	// reachable, and the bot is adjacent. Requiring body-yaw alignment (which
	// lags a few ticks behind the head after a turn) made the bot stall at the
	// ledge, walking into the wall instead of hopping up. checkWallCollision's
	// groundedStepUp bypass lets the forward motion carry it onto the step.
	tc.ShouldJump = true
	tc.JumpReason = fmt.Sprintf("Step Up: nextNode.Y(%d) > baseY(%d)", nextNode.Y, baseY)
}

func (tc *TickContext) isNearLadder() bool {
	if tc.IsOnLadder {
		return true
	}
	if tc.B.WorldModel == nil || !tc.HasPath {
		return false
	}
	tc.B.Mu.Lock()
	lookahead := len(tc.B.CurrentPath) - tc.B.PathIndex
	if lookahead > 3 {
		lookahead = 3
	}
	if lookahead < 0 {
		lookahead = 0
	}
	for li := 0; li < lookahead; li++ {
		ln := tc.B.CurrentPath[tc.B.PathIndex+li]
		if tc.B.WorldModel.IsLadder(ln.X, ln.Y, ln.Z) || tc.B.WorldModel.IsLadder(ln.X, ln.Y+1, ln.Z) {
			tc.B.Mu.Unlock()
			return true
		}
	}
	tc.B.Mu.Unlock()
	return false
}

func (tc *TickContext) applyAutoJump(isNearLadder bool) {
	if !tc.shouldAutoJump(isNearLadder) {
		return
	}
	tc.performAutoJump()
}

func (tc *TickContext) shouldAutoJump(isNearLadder bool) bool {
	if isNearLadder || tc.ShouldJump || tc.Dist <= 0.1 || tc.MState == "idle" {
		return false
	}
	if !tc.HasPath {
		return true
	}
	// While following a route, hop only for a step the route does not describe.
	// A path link the bot cannot see — a string-pulled straight line across
	// terrain that was still loading when A* planned it — has no jump annotation,
	// so nothing else would ever lift the bot over it and it would push its face
	// into the ledge forever. Annotated jumps keep their own timing in
	// applyParkourJump / handleStepUpJump.
	if !tc.IsGrounded || tc.Dist < 0.5 {
		return false
	}
	return !tc.nextNodeDescribesJump()
}

// nextNodeDescribesJump reports whether the active path node carries a jump
// annotation the jump code already handles.
func (tc *TickContext) nextNodeDescribesJump() bool {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	if tc.B.PathIndex >= len(tc.B.CurrentPath) {
		return false
	}
	link := tc.B.CurrentPath[tc.B.PathIndex].LinkType
	return link == pathfinder.LinkJump || link == pathfinder.LinkStepJump
}

func (tc *TickContext) performAutoJump() {
	moveDirX := tc.Dx / tc.Dist
	moveDirZ := tc.Dz / tc.Dist
	checkDistances := []float32{0.5, 0.8}
	for _, d := range checkDistances {
		checkX := int32(math.Floor(float64(tc.CurrPos.X() + moveDirX*d)))
		checkY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.2)))
		checkZ := int32(math.Floor(float64(tc.CurrPos.Z() + moveDirZ*d)))

		if tc.B.WorldModel.IsSolid(checkX, checkY, checkZ) && !tc.B.WorldModel.IsSolid(checkX, checkY+1, checkZ) && !tc.B.WorldModel.IsSolid(checkX, checkY+2, checkZ) {
			tc.ShouldJump = true
			tc.JumpReason = fmt.Sprintf("Auto-Jump: solid at %d,%d,%d", checkX, checkY, checkZ)
			break
		}
	}
}

func (tc *TickContext) clampJumpAtHeight() {
	if tc.CurrPos.Y() > 320 {
		tc.ShouldJump = false
	}
}
