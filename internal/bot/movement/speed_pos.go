// Package movement implements tick-level bot movement, path following, and look
// direction. It is responsible for steering, physics, collision resolution, and
// the PlayerAuthInput heartbeat sent to the server.
package movement

import (
	"math"

	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/debuglog"

	"github.com/go-gl/mathgl/mgl32"
)

func (tc *TickContext) calculateMovementSpeedAndPosition() {
	predictedPos := tc.computePredictedPosition()

	tc.B.Mu.Lock()
	tc.B.Pos = predictedPos
	tc.B.VelY = tc.VelY
	tc.B.Mu.Unlock()

	tc.CurrPos = predictedPos
	tc.LastPredictedY = predictedPos.Y()
}

func (tc *TickContext) computePredictedPosition() mgl32.Vec3 {
	if tc.IsOnLadder && tc.ActivelyClimbing {
		return tc.ladderPredictedPos()
	}
	if tc.HasHorizontalMove && tc.Dist > 0.05 {
		return tc.horizontalMovePredictedPos()
	}
	return mgl32.Vec3{tc.CurrPos.X(), tc.NextY, tc.CurrPos.Z()}
}

func (tc *TickContext) ladderPredictedPos() mgl32.Vec3 {
	feetX := int32(math.Floor(float64(tc.CurrPos.X())))
	feetZ := int32(math.Floor(float64(tc.CurrPos.Z())))
	ladderCenterX := float32(feetX) + 0.5
	ladderCenterZ := float32(feetZ) + 0.5
	centerSpeed := float32(0.15)
	newX := tc.CurrPos.X() + (ladderCenterX-tc.CurrPos.X())*centerSpeed
	newZ := tc.CurrPos.Z() + (ladderCenterZ-tc.CurrPos.Z())*centerSpeed
	tc.HasHorizontalMove = false
	return mgl32.Vec3{newX, tc.NextY, newZ}
}

func (tc *TickContext) horizontalMovePredictedPos() mgl32.Vec3 {
	yawDiff := angleDifference(tc.TargetYaw, tc.Yaw)
	absYawDiff := math.Abs(float64(yawDiff))
	speed, needsStepUp, isMidJump := tc.computeMoveSpeed(absYawDiff)
	targetX, targetZ := tc.computeTargetPosition(speed, needsStepUp, isMidJump)
	if tc.checkWallCollision(targetX, targetZ, needsStepUp, isMidJump) {
		return mgl32.Vec3{tc.CurrPos.X(), tc.NextY, tc.CurrPos.Z()}
	}
	return mgl32.Vec3{targetX, tc.NextY, targetZ}
}

func (tc *TickContext) computeMoveSpeed(absYawDiff float64) (float32, bool, bool) {
	speed, needsStepUp, isMidJump := tc.computeBaseSpeed()
	if tc.IsLadderActive {
		speed = 0.12
	}
	if tc.IsParkourJump {
		speed = 0.34
	}
	if absYawDiff > 15.0 {
		factor := float32(1.0 - (absYawDiff-15.0)/75.0)
		if factor < 0.1 {
			factor = 0.1
		}
		speed = speed * factor
	}
	if tc.Dist < speed {
		speed = tc.Dist
	}
	return speed, needsStepUp, isMidJump
}

func (tc *TickContext) computeBaseSpeed() (float32, bool, bool) {
	speed := float32(0.215)
	tc.B.Mu.Lock()
	if tc.HasPath && len(tc.B.CurrentPath)-tc.B.PathIndex > 2 {
		speed = 0.28
	}
	tc.B.Mu.Unlock()

	isMidJump := !tc.IsGrounded || tc.VelY > 0.05
	needsStepUp := false

	if tc.HasPath {
		tc.B.Mu.Lock()
		if tc.B.PathIndex < len(tc.B.CurrentPath) {
			nextNode := tc.B.CurrentPath[tc.B.PathIndex]
			baseY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.1)))
			if nextNode.Y > baseY {
				needsStepUp = true
				if isMidJump {
					speed = 0.2
				} else {
					speed = 0.18
				}
			}
			if nextNode.Y < baseY {
				speed = 0.13
			}
		}
		tc.B.Mu.Unlock()
	}

	return speed, needsStepUp, isMidJump
}

func (tc *TickContext) computeTargetPosition(speed float32, needsStepUp, isMidJump bool) (targetX, targetZ float32) {
	var stepX, stepZ float32
	if tc.Dist > 0.01 && speed > 0.001 {
		stepX = (tc.Dx / tc.Dist) * speed
		stepZ = (tc.Dz / tc.Dist) * speed
	}

	targetX = tc.CurrPos.X() + stepX
	targetZ = tc.CurrPos.Z() + stepZ
	baseY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.1)))
	descentTargetY, descentDrop, plannedDescent := tc.plannedDescent(baseY)
	hasSameLevelSupport := tc.hasGroundSupportAt(targetX, targetZ, baseY)
	pathAllowsGap := tc.pathAllowsForwardWithoutGround(baseY)
	groundUnknown := tc.B.VenityCompat && !hasSameLevelSupport && tc.groundSupportUnknownAt(targetX, targetZ, baseY)
	trustPath := groundUnknown && tc.HasPath

	if tc.isMoveBlocked(needsStepUp, isMidJump, plannedDescent, hasSameLevelSupport, pathAllowsGap, trustPath) {
		targetX = tc.CurrPos.X()
		targetZ = tc.CurrPos.Z()
		tc.HasHorizontalMove = false
		tc.logVenityWalkBlocked(baseY, hasSameLevelSupport, pathAllowsGap, plannedDescent, needsStepUp, groundUnknown)
	} else if tc.shouldDescent(plannedDescent, hasSameLevelSupport) {
		tc.NextY, tc.VelY = controlledDescentY(tc.CurrPos.Y(), tc.NextY, float32(descentTargetY), descentDrop)
		tc.IsGrounded = true
	}

	return
}

func (tc *TickContext) isMoveBlocked(needsStepUp, isMidJump, plannedDescent, hasSameLevelSupport, pathAllowsGap, trustPath bool) bool {
	return !needsStepUp && !tc.IsLadderActive && !tc.IsParkourJump && !isMidJump && !plannedDescent && !hasSameLevelSupport && !pathAllowsGap && !trustPath
}

func (tc *TickContext) shouldDescent(plannedDescent, hasSameLevelSupport bool) bool {
	return plannedDescent && !hasSameLevelSupport && !tc.IsLadderActive && !tc.IsParkourJump
}

func (tc *TickContext) checkWallCollision(targetX, targetZ float32, needsStepUp, isMidJump bool) bool {
	wallCheckMinY := tc.NextY + 0.1
	if isMidJump && needsStepUp {
		wallCheckMinY = tc.NextY + 0.5
	}

	minX := int32(math.Floor(float64(targetX - 0.3)))
	maxX := int32(math.Floor(float64(targetX + 0.3)))
	minZ := int32(math.Floor(float64(targetZ - 0.3)))
	maxZ := int32(math.Floor(float64(targetZ + 0.3)))
	minY := int32(math.Floor(float64(wallCheckMinY)))
	maxY := int32(math.Floor(float64(tc.NextY + 1.8)))

	// When stepping up onto a 1-block ledge while still grounded, the solid
	// block we are about to jump ONTO sits at body level (minY). A naive scan
	// reads it as a wall and cancels all forward motion, so the bot just hops
	// in place against the ledge forever. Ignore that single bottom level here
	// and rely on the head level (minY+1) to catch genuine 2+ block walls.
	groundedStepUp := needsStepUp && !isMidJump

	for bx := minX; bx <= maxX; bx++ {
		for by := minY; by <= maxY; by++ {
			if groundedStepUp && by == minY {
				continue
			}
			for bz := minZ; bz <= maxZ; bz++ {
				if tc.B.WorldModel.IsSolid(bx, by, bz) {
					return true
				}
			}
		}
	}

	if tc.isNearLadder() {
		return false
	}
	if isMidJump && needsStepUp {
		return false
	}
	return false
}

func (tc *TickContext) plannedDescent(baseY int32) (int32, int32, bool) {
	if !tc.HasPath {
		return 0, 0, false
	}
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	if tc.B.PathIndex >= len(tc.B.CurrentPath) {
		return 0, 0, false
	}
	nextNode := tc.B.CurrentPath[tc.B.PathIndex]
	drop := baseY - nextNode.Y
	return nextNode.Y, drop, drop > 0 && drop <= 3
}

func controlledDescentY(currentY, physicsNextY, targetY float32, drop int32) (float32, float32) {
	if currentY <= targetY {
		return targetY, 0
	}
	step := 0.16 + float32(drop)*0.08
	if step > 0.42 {
		step = 0.42
	}
	nextY := currentY - step
	if physicsNextY < nextY {
		nextY = physicsNextY
	}
	if nextY < targetY {
		nextY = targetY
	}
	return nextY, nextY - currentY
}

// pathAllowsForwardWithoutGround is true when the active path expects a gap
// jump or the next stand node already has floor support.
func (tc *TickContext) pathAllowsForwardWithoutGround(feetY int32) bool {
	if !tc.HasPath || tc.B.WorldModel == nil {
		return false
	}
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	if tc.B.PathIndex >= len(tc.B.CurrentPath) {
		return false
	}
	next := tc.B.CurrentPath[tc.B.PathIndex]
	if next.LinkType == pathfinder.LinkJump || next.LinkType == pathfinder.LinkStepJump {
		return true
	}
	if tc.B.PathIndex > 0 {
		prev := tc.B.CurrentPath[tc.B.PathIndex-1]
		dx := abs32(next.X - prev.X)
		dz := abs32(next.Z - prev.Z)
		if dx > 1 || dz > 1 {
			return true
		}
	}
	floorY := next.Y - 1
	if floorY < feetY-3 {
		return false
	}
	return tc.B.WorldModel.IsSolid(next.X, floorY, next.Z) &&
		!tc.B.WorldModel.IsHazard(next.X, floorY, next.Z)
}

func (tc *TickContext) hasGroundSupportAt(x, z float32, feetY int32) bool {
	supportY := feetY - 1
	offsets := []float32{0, -0.25, 0.25}
	for _, dx := range offsets {
		for _, dz := range offsets {
			bx := int32(math.Floor(float64(x + dx)))
			bz := int32(math.Floor(float64(z + dz)))
			if tc.B.WorldModel.IsSolid(bx, supportY, bz) && !tc.B.WorldModel.IsHazard(bx, supportY, bz) {
				return true
			}
		}
	}
	return false
}

// groundSupportUnknownAt reports whether the support cell under the next step is
// genuinely *unloaded* (not yet decoded) rather than confirmed air. Used only on
// Venity, where lazy chunk decoding means "not solid" usually means "not known".
// Returns false if the WorldCache confirms the cell is loaded (so a real ledge /
// air gap still blocks the step as normal).
func (tc *TickContext) groundSupportUnknownAt(x, z float32, feetY int32) bool {
	if tc.B.WorldCache == nil {
		return false
	}
	supportY := feetY - 1
	offsets := []float32{0, -0.25, 0.25}
	anyUnknown := false
	for _, dx := range offsets {
		for _, dz := range offsets {
			bx := int32(math.Floor(float64(x + dx)))
			bz := int32(math.Floor(float64(z + dz)))
			if _, loaded := tc.B.WorldCache.IsBlockSolid(bx, supportY, bz); !loaded {
				anyUnknown = true
			}
		}
	}
	return anyUnknown
}

// logVenityWalkBlocked records why a forward step was canceled. This is the
// primary signal for disambiguating the "bot can't walk on Venity" hypotheses:
// H1 (ground unknown / unloaded), H2 (server pins position), H3 (input rejected).
// Gated on debug logging (log_level: debug) and Venity only.
func (tc *TickContext) logVenityWalkBlocked(baseY int32, hasSupport, pathGap, descent, stepUp, groundUnknown bool) {
	if !tc.B.VenityCompat || !debuglog.Enabled() {
		return
	}
	if tc.Tick%10 != 0 {
		return
	}
	tc.B.Mu.Lock()
	pathLen := len(tc.B.CurrentPath)
	pathIdx := tc.B.PathIndex
	mState := tc.B.MovementState
	tc.B.Mu.Unlock()
	// #region agent log
	debuglog.Log("V", "speed_pos.go:walkBlocked", "venity forward step canceled", map[string]any{
		"tick":          tc.Tick,
		"mState":        mState,
		"hasPath":       tc.HasPath,
		"pathLen":       pathLen,
		"pathIdx":       pathIdx,
		"hasSupport":    hasSupport,
		"pathAllowsGap": pathGap,
		"plannedDesc":   descent,
		"needsStepUp":   stepUp,
		"groundUnknown": groundUnknown,
		"dist":          tc.Dist,
		"x":             tc.CurrPos.X(),
		"y":             tc.CurrPos.Y(),
		"z":             tc.CurrPos.Z(),
		"runId":         "venity-walk-v1",
	})
	// #endregion
}
