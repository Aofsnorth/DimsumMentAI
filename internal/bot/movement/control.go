package movement

import (
	"math"
)

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
		if tc.MState == "follow" {
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
			tc.TargetYaw = tc.Yaw
			tc.TargetPitch = tc.Pitch
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
	tc.Yaw = InterpolateAngle(tc.Yaw, tc.TargetYaw, yawSpeed)
	tc.Pitch = InterpolatePitch(tc.Pitch, tc.TargetPitch, 15.0)
}
