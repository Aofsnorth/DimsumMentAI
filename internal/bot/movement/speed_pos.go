package movement

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

func (tc *TickContext) calculateMovementSpeedAndPosition() {
	var predictedPos mgl32.Vec3
	feetX_l := int32(math.Floor(float64(tc.CurrPos.X())))
	feetZ_l := int32(math.Floor(float64(tc.CurrPos.Z())))

	yawDiff := tc.TargetYaw - tc.Yaw
	for yawDiff < -180 {
		yawDiff += 360
	}
	for yawDiff > 180 {
		yawDiff -= 360
	}
	absYawDiff := math.Abs(float64(yawDiff))

	if tc.IsOnLadder && tc.ActivelyClimbing {
		ladderCenterX := float32(feetX_l) + 0.5
		ladderCenterZ := float32(feetZ_l) + 0.5
		centerSpeed := float32(0.15)
		newX := tc.CurrPos.X() + (ladderCenterX-tc.CurrPos.X())*centerSpeed
		newZ := tc.CurrPos.Z() + (ladderCenterZ-tc.CurrPos.Z())*centerSpeed
		predictedPos = mgl32.Vec3{newX, tc.NextY, newZ}
		tc.HasHorizontalMove = false
	} else if tc.HasHorizontalMove && tc.Dist > 0.05 {
		var speed float32 = 0.215
		tc.B.Mu.Lock()
		if tc.HasPath && len(tc.B.CurrentPath)-tc.B.PathIndex > 2 {
			speed = 0.28
		}
		tc.B.Mu.Unlock()

		needsStepUp := false
		isMidJump := !tc.IsGrounded || tc.VelY > 0.05
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
			}
			tc.B.Mu.Unlock()
		}

		needsStepDown := false
		if tc.HasPath {
			tc.B.Mu.Lock()
			if tc.B.PathIndex < len(tc.B.CurrentPath) {
				nextNode := tc.B.CurrentPath[tc.B.PathIndex]
				baseY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.1)))
				if nextNode.Y < baseY {
					needsStepDown = true
					speed = 0.13
				}
			}
			tc.B.Mu.Unlock()
		}
		_ = needsStepDown

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
		var stepX, stepZ float32
		if tc.Dist > 0.01 && speed > 0.001 {
			stepX = (tc.Dx / tc.Dist) * speed
			stepZ = (tc.Dz / tc.Dist) * speed
		}

		targetX := tc.CurrPos.X() + stepX
		targetZ := tc.CurrPos.Z() + stepZ
		baseY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.1)))
		descentTargetY, descentDrop, plannedDescent := tc.plannedDescent(baseY)
		hasSameLevelSupport := tc.hasGroundSupportAt(targetX, targetZ, baseY)
		if !needsStepUp && !tc.IsLadderActive && !tc.IsParkourJump && !isMidJump && !plannedDescent && !hasSameLevelSupport {
			targetX = tc.CurrPos.X()
			targetZ = tc.CurrPos.Z()
			tc.HasHorizontalMove = false
		}
		if plannedDescent && !hasSameLevelSupport && !tc.IsLadderActive && !tc.IsParkourJump {
			tc.NextY, tc.VelY = controlledDescentY(tc.CurrPos.Y(), tc.NextY, float32(descentTargetY), descentDrop)
			tc.IsGrounded = true
		}

		hasWall := false
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

		for bx := minX; bx <= maxX; bx++ {
			for by := minY; by <= maxY; by++ {
				for bz := minZ; bz <= maxZ; bz++ {
					if tc.B.WorldModel.IsSolid(bx, by, bz) {
						hasWall = true
						break
					}
				}
				if hasWall {
					break
				}
			}
			if hasWall {
				break
			}
		}

		isNearLadder := tc.IsOnLadder
		if !isNearLadder && tc.B.WorldModel != nil && tc.HasPath {
			tc.B.Mu.Lock()
			lookahead := 3
			if tc.B.PathIndex+lookahead > len(tc.B.CurrentPath) {
				lookahead = len(tc.B.CurrentPath) - tc.B.PathIndex
			}
			for li := 0; li < lookahead; li++ {
				ln := tc.B.CurrentPath[tc.B.PathIndex+li]
				if tc.B.WorldModel.IsLadder(ln.X, ln.Y, ln.Z) || tc.B.WorldModel.IsLadder(ln.X, ln.Y+1, ln.Z) {
					isNearLadder = true
					break
				}
			}
			tc.B.Mu.Unlock()
		}

		if isNearLadder {
			hasWall = false
		}

		if isMidJump && needsStepUp {
			hasWall = false
		}

		if hasWall {
			predictedPos = mgl32.Vec3{tc.CurrPos.X(), tc.NextY, tc.CurrPos.Z()}
		} else {
			predictedPos = mgl32.Vec3{targetX, tc.NextY, targetZ}
		}
	} else {
		predictedPos = mgl32.Vec3{tc.CurrPos.X(), tc.NextY, tc.CurrPos.Z()}
	}

	tc.B.Mu.Lock()
	tc.B.Pos = predictedPos
	tc.B.VelY = tc.VelY
	tc.B.Mu.Unlock()

	tc.CurrPos = predictedPos
	tc.LastPredictedY = predictedPos.Y()
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
