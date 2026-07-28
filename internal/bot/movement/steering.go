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
	if tc.MState == "follow" && tc.TPlayer != "" {
		if _, pPos, ok := tc.B.FindPlayer(tc.TPlayer); ok {
			tc.PlayerHeightDiff = float32(math.Abs(float64(pPos.Y() - tc.CurrPos.Y())))
		}
	}
	tc.HasHorizontalMove = false
}

func (tc *TickContext) closeFollowTarget() bool {
	if tc.MState == "follow" && tc.DistToPlayer < 2.0 && tc.PlayerHeightDiff < 1.5 {
		return true
	}
	return false
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

func (tc *TickContext) handleStuck() {
	if tc.MState == "idle" {
		return
	}
	tc.B.Mu.Lock()
	tc.updateStuckCounter()
	if tc.B.TicksStuck < 12 {
		tc.B.Mu.Unlock()
		return
	}

	tc.B.TicksStuck = 0
	tc.B.ConsecutiveStuckCount++
	tc.B.Logger.Debug("Stuck detected", "consecutive_count", tc.B.ConsecutiveStuckCount, "hasPath", tc.HasPath)
	tc.B.Mu.Unlock()

	if tc.tryStuckJump() {
		return
	}

	tc.B.Mu.Lock()
	tc.handleStuckRecalcLocked()
	tc.B.Mu.Unlock()
}

func (tc *TickContext) updateStuckCounter() {
	moveDeltaX := tc.CurrPos.X() - tc.B.LastTickPos.X()
	moveDeltaZ := tc.CurrPos.Z() - tc.B.LastTickPos.Z()
	moveDeltaY := tc.CurrPos.Y() - tc.B.LastTickPos.Y()
	if moveDeltaX*moveDeltaX+moveDeltaY*moveDeltaY+moveDeltaZ*moveDeltaZ < 0.001 {
		tc.B.TicksStuck++
	} else {
		tc.B.TicksStuck = 0
		tc.B.ConsecutiveStuckCount = 0
		tc.B.LastTickPos = tc.CurrPos
	}
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
	if tc.B.ConsecutiveStuckCount >= 2 {
		tc.breakPathObstacleLocked()
	}
	tc.markCurrentPathTempSolidLocked()
	tc.recalculatePathLocked()
	if tc.B.ConsecutiveStuckCount >= 3 {
		tc.B.Logger.Warn("Multiple stuck detections, attempting direct movement fallback", "consecutive_count", tc.B.ConsecutiveStuckCount)
		tc.B.CurrentPath = nil
		tc.B.ConsecutiveStuckCount = 0
	}
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
		return
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
	return !tc.HasPath && !isNearLadder && !tc.ShouldJump && tc.Dist > 0.1 && tc.MState != "idle"
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
