package movement

import (
	"math"
	"math/rand"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

func shouldApplyTrackedLook(_ string, wantsToMove bool) bool {
	return !wantsToMove
}

func (tc *TickContext) updateLookDirection() {
	tc.ActivelyClimbing = false
	feetY_l := int32(math.Floor(float64(tc.CurrPos.Y())))
	if tc.IsOnLadder && tc.HasPath {
		tc.B.Mu.Lock()
		if tc.B.PathIndex < len(tc.B.CurrentPath) {
			nn := tc.B.CurrentPath[tc.B.PathIndex]
			if nn.Y != feetY_l && (tc.B.WorldModel.IsLadder(nn.X, nn.Y, nn.Z) || tc.B.WorldModel.IsLadder(nn.X, nn.Y-1, nn.Z)) {
				tc.ActivelyClimbing = true
			}
		}
		tc.B.Mu.Unlock()
	}

	tc.TargetYaw = tc.Yaw
	tc.TargetPitch = tc.Pitch

	wantsToMove := tc.MState == "walk_to" || (tc.MState == "follow" && !(tc.DistToPlayer < 2.0 && tc.PlayerHeightDiff < 1.5))
	lookTargetActive := false
	if shouldApplyTrackedLook(tc.MState, wantsToMove) {
		lookTargetActive = tc.applyTrackedLookTarget()
	}

	if wantsToMove {
		if tc.Dist > 0.1 && tc.HasHorizontalMove {
			yawRad := math.Atan2(float64(tc.Dz), float64(tc.Dx))
			tc.TargetYaw = float32(yawRad*180/math.Pi) - 90
			tc.TargetPitch = 0
		} else {
			tc.TargetYaw = tc.Yaw
			tc.TargetPitch = tc.Pitch
		}

		if tc.ActivelyClimbing && tc.LadderWallYaw != -999 {
			tc.TargetYaw = tc.LadderWallYaw
		}
	} else {
		if lookTargetActive {
			// A command such as lookat/stare owns the gaze briefly.
		} else if tc.MState == "follow" {
			tc.B.Mu.Lock()
			playerPos := tc.B.TargetPos
			tc.B.Mu.Unlock()

			dxP := playerPos.X() - tc.CurrPos.X()
			dzP := playerPos.Z() - tc.CurrPos.Z()
			dyP := (playerPos.Y() + 1.62) - (tc.CurrPos.Y() + 1.62)

			distP := float32(math.Sqrt(float64(dxP*dxP + dzP*dzP)))
			if distP > 0.1 {
				yawRad := math.Atan2(float64(dzP), float64(dxP))
				tc.TargetYaw = float32(yawRad*180/math.Pi) - 90
				pitchRad := math.Atan2(float64(dyP), float64(distP))
				tc.TargetPitch = float32(-pitchRad * 180 / math.Pi)
			} else {
				tc.TargetYaw = tc.Yaw
				tc.TargetPitch = tc.Pitch
			}
		} else {
			tc.applyIdleLook()
		}
	}

	yawDiff := tc.TargetYaw - tc.Yaw
	for yawDiff < -180 {
		yawDiff += 360
	}
	for yawDiff > 180 {
		yawDiff -= 360
	}
	absYawDiff := math.Abs(float64(yawDiff))

	var yawSpeed float32 = 25.0
	if tc.IsLadderActive {
		yawSpeed = 60.0
	} else if absYawDiff > 30.0 {
		yawSpeed = 45.0
	}
	pitchSpeed := float32(15.0)
	if !wantsToMove {
		tc.TargetYaw, tc.TargetPitch = dampenLookJitter(tc.Yaw, tc.Pitch, tc.TargetYaw, tc.TargetPitch)
		pitchSpeed = 7.0
	}

	// Linear InterpolateAngle (constant angular speed) looks robotic on every
	// server. Use ease-out interpolation so head turns decelerate as they
	// approach the target — the way a real player's view settles — plus a faint
	// per-tick micro-jitter so the gaze is never perfectly frozen between
	// targets. Applied to all servers, not just Venity.
	tc.applyEasedLook(wantsToMove, yawSpeed, pitchSpeed)
}

// applyEasedLook performs ease-out yaw/pitch interpolation toward the
// resolved target, tuned to read as a human head movement rather than a
// constant-rate servo. maxStep is taken from the caller's computed speeds so
// large corrections (e.g. ladder turns) still snap quickly.
func (tc *TickContext) applyEasedLook(wantsToMove bool, yawMax, pitchMax float32) {
	// ease = fraction of remaining angle consumed per tick. Lower = softer,
	// longer settle. Moving needs a touch more authority to face travel dir.
	yawEase := float32(0.14)
	pitchEase := float32(0.1)
	yawMin := float32(0.18)
	pitchMin := float32(0.12)
	if wantsToMove {
		yawEase = 0.2
		yawMin = 0.35
	}

	// The caller's yawMax/pitchMax (25–60°/tick) are tuned for the old constant
	// -speed interpolator and feel like a whip-pan with easing. Cap the per-tick
	// step so even large turns stay smooth; ladder turns get a little more room.
	yawCap := float32(9.0)
	pitchCap := float32(6.0)
	if tc.IsLadderActive {
		yawCap = 14.0
	}
	if yawMax > yawCap {
		yawMax = yawCap
	}
	if pitchMax > pitchCap {
		pitchMax = pitchCap
	}

	tc.Yaw = EaseAngle(tc.Yaw, tc.TargetYaw, yawMin, yawMax, yawEase)
	tc.Pitch = EasePitch(tc.Pitch, tc.TargetPitch, pitchMin, pitchMax, pitchEase)

	// Faint idle micro-jitter: only when essentially settled and not walking,
	// so a stationary bot still has a living, breathing gaze instead of a
	// frozen stare. Deterministic-ish via tick parity to avoid Math.rand churn.
	if !wantsToMove {
		yawDiff := angleDifference(tc.TargetYaw, tc.Yaw)
		if math.Abs(float64(yawDiff)) < 0.6 {
			switch tc.Tick % 11 {
			case 0:
				tc.Yaw = normalizeYaw(tc.Yaw + 0.12)
			case 5:
				tc.Yaw = normalizeYaw(tc.Yaw - 0.1)
			case 8:
				tc.Pitch = clampFloat32(tc.Pitch+0.08, -90, 90)
			}
		}
	}
}

func (tc *TickContext) applyTrackedLookTarget() bool {
	tc.B.Mu.Lock()
	name := tc.B.LookTargetName
	until := tc.B.LookTargetUntil
	tc.B.Mu.Unlock()
	if name == "" || time.Now().After(until) {
		if name != "" {
			tc.B.Mu.Lock()
			tc.B.LookTargetName = ""
			tc.B.LookTargetUntil = time.Time{}
			tc.B.Mu.Unlock()
		}
		return false
	}
	if _, pos, ok := tc.B.FindPlayer(name); ok {
		tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 1.62, 0}))
		return true
	}
	return false
}

func (tc *TickContext) applyIdleLook() {
	now := time.Now()
	tc.B.Mu.Lock()
	nextChange := tc.B.NextIdleLookChange
	targetYaw := tc.B.IdleLookTargetYaw
	targetPitch := tc.B.IdleLookTargetPitch
	targetType := tc.B.IdleLookTargetType
	targetID := tc.B.IdleLookTargetID
	targetPos := tc.B.IdleLookTargetPos
	tc.B.Mu.Unlock()

	if !nextChange.IsZero() && now.Before(nextChange) {
		if targetType == "player" {
			if pos, ok := tc.playerPositionByID(targetID); ok {
				tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 1.62, 0}))
				return
			}
		} else if targetType == "actor" {
			if pos, ok := tc.actorPositionByID(targetID); ok {
				tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 0.9, 0}))
				return
			}
		} else if targetType == "block" {
			tc.setLookTarget(targetPos)
			return
		} else {
			tc.TargetYaw = targetYaw
			tc.TargetPitch = targetPitch
			return
		}
	}

	roll := rand.Intn(100)
	if roll < 45 {
		if id, pos, ok := tc.nearestIdlePlayer(14); ok {
			tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 1.62, 0}))
			tc.storeIdleLook("player", id, mgl32.Vec3{}, now.Add(time.Duration(3+rand.Intn(5))*time.Second))
			return
		}
	}
	if roll < 75 {
		if id, pos, ok := tc.nearestIdleActor(12); ok {
			tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 0.9, 0}))
			tc.storeIdleLook("actor", id, mgl32.Vec3{}, now.Add(time.Duration(2+rand.Intn(4))*time.Second))
			return
		}
	}
	if roll < 90 {
		if pos, ok := tc.randomIdleBlock(8); ok {
			tc.setNaturalLookTarget(pos)
			tc.storeIdleLook("block", 0, pos, now.Add(time.Duration(2+rand.Intn(4))*time.Second))
			return
		}
	}

	targetYaw = normalizeYaw(tc.Yaw + float32(rand.Intn(91)-45))
	targetPitch = float32(rand.Intn(15) - 7)
	tc.TargetYaw = targetYaw
	tc.TargetPitch = targetPitch
	tc.storeIdleLook("wander", 0, mgl32.Vec3{}, now.Add(time.Duration(2+rand.Intn(4))*time.Second))
}

func (tc *TickContext) setLookTarget(pos mgl32.Vec3) {
	dx := pos.X() - tc.CurrPos.X()
	dy := pos.Y() - (tc.CurrPos.Y() + 1.62)
	dz := pos.Z() - tc.CurrPos.Z()
	distH := math.Sqrt(float64(dx*dx + dz*dz))
	if distH < 0.001 {
		distH = 0.001
	}
	tc.TargetYaw = normalizeYaw(float32(math.Atan2(float64(dz), float64(dx))*180/math.Pi) - 90)
	tc.TargetPitch = float32(-math.Atan2(float64(dy), distH) * 180 / math.Pi)
}

func (tc *TickContext) setNaturalLookTarget(pos mgl32.Vec3) {
	tc.TargetYaw, tc.TargetPitch = naturalLookAngles(tc.CurrPos, pos, tc.Yaw)
}

func dampenLookJitter(currentYaw, currentPitch, targetYaw, targetPitch float32) (float32, float32) {
	yawDiff := angleDifference(targetYaw, currentYaw)
	if math.Abs(float64(yawDiff)) < 0.8 {
		targetYaw = currentYaw
	}

	pitchDiff := targetPitch - currentPitch
	if math.Abs(float64(pitchDiff)) < 1.4 {
		targetPitch = currentPitch
	} else if math.Abs(float64(pitchDiff)) < 6.0 {
		targetPitch = currentPitch + pitchDiff*0.4
	}
	return targetYaw, targetPitch
}

func naturalLookAngles(originFeet, target mgl32.Vec3, currentYaw float32) (float32, float32) {
	dx := target.X() - originFeet.X()
	dy := target.Y() - (originFeet.Y() + 1.62)
	dz := target.Z() - originFeet.Z()
	distH := math.Sqrt(float64(dx*dx + dz*dz))

	yaw := currentYaw
	if distH >= 0.35 {
		yaw = normalizeYaw(float32(math.Atan2(float64(dz), float64(dx))*180/math.Pi) - 90)
	}

	pitchDist := distH
	if pitchDist < 1.8 {
		pitchDist = 1.8
	}
	pitch := float32(-math.Atan2(float64(dy), pitchDist) * 180 / math.Pi)
	if distH < 1.0 {
		pitch = clampFloat32(pitch, -18, 18)
	} else {
		pitch = clampFloat32(pitch, -40, 40)
	}
	if math.Abs(float64(pitch)) < 1.25 {
		pitch = 0
	}
	return yaw, pitch
}

func (tc *TickContext) storeIdleLook(targetType string, targetID uint64, targetPos mgl32.Vec3, until time.Time) {
	tc.B.Mu.Lock()
	tc.B.IdleLookTargetType = targetType
	tc.B.IdleLookTargetID = targetID
	tc.B.IdleLookTargetPos = targetPos
	tc.B.IdleLookTargetYaw = tc.TargetYaw
	tc.B.IdleLookTargetPitch = tc.TargetPitch
	tc.B.NextIdleLookChange = until
	tc.B.Mu.Unlock()
}

func (tc *TickContext) playerPositionByID(id uint64) (mgl32.Vec3, bool) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	pos, ok := tc.B.PlayerPositions[id]
	return pos, ok
}

func (tc *TickContext) actorPositionByID(id uint64) (mgl32.Vec3, bool) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	act, ok := tc.B.Actors[id]
	if !ok || act == nil {
		return mgl32.Vec3{}, false
	}
	return act.Position, true
}

func (tc *TickContext) nearestIdlePlayer(maxDist float32) (uint64, mgl32.Vec3, bool) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()

	var bestID uint64
	var bestPos mgl32.Vec3
	bestDist := maxDist * maxDist
	found := false
	for id, pos := range tc.B.PlayerPositions {
		dx := pos.X() - tc.CurrPos.X()
		dy := pos.Y() - tc.CurrPos.Y()
		dz := pos.Z() - tc.CurrPos.Z()
		dist := dx*dx + dy*dy + dz*dz
		if dist <= bestDist {
			bestDist = dist
			bestPos = pos
			bestID = id
			found = true
		}
	}
	return bestID, bestPos, found
}

func (tc *TickContext) nearestIdleActor(maxDist float32) (uint64, mgl32.Vec3, bool) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()

	var bestID uint64
	var bestPos mgl32.Vec3
	bestDist := maxDist * maxDist
	found := false
	for id, act := range tc.B.Actors {
		if act == nil {
			continue
		}
		dx := act.Position.X() - tc.CurrPos.X()
		dy := act.Position.Y() - tc.CurrPos.Y()
		dz := act.Position.Z() - tc.CurrPos.Z()
		dist := dx*dx + dy*dy + dz*dz
		if dist <= bestDist {
			bestDist = dist
			bestPos = act.Position
			bestID = id
			found = true
		}
	}
	return bestID, bestPos, found
}

func (tc *TickContext) randomIdleBlock(maxDist int32) (mgl32.Vec3, bool) {
	feetX := int32(math.Floor(float64(tc.CurrPos.X())))
	feetY := int32(math.Floor(float64(tc.CurrPos.Y())))
	feetZ := int32(math.Floor(float64(tc.CurrPos.Z())))

	for attempt := 0; attempt < 36; attempt++ {
		dx := int32(rand.Intn(int(maxDist*2+1))) - maxDist
		dz := int32(rand.Intn(int(maxDist*2+1))) - maxDist
		if dx*dx+dz*dz < 4 {
			continue
		}
		dy := int32(rand.Intn(5)) - 1
		x, y, z := feetX+dx, feetY+dy, feetZ+dz
		if tc.B.WorldModel.IsSolid(x, y, z) &&
			!tc.B.WorldModel.IsHazard(x, y, z) &&
			!tc.B.WorldModel.IsSolid(x, y+1, z) {
			return mgl32.Vec3{float32(x) + 0.5, float32(y) + 0.55, float32(z) + 0.5}, true
		}
	}
	return mgl32.Vec3{}, false
}

func clampFloat32(v, min, max float32) float32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func angleDifference(target, current float32) float32 {
	diff := target - current
	for diff < -180 {
		diff += 360
	}
	for diff > 180 {
		diff -= 360
	}
	return diff
}

func normalizeYaw(yaw float32) float32 {
	for yaw < 0 {
		yaw += 360
	}
	for yaw >= 360 {
		yaw -= 360
	}
	return yaw
}
