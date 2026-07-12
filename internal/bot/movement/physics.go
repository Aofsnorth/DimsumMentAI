// Package movement implements tick-level bot movement, path following, and look
// direction. It is responsible for steering, physics, collision resolution, and
// the PlayerAuthInput heartbeat sent to the server.
package movement

import (
	"math"
	"time"
)

func (tc *TickContext) runPhysicsAndCollisions() {
	tc.updateLadderState()
	tc.applyPositionCorrection()
	tc.updateDescendingFlag()
	tc.updateGroundedState()
	tc.applyVerticalVelocity()
	tc.applyStepDownAssist(tc.FeetY)
	tc.applyCeilingCollision()
	tc.applyGroundLanding()
	tc.syncGrounded()
}

func (tc *TickContext) updateLadderState() {
	tc.IsOnLadder = false
	if tc.B.WorldModel == nil {
		return
	}

	feetX := int32(math.Floor(float64(tc.CurrPos.X())))
	feetY := int32(math.Floor(float64(tc.CurrPos.Y())))
	feetZ := int32(math.Floor(float64(tc.CurrPos.Z())))

	if tc.B.WorldModel.IsLadder(feetX, feetY, feetZ) || tc.B.WorldModel.IsLadder(feetX, feetY+1, feetZ) {
		tc.IsOnLadder = true
	}

	if !tc.IsOnLadder && tc.HasPath {
		tc.B.Mu.Lock()
		if tc.B.PathIndex < len(tc.B.CurrentPath) {
			nn := tc.B.CurrentPath[tc.B.PathIndex]
			if tc.B.WorldModel.IsLadder(nn.X, nn.Y, nn.Z) {
				ndx := float64(nn.X) + 0.5 - float64(tc.CurrPos.X())
				ndz := float64(nn.Z) + 0.5 - float64(tc.CurrPos.Z())
				if ndx*ndx+ndz*ndz < 0.25 {
					tc.IsOnLadder = true
				}
			}
		}
		tc.B.Mu.Unlock()
	}

	tc.B.Mu.Lock()
	tc.B.IsOnLadder = tc.IsOnLadder
	tc.B.Mu.Unlock()

	if tc.IsOnLadder {
		tc.ShouldJump = false
	}
}

func (tc *TickContext) applyPositionCorrection() {
	correctionThreshold := float64(0.5)
	if tc.IsOnLadder {
		correctionThreshold = 1.5
	}
	if math.Abs(float64(tc.CurrPos.Y()-tc.LastPredictedY)) > correctionThreshold && !tc.IsOnLadder {
		tc.VelY = 0.0
	}
}

func (tc *TickContext) updateDescendingFlag() {
	tc.IsGrounded = false
	tc.IsDescending = false
	if !tc.HasPath {
		return
	}
	tc.B.Mu.Lock()
	if tc.B.PathIndex < len(tc.B.CurrentPath) {
		nextNode := tc.B.CurrentPath[tc.B.PathIndex]
		if nextNode.Y < tc.FeetY {
			tc.IsDescending = true
		}
	}
	tc.B.Mu.Unlock()
}

func (tc *TickContext) updateGroundedState() {
	checkOffsets := groundCheckOffsets(tc.IsDescending, tc.IsParkourJump)

	for _, dxOffset := range checkOffsets {
		for _, dzOffset := range checkOffsets {
			cx := int32(math.Floor(float64(tc.CurrPos.X() + dxOffset)))
			cy := int32(math.Floor(float64(tc.CurrPos.Y() - 0.01)))
			cz := int32(math.Floor(float64(tc.CurrPos.Z() + dzOffset)))
			if tc.B.WorldModel.IsSolid(cx, cy, cz) {
				tc.IsGrounded = true
				break
			}
		}
		if tc.IsGrounded {
			break
		}
	}
}

func (tc *TickContext) applyVerticalVelocity() {
	if tc.IsOnLadder {
		tc.applyLadderVerticalVelocity()
	} else if tc.IsGrounded {
		tc.VelY = 0.0
		if tc.ShouldJump {
			tc.B.Logger.Debug("jump triggered", "reason", tc.JumpReason, "dist", tc.Dist, "pos", tc.CurrPos, "mState", tc.MState)
			tc.VelY = 0.42
			tc.IsGrounded = false
		}
	} else {
		tc.VelY -= 0.08
		if tc.VelY < -3.92 {
			tc.VelY = -3.92
		}
	}

	tc.NextY = tc.CurrPos.Y() + tc.VelY
}

func (tc *TickContext) applyLadderVerticalVelocity() {
	tc.IsGrounded = true
	tc.VelY = 0.0
	if !tc.HasPath {
		return
	}
	tc.B.Mu.Lock()
	if tc.B.PathIndex < len(tc.B.CurrentPath) {
		nextNode := tc.B.CurrentPath[tc.B.PathIndex]
		targetY := float32(nextNode.Y)
		actualY := tc.CurrPos.Y()
		if actualY < targetY-0.15 {
			tc.VelY = 0.2
		} else if actualY > targetY+0.15 {
			tc.VelY = -0.2
		} else {
			tc.NextY = targetY
		}
	}
	tc.B.Mu.Unlock()
}

func (tc *TickContext) applyCeilingCollision() {
	if tc.VelY <= 0 {
		return
	}

	checkOffsets := groundCheckOffsets(tc.IsDescending, tc.IsParkourJump)
	hasCeiling := false
	for _, dxOffset := range checkOffsets {
		for _, dzOffset := range checkOffsets {
			cx := int32(math.Floor(float64(tc.CurrPos.X() + dxOffset)))
			cy := int32(math.Floor(float64(tc.NextY + 1.8)))
			cz := int32(math.Floor(float64(tc.CurrPos.Z() + dzOffset)))
			if tc.B.WorldModel.IsSolid(cx, cy, cz) {
				hasCeiling = true
				break
			}
		}
		if hasCeiling {
			break
		}
	}

	if hasCeiling {
		tc.VelY = 0.0
		tc.NextY = float32(math.Floor(float64(tc.NextY+1.8))) - 1.8
	}
}

func (tc *TickContext) applyGroundLanding() {
	if tc.VelY > 0 || tc.IsOnLadder {
		return
	}

	checkOffsets := groundCheckOffsets(tc.IsDescending, tc.IsParkourJump)
	hasGroundBelow := false
	var landingCy int32 = -999
	for _, dxOffset := range checkOffsets {
		for _, dzOffset := range checkOffsets {
			cx := int32(math.Floor(float64(tc.CurrPos.X() + dxOffset)))
			cy := int32(math.Floor(float64(tc.NextY - 0.01)))
			cz := int32(math.Floor(float64(tc.CurrPos.Z() + dzOffset)))
			if tc.B.WorldModel.IsSolid(cx, cy, cz) {
				hasGroundBelow = true
				landingCy = cy
				break
			}
		}
		if hasGroundBelow {
			break
		}
	}

	if hasGroundBelow {
		tc.NextY = float32(landingCy + 1)
		tc.VelY = 0.0
		tc.IsGrounded = true
		if tc.IsParkourJump {
			tc.B.Mu.Lock()
			tc.B.ParkourUntil = time.Time{}
			tc.B.Mu.Unlock()
			tc.IsParkourJump = false
		}
	}
}

func (tc *TickContext) syncGrounded() {
	tc.B.Mu.Lock()
	tc.B.IsGrounded = tc.IsGrounded
	tc.B.Mu.Unlock()
}

func groundCheckOffsets(isDescending, isParkourJump bool) []float32 {
	if isDescending {
		return []float32{0.0}
	}
	return []float32{0.0, -0.3, 0.3}
}

func parkourWindowActive(until time.Time) bool {
	return !until.IsZero() && time.Now().Before(until)
}

func (tc *TickContext) applyStepDownAssist(feetY int32) {
	if !tc.IsDescending || !tc.HasPath || tc.IsOnLadder {
		return
	}
	tc.B.Mu.Lock()
	if tc.B.PathIndex >= len(tc.B.CurrentPath) {
		tc.B.Mu.Unlock()
		return
	}
	nextNode := tc.B.CurrentPath[tc.B.PathIndex]
	tc.B.Mu.Unlock()

	if nextNode.Y >= feetY || feetY-nextNode.Y > 3 {
		return
	}
	if tc.Dist > 1.4 {
		return
	}
	targetY := float32(nextNode.Y)
	if tc.CurrPos.Y() > targetY {
		nextY := tc.CurrPos.Y() - 0.16
		if nextY < targetY {
			nextY = targetY
		}
		tc.NextY = nextY
		tc.VelY = tc.NextY - tc.CurrPos.Y()
		tc.IsGrounded = true
	}
}
