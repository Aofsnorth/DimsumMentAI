package movement

import (
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

func (tc *TickContext) performActiveSteering() {
	tc.B.Mu.Lock()
	scaffActive := tc.B.ScaffoldingActive
	tc.B.Mu.Unlock()

	if scaffActive {
		tc.ShouldMove = false
		tc.HasHorizontalMove = false
		tc.MoveVec = mgl32.Vec2{0, 0}
		return
	}

	tc.ShouldMove = tc.MState != "idle" && tc.AllowDirectSteering
	tc.PlayerHeightDiff = 0.0
	if tc.MState == "follow" && tc.TPlayer != "" {
		if _, pPos, ok := tc.B.FindPlayer(tc.TPlayer); ok {
			tc.PlayerHeightDiff = float32(math.Abs(float64(pPos.Y() - tc.CurrPos.Y())))
		}
	}

	tc.HasHorizontalMove = false

	if tc.ShouldMove {
		if tc.MState == "follow" && tc.DistToPlayer < 2.0 && tc.PlayerHeightDiff < 1.5 {
			tc.MoveVec = mgl32.Vec2{0, 0}
		} else {
			tc.HasHorizontalMove = true

			var advanceDist float32 = 0.8
			segmentIsGap := false
			if tc.IsLadderActive {
				advanceDist = 0.25
			} else if tc.HasPath {
				tc.B.Mu.Lock()
				if tc.B.PathIndex < len(tc.B.CurrentPath) {
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
				}
				tc.B.Mu.Unlock()
			}

			maxHeightDiff := 2.5
			if tc.IsLadderActive {
				maxHeightDiff = 1.2
			} else if segmentIsGap {
				maxHeightDiff = 0.55
			}

			if tc.HasPath && tc.Dist < advanceDist {
				tc.B.Mu.Lock()
				nextNode := tc.B.CurrentPath[tc.B.PathIndex]
				heightDiff := math.Abs(float64(nextNode.Y) - float64(tc.CurrPos.Y()))
				if heightDiff < maxHeightDiff {
					tc.B.PathIndex++
					tc.B.TicksStuck = 0
					tc.B.LastTickPos = tc.CurrPos
					if tc.B.PathIndex >= len(tc.B.CurrentPath) {
						tc.B.CurrentPath = nil
						if tc.B.MovementState == "walk_to" {
							tc.B.MovementState = "idle"
							tc.B.Logger.Debug("bot arrived at target destination", slog.Float64("x", float64(tc.TPos.X())), slog.Float64("z", float64(tc.TPos.Z())))
						}
					}
				}
				tc.B.Mu.Unlock()
			} else if !tc.HasPath && tc.Dist < 1.5 {
				if tc.MState == "walk_to" {
					tc.B.Mu.Lock()
					tc.B.MovementState = "idle"
					tc.B.Mu.Unlock()
					tc.B.Logger.Debug("bot arrived at target destination", slog.Float64("x", float64(tc.TPos.X())), slog.Float64("z", float64(tc.TPos.Z())))
				}
			}

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

			if tc.MState != "idle" {
				tc.B.Mu.Lock()
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

				if tc.B.TicksStuck >= 20 {
					tc.B.TicksStuck = 0
					tc.B.ConsecutiveStuckCount++
					tc.B.Logger.Debug("Stuck detected, recalculating path", "consecutive_count", tc.B.ConsecutiveStuckCount, "hasPath", tc.HasPath)

					if tc.HasPath && tc.B.PathIndex < len(tc.B.CurrentPath) {
						node := tc.B.CurrentPath[tc.B.PathIndex]
						tc.B.WorldModel.SetTempSolid(node.X, node.Y, node.Z, 5*time.Second)
						tc.B.Mu.Unlock()
						RecalculatePath(tc.B)
						tc.B.Mu.Lock()
					} else {
						tc.B.Mu.Unlock()
						RecalculatePath(tc.B)
						tc.B.Mu.Lock()
					}

					if tc.B.ConsecutiveStuckCount >= 3 {
						tc.B.Logger.Warn("Multiple stuck detections, attempting direct movement fallback", "consecutive_count", tc.B.ConsecutiveStuckCount)
						tc.B.CurrentPath = nil
						tc.B.ConsecutiveStuckCount = 0
					}
				}
				tc.B.Mu.Unlock()
			}

			tc.ShouldJump = false
			tc.JumpReason = ""
			tc.B.Mu.Lock()
			tc.IsParkourJump = parkourWindowActive(tc.B.ParkourUntil)
			tc.B.Mu.Unlock()
			if tc.HasPath {
				tc.B.Mu.Lock()
				if tc.B.PathIndex < len(tc.B.CurrentPath) {
					nextNode := tc.B.CurrentPath[tc.B.PathIndex]
					baseY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.1)))

					if nextNode.Y > baseY && tc.Dist < 1.8 {
						yawRad := math.Atan2(float64(tc.Dz), float64(tc.Dx))
						idealYaw := float32(yawRad*180/math.Pi) - 90
						yawDiff := math.Mod(float64(tc.B.Yaw-idealYaw+540), 360) - 180

						if math.Abs(yawDiff) < 45.0 {
							tc.ShouldJump = true
							tc.JumpReason = fmt.Sprintf("Step Up: nextNode.Y(%d) > baseY(%d)", nextNode.Y, baseY)
						}
					}

					isGapInPath := false
					gapDistance := float32(0)
					if tc.B.PathIndex > 0 {
						prevNode := tc.B.CurrentPath[tc.B.PathIndex-1]
						dxPath := math.Abs(float64(nextNode.X - prevNode.X))
						dzPath := math.Abs(float64(nextNode.Z - prevNode.Z))
						gapDistance = float32(math.Max(dxPath, dzPath))
						if gapDistance > 1.5 && gapDistance <= 4.0 {
							isGapInPath = true
						}
					}

					if nextNode.Y == baseY && isGapInPath && tc.Dist < gapDistance+0.55 && tc.Dist > 0.55 {
						yawRad := math.Atan2(float64(tc.Dz), float64(tc.Dx))
						idealYaw := float32(yawRad*180/math.Pi) - 90
						yawDiff := math.Mod(float64(tc.B.Yaw-idealYaw+540), 360) - 180
						if math.Abs(yawDiff) < 25.0 {
							tc.ShouldJump = true
							tc.IsParkourJump = true
							tc.B.ParkourUntil = time.Now().Add(1800 * time.Millisecond)
							tc.JumpReason = fmt.Sprintf("Parkour Gap: distance %.0f", gapDistance)
						}
					}
				}
				tc.B.Mu.Unlock()
			}

			if !tc.ShouldJump && tc.Dist > 0.1 && tc.MState != "idle" {
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

			if tc.CurrPos.Y() > 320 {
				tc.ShouldJump = false
			}
		}
	}
}
