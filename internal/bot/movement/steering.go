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
				// Re-validate the path snapshot under the lock. HasPath was
				// computed in resolveNextTarget; another goroutine (e.g. a
				// concurrent RecalculatePath) can have nulled CurrentPath
				// since then.
				if tc.B.PathIndex < len(tc.B.CurrentPath) {
					nextNode := tc.B.CurrentPath[tc.B.PathIndex]
					yDiff := float64(nextNode.Y) - float64(tc.CurrPos.Y())
					heightDiff := math.Abs(yDiff)

					canAdvance := heightDiff < maxHeightDiff
					if yDiff > 0.5 {
						// Allow advancing while climbing if we're within one block of node Y.
						canAdvance = heightDiff < 1.05 && float64(tc.CurrPos.Y())+0.15 >= float64(nextNode.Y)
					}

					if canAdvance {
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
				}
				tc.B.Mu.Unlock()
			} else if !tc.HasPath && tc.MState == "walk_to" {
				// For walk_to, consider arrived if within 2 blocks in XZ and 2 blocks in Y
				dyTarget := tc.CurrPos.Y() - tc.TPos.Y()
				distXZ := float32(math.Sqrt(float64(tc.Dx*tc.Dx + tc.Dz*tc.Dz)))
				if distXZ < 2.0 && math.Abs(float64(dyTarget)) < 2.0 {
					tc.B.Mu.Lock()
					tc.B.MovementState = "idle"
					tc.B.Mu.Unlock()
					tc.B.Logger.Debug("bot arrived at target destination (within 2 blocks)", slog.Float64("x", float64(tc.TPos.X())), slog.Float64("y", float64(tc.TPos.Y())), slog.Float64("z", float64(tc.TPos.Z())))
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

				if tc.B.TicksStuck >= 12 {
					tc.B.TicksStuck = 0
					tc.B.ConsecutiveStuckCount++
					tc.B.Logger.Debug("Stuck detected", "consecutive_count", tc.B.ConsecutiveStuckCount, "hasPath", tc.HasPath)

					// First stuck: try a jump to unstick before expensive recalc.
					// Many "stuck" situations are just the bot needing to hop up
					// a block that the path expects but the physics missed.
					needRecalc := true
					if tc.B.ConsecutiveStuckCount == 1 && tc.HasPath && tc.B.PathIndex < len(tc.B.CurrentPath) {
						node := tc.B.CurrentPath[tc.B.PathIndex]
						baseY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.1)))
						if node.Y >= baseY {
							tc.B.Mu.Unlock()
							tc.ShouldJump = true
							tc.JumpReason = "Stuck-recovery jump"
							tc.B.Logger.Debug("Stuck-recovery jump triggered", "node_y", node.Y, "base_y", baseY)
							tc.B.Mu.Lock()
							needRecalc = false
						}
					}

					if needRecalc {
						// On the SECOND consecutive stuck, try breaking whatever is
						// in front of the bot at head height. First stuck just
						// retries pathing; if a wall really is blocking us we'll
						// arrive here again and break through.
						if tc.B.ConsecutiveStuckCount >= 2 && tc.HasPath && tc.B.PathIndex < len(tc.B.CurrentPath) {
							node := tc.B.CurrentPath[tc.B.PathIndex]
							obs := protocol.BlockPos{node.X, node.Y, node.Z}
							tc.B.Mu.Unlock()
							tc.B.BreakObstacleAt(obs)
							// Also try the block above (head height) in case the
							// floor block is fine but a ceiling overhang is.
							tc.B.BreakObstacleAt(protocol.BlockPos{node.X, node.Y + 1, node.Z})
							tc.B.Mu.Lock()
						}

						if tc.HasPath && tc.B.PathIndex < len(tc.B.CurrentPath) {
							node := tc.B.CurrentPath[tc.B.PathIndex]
							if node.Y == int32(math.Floor(float64(tc.CurrPos.Y()))) {
								tc.B.WorldModel.SetTempSolid(node.X, node.Y, node.Z, 5*time.Second)
							}
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
				if tc.B.PathIndex < len(tc.B.CurrentPath) && tc.B.PathIndex > 0 {
					prevNode := tc.B.CurrentPath[tc.B.PathIndex-1]
					nextNode := tc.B.CurrentPath[tc.B.PathIndex]
					baseY := int32(math.Floor(float64(tc.CurrPos.Y() + 0.1)))

					dxPath := math.Abs(float64(nextNode.X - prevNode.X))
					dzPath := math.Abs(float64(nextNode.Z - prevNode.Z))
					horizDistance := float32(math.Max(dxPath, dzPath))

					// Check if we are approaching a parkour link (horizontal gap jump OR step jump with gap)
					isParkourLink := nextNode.LinkType == pathfinder.LinkJump ||
						(nextNode.LinkType == pathfinder.LinkStepJump && horizDistance > 1.5)

					if isParkourLink {
						jumpTriggerDist := horizDistance - 0.5
						isApproaching := tc.IsGrounded && tc.Dist >= jumpTriggerDist-0.2
						isJumpingOrMidAir := !tc.IsGrounded

						if isApproaching || isJumpingOrMidAir {
							tc.IsParkourJump = true
							tc.B.ParkourUntil = time.Now().Add(1200 * time.Millisecond)
						}

						// Wide window for jumping to avoid "bullet-through-paper" race condition
						if tc.B.LastJumpPathIndex != tc.B.PathIndex && tc.Dist <= jumpTriggerDist+0.25 && tc.Dist >= 0.2 {
							yawRad := math.Atan2(float64(tc.Dz), float64(tc.Dx))
							idealYaw := float32(yawRad*180/math.Pi) - 90
							yawDiff := math.Mod(float64(tc.B.Yaw-idealYaw+540), 360) - 180
							if math.Abs(yawDiff) < 90.0 {
								tc.ShouldJump = true
								tc.B.LastJumpPathIndex = tc.B.PathIndex
								tc.JumpReason = fmt.Sprintf("Parkour Gap (%s): dist %.2f, gap %.2f", nextNode.LinkType, tc.Dist, horizDistance)
							}
						}
					} else if nextNode.LinkType == pathfinder.LinkStepJump {
						// Directly adjacent step-up jump (distance <= 1.5)
						if tc.Dist < 1.4 {
							yawRad := math.Atan2(float64(tc.Dz), float64(tc.Dx))
							idealYaw := float32(yawRad*180/math.Pi) - 90
							yawDiff := math.Mod(float64(tc.B.Yaw-idealYaw+540), 360) - 180
							if math.Abs(yawDiff) < 90.0 {
								tc.ShouldJump = true
								tc.JumpReason = fmt.Sprintf("Step Up: nextNode.Y(%d) > baseY(%d)", nextNode.Y, baseY)
							}
						}
					}
				}
				tc.B.Mu.Unlock()
			}

			// Disable auto-jump check entirely when on or near a ladder
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

			// Enable auto-jump check only when direct-steering (no path)
			if !tc.HasPath && !isNearLadder && !tc.ShouldJump && tc.Dist > 0.1 && tc.MState != "idle" {
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

// pathAheadIsLevelOrDown is true when no remaining path node requires climbing higher.
// Used to stop step-up / auto-jump at the top of block stairs.
func (tc *TickContext) pathAheadIsLevelOrDown() bool {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	if !tc.HasPath || tc.B.PathIndex >= len(tc.B.CurrentPath) {
		return true
	}
	baselineY := tc.B.CurrentPath[tc.B.PathIndex].Y
	for i := tc.B.PathIndex + 1; i < len(tc.B.CurrentPath); i++ {
		if tc.B.CurrentPath[i].Y > baselineY {
			return false
		}
	}
	return true
}
