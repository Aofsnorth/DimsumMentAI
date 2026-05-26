package movement

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// SendInputLoop handles the physical updates and steering of the bot
func SendInputLoop(ctx context.Context, b *bot.Bot, gd minecraft.GameData) {
	ticker := time.NewTicker(time.Second / 20) // 20 ticks/sec
	defer ticker.Stop()
	tick := uint64(0)

	b.Mu.Lock()
	initYaw := b.Yaw
	initPitch := b.Pitch
	initPos := b.Pos
	b.Mu.Unlock()

	var idleLookTargetYaw float32 = initYaw
	var idleLookTargetPitch float32 = initPitch
	var idleLookTicksRemaining int = 0
	var idleLookWaitTicks int = 30 + rand.Intn(50)
	idleLookState := "waiting"
	_ = idleLookTargetYaw
	_ = idleLookTargetPitch
	_ = idleLookTicksRemaining
	_ = idleLookWaitTicks
	_ = idleLookState

	var lastPredictedY float32 = initPos.Y()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 1. Read all state variables under lock
			b.Mu.Lock()
			currentPos := b.Pos
			mState := b.MovementState
			tPlayer := b.TargetPlayerName
			tPos := b.TargetPos
			yaw := b.Yaw
			pitch := b.Pitch
			velY := b.VelY
			b.Mu.Unlock()

			// === AUTO-LEARN PASSABLE BLOCKS ===
			feetX := int32(math.Floor(float64(currentPos.X())))
			feetY := int32(math.Floor(float64(currentPos.Y())))
			feetZ := int32(math.Floor(float64(currentPos.Z())))

			b.WorldModel.SetSolid(feetX, feetY-1, feetZ, true)
			b.WorldModel.SetSolid(feetX, feetY, feetZ, false)
			b.WorldModel.SetSolid(feetX, feetY+1, feetZ, false)
			b.WorldModel.SetSolid(feetX, feetY+2, feetZ, false)
			// ====================================

			// === DETECT ACTIVE LADDER STATE ===
			isLadderActive := false
			if b.WorldModel != nil {
				if b.WorldModel.IsLadder(feetX, feetY, feetZ) || b.WorldModel.IsLadder(feetX, feetY+1, feetZ) {
					isLadderActive = true
				} else {
					b.Mu.Lock()
					hasPathForLadder := len(b.CurrentPath) > 0 && b.PathIndex < len(b.CurrentPath)
					var currNode pathfinder.Node
					if hasPathForLadder {
						currNode = b.CurrentPath[b.PathIndex]
					}
					b.Mu.Unlock()

					if hasPathForLadder {
						if b.WorldModel.IsLadder(currNode.X, currNode.Y, currNode.Z) || b.WorldModel.IsLadder(currNode.X, currNode.Y-1, currNode.Z) {
							isLadderActive = true
						}
					}
				}
			}

			// === HIT PLAYER DISTANCE ===
			var distToPlayer float32 = 999.0
			if mState == "follow" && tPlayer != "" {
				if _, pPos, ok := b.FindPlayer(tPlayer); ok {
					dxP := pPos.X() - currentPos.X()
					dzP := pPos.Z() - currentPos.Z()
					distToPlayer = float32(math.Sqrt(float64(dxP*dxP + dzP*dzP)))
				}
			}

			// Update target position dynamically if following a player
			if mState == "follow" && tPlayer != "" {
				if _, pos, ok := b.FindPlayer(tPlayer); ok {
					// Other player positions are already feet-level in Bedrock Edition
					playerFeetPos := pos

					b.Mu.Lock()
					dxT := playerFeetPos.X() - b.TargetPos.X()
					dzT := playerFeetPos.Z() - b.TargetPos.Z()
					dyT := playerFeetPos.Y() - b.TargetPos.Y()
					timeSinceRecalc := time.Since(b.LastPathRecalcTime)

					dxP := playerFeetPos.X() - currentPos.X()
					dzP := playerFeetPos.Z() - currentPos.Z()
					dPlayer := float32(math.Sqrt(float64(dxP*dxP + dzP*dzP)))
					hDiff := float32(math.Abs(float64(playerFeetPos.Y() - currentPos.Y())))

					isClose := dPlayer < 2.0 && hDiff < 1.5

					if isClose {
						b.CurrentPath = nil
						b.PathIndex = 0
						b.TargetPos = playerFeetPos
						b.Mu.Unlock()
					} else {
						// Recalculate path if player moved significantly (more than 2 blocks in 3D)
						if (dxT*dxT+dyT*dyT+dzT*dzT > 4.0) && timeSinceRecalc > 500*time.Millisecond {
							b.TargetPos = playerFeetPos
							b.LastPathRecalcTime = time.Now()
							b.Mu.Unlock()
							RecalculatePath(b)
							b.Mu.Lock()
						} else {
							b.TargetPos = playerFeetPos
						}
						tPos = b.TargetPos

						hasPath := len(b.CurrentPath) > 0 && b.PathIndex < len(b.CurrentPath)
						if (!hasPath || b.TicksStuck > 10) && timeSinceRecalc > 800*time.Millisecond {
							b.LastPathRecalcTime = time.Now()
							b.Mu.Unlock()
							RecalculatePath(b)
							b.Mu.Lock()
						}
						b.Mu.Unlock()
					}
				}
			}

			b.Mu.Lock()
			hasPath := len(b.CurrentPath) > 0 && b.PathIndex < len(b.CurrentPath)
			var nextTarget mgl32.Vec3
			if hasPath {
				node := b.CurrentPath[b.PathIndex]
				nextTarget = mgl32.Vec3{float32(node.X) + 0.5, float32(node.Y), float32(node.Z) + 0.5}
			} else {
				nextTarget = tPos
			}
			b.Mu.Unlock()

			if !hasPath {
				nextTarget = tPos
				b.Mu.Lock()
				b.CurrentPath = nil
				b.Mu.Unlock()
			}

			dx := nextTarget.X() - currentPos.X()
			dz := nextTarget.Z() - currentPos.Z()
			dist := float32(math.Sqrt(float64(dx*dx + dz*dz)))

			shouldJump := false
			jumpReason := ""
			prevPos := currentPos
			var moveVec mgl32.Vec2
			var moveDelta mgl32.Vec3

			// Determine if we should allow direct steering fallback
			allowDirectSteering := false
			if hasPath {
				allowDirectSteering = true
			} else {
				// If no path, only allow direct movement if target is close (within 8.0 blocks) AND at the same height level
				var distanceToTarget float32 = 999.0
				if mState == "follow" && tPlayer != "" {
					distanceToTarget = distToPlayer
				} else {
					distanceToTarget = dist
				}

				var hDiffToTarget float32 = 0.0
				if mState == "follow" && tPlayer != "" {
					if _, pPos, ok := b.FindPlayer(tPlayer); ok {
						hDiffToTarget = float32(math.Abs(float64(pPos.Y() - currentPos.Y())))
					}
				} else {
					hDiffToTarget = float32(math.Abs(float64(tPos.Y() - currentPos.Y())))
				}

				if distanceToTarget < 8.0 && hDiffToTarget < 1.5 {
					allowDirectSteering = true
				}
			}

			// Active steering
			shouldMove := mState != "idle" && allowDirectSteering
			var playerHeightDiff float32 = 0.0
			if mState == "follow" && tPlayer != "" {
				if _, pPos, ok := b.FindPlayer(tPlayer); ok {
					playerHeightDiff = float32(math.Abs(float64(pPos.Y() - currentPos.Y())))
				}
			}

			hasHorizontalMove := false

			if shouldMove {
				if mState == "follow" && distToPlayer < 2.0 && playerHeightDiff < 1.5 {
					// Stop horizontal movement
					moveVec = mgl32.Vec2{0, 0}
				} else {
					hasHorizontalMove = true

					var advanceDist float32 = 0.8
					if isLadderActive {
						advanceDist = 0.25
					} else if hasPath && b.PathIndex < len(b.CurrentPath) {
						currNode := b.CurrentPath[b.PathIndex]

						// If height changes, use moderate distance — too tight causes stalling on steps
						if currNode.Y != int32(math.Floor(float64(currentPos.Y()+0.1))) {
							advanceDist = 0.5
						} else if b.PathIndex > 0 && b.PathIndex+1 < len(b.CurrentPath) {
							// If the path turns (corner), align closely to avoid clipping inside corners
							prevNode := b.CurrentPath[b.PathIndex-1]
							nextNode := b.CurrentPath[b.PathIndex+1]

							dx1 := currNode.X - prevNode.X
							dz1 := currNode.Z - prevNode.Z
							dx2 := nextNode.X - currNode.X
							dz2 := nextNode.Z - currNode.Z

							// If direction changes, it's a turn
							if dx1 != dx2 || dz1 != dz2 {
								advanceDist = 0.3
							}
						}
					}

					maxHeightDiff := 2.5
					if isLadderActive {
						maxHeightDiff = 0.5
					}

					if hasPath && dist < advanceDist {
						b.Mu.Lock()
						nextNode := b.CurrentPath[b.PathIndex]
						heightDiff := math.Abs(float64(nextNode.Y) - float64(currentPos.Y()))
						// Increase heightDiff tolerance so jumping over obstacles doesn't prevent advancing to the next node,
						// but use a tighter limit on ladders to climb them step-by-step.
						if heightDiff < maxHeightDiff {
							b.PathIndex++
							b.TicksStuck = 0
							b.LastTickPos = currentPos
							if b.PathIndex >= len(b.CurrentPath) {
								b.CurrentPath = nil
								if b.MovementState == "walk_to" {
									b.MovementState = "idle"
									b.Logger.Info("bot arrived at target destination", slog.Float64("x", float64(tPos.X())), slog.Float64("z", float64(tPos.Z())))
								}
							}
						}
						b.Mu.Unlock()
					} else if !hasPath && dist < 1.5 {
						if mState == "walk_to" {
							b.Mu.Lock()
							b.MovementState = "idle"
							b.Mu.Unlock()
							b.Logger.Info("bot arrived at target destination", slog.Float64("x", float64(tPos.X())), slog.Float64("z", float64(tPos.Z())))
						}
					}

					// Recalculate active target variables in case PathIndex or CurrentPath changed
					b.Mu.Lock()
					hasPath = len(b.CurrentPath) > 0 && b.PathIndex < len(b.CurrentPath)
					if hasPath {
						node := b.CurrentPath[b.PathIndex]
						nextTarget = mgl32.Vec3{float32(node.X) + 0.5, float32(node.Y), float32(node.Z) + 0.5}
					} else {
						nextTarget = tPos
					}
					b.Mu.Unlock()
					dx = nextTarget.X() - currentPos.X()
					dz = nextTarget.Z() - currentPos.Z()
					dist = float32(math.Sqrt(float64(dx*dx + dz*dz)))

					if mState != "idle" {
						b.Mu.Lock()
						moveDeltaX := currentPos.X() - b.LastTickPos.X()
						moveDeltaZ := currentPos.Z() - b.LastTickPos.Z()
						moveDeltaY := currentPos.Y() - b.LastTickPos.Y()
						if moveDeltaX*moveDeltaX+moveDeltaY*moveDeltaY+moveDeltaZ*moveDeltaZ < 0.001 {
							b.TicksStuck++
						} else {
							b.TicksStuck = 0
							b.ConsecutiveStuckCount = 0
							b.LastTickPos = currentPos
						}

						if b.TicksStuck >= 20 {
							b.TicksStuck = 0
							b.ConsecutiveStuckCount++
							b.Logger.Debug("Stuck detected, recalculating path", "consecutive_count", b.ConsecutiveStuckCount, "hasPath", hasPath)

							if hasPath && b.PathIndex < len(b.CurrentPath) {
								node := b.CurrentPath[b.PathIndex]
								b.WorldModel.SetTempSolid(node.X, node.Y, node.Z, 5*time.Second)
								b.Mu.Unlock()
								RecalculatePath(b)
								b.Mu.Lock()
							} else {
								b.Mu.Unlock()
								RecalculatePath(b)
								b.Mu.Lock()
							}

							if b.ConsecutiveStuckCount >= 3 {
								b.Logger.Warn("Multiple stuck detections, attempting direct movement fallback", "consecutive_count", b.ConsecutiveStuckCount)
								b.CurrentPath = nil
								b.ConsecutiveStuckCount = 0
							}
						}
						b.Mu.Unlock()
					}

					// === JUMP LOGIC ===
					shouldJump = false
					jumpReason = ""
					if hasPath && b.PathIndex < len(b.CurrentPath) {
						nextNode := b.CurrentPath[b.PathIndex]
						baseY := int32(math.Floor(float64(currentPos.Y() + 0.1)))

						// 1. Step Up: Jump if next node is higher
						if nextNode.Y > baseY && dist < 1.8 {
							// Check if we are roughly facing the right direction before jumping
							yawRad := math.Atan2(float64(dz), float64(dx))
							idealYaw := float32(yawRad*180/math.Pi) - 90
							yawDiff := math.Mod(float64(b.Yaw-idealYaw+540), 360) - 180
							
							if math.Abs(yawDiff) < 45.0 {
								shouldJump = true
								jumpReason = fmt.Sprintf("Step Up: nextNode.Y(%d) > baseY(%d)", nextNode.Y, baseY)
							}
						}

						// 2. Jump Gap: Jump over 1-block gaps
						isGapInPath := false
						if b.PathIndex > 0 {
							prevNode := b.CurrentPath[b.PathIndex-1]
							dxPath := math.Abs(float64(nextNode.X - prevNode.X))
							dzPath := math.Abs(float64(nextNode.Z - prevNode.Z))
							if dxPath > 1.5 || dzPath > 1.5 {
								isGapInPath = true
							}
						}

						if nextNode.Y == baseY && isGapInPath && dist < 2.5 && dist > 0.3 {
							shouldJump = true
							jumpReason = "Jump Gap"
						}
					}

					// General Obstacle Auto-Jump: If there is a solid block at leg level right in front in the direction of movement, jump!
					if !shouldJump && dist > 0.1 && mState != "idle" {
						moveDirX := dx / dist
						moveDirZ := dz / dist

						checkDistances := []float32{0.5, 0.8}
						for _, d := range checkDistances {
							checkX := int32(math.Floor(float64(currentPos.X() + moveDirX*d)))
							checkY := int32(math.Floor(float64(currentPos.Y() + 0.2)))
							checkZ := int32(math.Floor(float64(currentPos.Z() + moveDirZ*d)))

							// If solid at leg level, but empty at head level and above, jump!
							if b.WorldModel.IsSolid(checkX, checkY, checkZ) && !b.WorldModel.IsSolid(checkX, checkY+1, checkZ) && !b.WorldModel.IsSolid(checkX, checkY+2, checkZ) {
								shouldJump = true
								jumpReason = fmt.Sprintf("Auto-Jump: solid at %d,%d,%d", checkX, checkY, checkZ)
								break
							}
						}
					}

					if currentPos.Y() > 320 {
						shouldJump = false
					}
				}
			}

			// === PHYSICS & GRAVITY SIMULATION ===
			isOnLadder := false
			feetX_l := int32(math.Floor(float64(currentPos.X())))
			feetY_l := int32(math.Floor(float64(currentPos.Y())))
			feetZ_l := int32(math.Floor(float64(currentPos.Z())))
			if b.WorldModel != nil {
				// Check feet, head, and one block below (for transitions)
				if b.WorldModel.IsLadder(feetX_l, feetY_l, feetZ_l) || b.WorldModel.IsLadder(feetX_l, feetY_l+1, feetZ_l) {
					isOnLadder = true
				}
				// Pre-detect: if next path node is a ladder and we're very close, treat as on ladder
				if !isOnLadder && hasPath && b.PathIndex < len(b.CurrentPath) {
					nn := b.CurrentPath[b.PathIndex]
					if b.WorldModel.IsLadder(nn.X, nn.Y, nn.Z) {
						ndx := float64(nn.X) + 0.5 - float64(currentPos.X())
						ndz := float64(nn.Z) + 0.5 - float64(currentPos.Z())
						if ndx*ndx+ndz*ndz < 0.25 { // within 0.5 blocks horizontally
							isOnLadder = true
						}
					}
				}
			}

			// Share ladder state with network handler
			b.Mu.Lock()
			b.IsOnLadder = isOnLadder
			b.Mu.Unlock()

			// Suppress jump logic when on ladder — ladder climbing handles vertical movement
			if isOnLadder {
				shouldJump = false
			}

			// If the server teleports or corrects Y position, reset local Y prediction
			// But be more lenient on ladders (allow up to 1.0 block difference)
			correctionThreshold := float64(0.5)
			if isOnLadder {
				correctionThreshold = 1.5
			}
			if math.Abs(float64(currentPos.Y()-lastPredictedY)) > correctionThreshold {
				if !isOnLadder {
					velY = 0.0
				}
			}

			// 1. Grounded check
			// When the next path node is LOWER (descending), use a tighter check (center only)
			// so the bot falls off edges immediately instead of "floating" with bounding box corners
			isGrounded := false
			isDescending := false
			if hasPath && b.PathIndex < len(b.CurrentPath) {
				nextNode := b.CurrentPath[b.PathIndex]
				if nextNode.Y < feetY_l {
					isDescending = true
				}
			}

			var checkOffsets []float32
			if isDescending {
				// Descending: only check center point — fall as soon as center is over air
				checkOffsets = []float32{0.0}
			} else {
				// Normal: check center + bounding box corners
				checkOffsets = []float32{0.0, -0.3, 0.3}
			}
			for _, dxOffset := range checkOffsets {
				for _, dzOffset := range checkOffsets {
					cx := int32(math.Floor(float64(currentPos.X() + dxOffset)))
					cy := int32(math.Floor(float64(currentPos.Y() - 0.01)))
					cz := int32(math.Floor(float64(currentPos.Z() + dzOffset)))
					if b.WorldModel.IsSolid(cx, cy, cz) {
						isGrounded = true
						break
					}
				}
				if isGrounded {
					break
				}
			}

			// 2. Gravity and Jump logic
			if isOnLadder {
				isGrounded = true
				velY = 0.0 // Default: hold position on ladder (sneak)
				if hasPath && b.PathIndex < len(b.CurrentPath) {
					nextNode := b.CurrentPath[b.PathIndex]
					if nextNode.Y > feetY_l {
						velY = 0.2 // Climb up — matches Minecraft's ladder speed
					} else if nextNode.Y < feetY_l {
						velY = -0.2 // Climb down — matches Minecraft's ladder speed
					} else if nextNode.X != feetX_l || nextNode.Z != feetZ_l {
						// Next node is on a different X/Z — we're leaving the ladder
						// Allow normal horizontal movement (velY stays 0)
					}
				}
			} else if isGrounded {
				velY = 0.0
				if shouldJump {
					b.Logger.Info("JUMP TRIGGERED", "reason", jumpReason, "dist", dist, "pos", currentPos, "mState", mState)
					velY = 0.42
					isGrounded = false
				}
			} else {
				velY -= 0.08
				if velY < -3.92 {
					velY = -3.92
				}
			}

			nextY := currentPos.Y() + velY

			// 3. Ceiling collision check
			if velY > 0 {
				hasCeiling := false
				for _, dxOffset := range checkOffsets {
					for _, dzOffset := range checkOffsets {
						cx := int32(math.Floor(float64(currentPos.X() + dxOffset)))
						cy := int32(math.Floor(float64(nextY + 1.8)))
						cz := int32(math.Floor(float64(currentPos.Z() + dzOffset)))
						if b.WorldModel.IsSolid(cx, cy, cz) {
							hasCeiling = true
							break
						}
					}
					if hasCeiling {
						break
					}
				}
				if hasCeiling {
					velY = 0.0
					nextY = float32(math.Floor(float64(nextY+1.8))) - 1.8
				}
			}

			// 4. Landing check
			if velY <= 0 {
				hasGroundBelow := false
				var landingCy int32 = -999
				for _, dxOffset := range checkOffsets {
					for _, dzOffset := range checkOffsets {
						cx := int32(math.Floor(float64(currentPos.X() + dxOffset)))
						cy := int32(math.Floor(float64(nextY)))
						cz := int32(math.Floor(float64(currentPos.Z() + dzOffset)))
						if b.WorldModel.IsSolid(cx, cy, cz) {
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
					nextY = float32(landingCy + 1)
					velY = 0.0
					isGrounded = true
				}
			}

			// 5. Horizontal collision and position prediction
			var predictedPos mgl32.Vec3

			// Determine if bot is actively climbing (on ladder and next node or its base is a ladder)
			activelyClimbing := false
			if isOnLadder && hasPath && b.PathIndex < len(b.CurrentPath) {
				nn := b.CurrentPath[b.PathIndex]
				if nn.Y != feetY_l && (b.WorldModel.IsLadder(nn.X, nn.Y, nn.Z) || b.WorldModel.IsLadder(nn.X, nn.Y-1, nn.Z)) {
					activelyClimbing = true
				}
			}

			if isOnLadder && activelyClimbing {
				// While actively climbing a ladder: center horizontally on the ladder block
				// and suppress horizontal movement to prevent drifting off
				ladderCenterX := float32(feetX_l) + 0.5
				ladderCenterZ := float32(feetZ_l) + 0.5
				// Smoothly interpolate toward center (avoid snapping)
				centerSpeed := float32(0.15)
				newX := currentPos.X() + (ladderCenterX-currentPos.X())*centerSpeed
				newZ := currentPos.Z() + (ladderCenterZ-currentPos.Z())*centerSpeed
				predictedPos = mgl32.Vec3{newX, nextY, newZ}
				// Override horizontal movement flags
				hasHorizontalMove = false
			} else if hasHorizontalMove && dist > 0.05 {
				var speed float32 = 0.215 // Default walking speed
				if hasPath && len(b.CurrentPath)-b.PathIndex > 2 {
					speed = 0.28 // Sprint if we have a path ahead
				}

				// If the next node is HIGHER (step up), adjust speed for approach
				needsStepUp := false
				isMidJump := velY > 0.05 // Bot is currently mid-jump (ascending)
				if hasPath && b.PathIndex < len(b.CurrentPath) {
					nextNode := b.CurrentPath[b.PathIndex]
					baseY := int32(math.Floor(float64(currentPos.Y() + 0.1)))
					if nextNode.Y > baseY {
						needsStepUp = true

						if isMidJump {
							// Mid-jump: maintain good forward speed to reach the block
							speed = 0.2
						} else {
							// On ground approaching the step: move at moderate speed
							// Don't stop to align — just walk toward it
							speed = 0.18
						}
					}
				}
				_ = needsStepUp

				// If the next node is LOWER (descending), slow down to descend smoothly without overshooting
				needsStepDown := false
				if hasPath && b.PathIndex < len(b.CurrentPath) {
					nextNode := b.CurrentPath[b.PathIndex]
					baseY := int32(math.Floor(float64(currentPos.Y() + 0.1)))
					if nextNode.Y < baseY {
						needsStepDown = true
						speed = 0.13 // Slow down so we drop down immediately off the edge instead of flying forward
					}
				}
				_ = needsStepDown

				// On or approaching ladder: slow down to align perfectly and climb safely
				if isLadderActive {
					speed = 0.12
				}

				if dist < speed {
					speed = dist // Cap speed to prevent overshooting targets
				}
				var stepX, stepZ float32
				if dist > 0.01 && speed > 0.001 {
					stepX = (dx / dist) * speed
					stepZ = (dz / dist) * speed
				}

				targetX := currentPos.X() + stepX
				targetZ := currentPos.Z() + stepZ

				// Wall collision check using AABB
				hasWall := false

				// KEY FIX: When mid-jump stepping up, raise the collision check Y
				// so the step block (at foot level) doesn't block forward movement.
				// The bot's feet are rising above the step block during the jump.
				wallCheckMinY := nextY + 0.1
				if isMidJump && needsStepUp {
					// During jump: only check for walls ABOVE the step block
					// nextY already includes velY, so the bot is rising
					// Add 0.5 to skip the step block at foot level
					wallCheckMinY = nextY + 0.5
				}

				minX := int32(math.Floor(float64(targetX - 0.3)))
				maxX := int32(math.Floor(float64(targetX + 0.3)))
				minZ := int32(math.Floor(float64(targetZ - 0.3)))
				maxZ := int32(math.Floor(float64(targetZ + 0.3)))
				minY := int32(math.Floor(float64(wallCheckMinY)))
				maxY := int32(math.Floor(float64(nextY + 1.8)))

				for bx := minX; bx <= maxX; bx++ {
					for by := minY; by <= maxY; by++ {
						for bz := minZ; bz <= maxZ; bz++ {
							if b.WorldModel.IsSolid(bx, by, bz) {
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

				// Bypass wall check if we are on a ladder, or any of the next 3 path nodes is a ladder
				isNearLadder := isOnLadder
				if !isNearLadder && b.WorldModel != nil && hasPath {
					// Check the current target and up to 2 nodes ahead
					lookahead := 3
					if b.PathIndex+lookahead > len(b.CurrentPath) {
						lookahead = len(b.CurrentPath) - b.PathIndex
					}
					for li := 0; li < lookahead; li++ {
						ln := b.CurrentPath[b.PathIndex+li]
						if b.WorldModel.IsLadder(ln.X, ln.Y, ln.Z) || b.WorldModel.IsLadder(ln.X, ln.Y+1, ln.Z) {
							isNearLadder = true
							break
						}
					}
				}

				if isNearLadder {
					hasWall = false
				}

				// Also bypass wall collision entirely during mid-jump step-up
				// The bot NEEDS to move forward to land on the step
				if isMidJump && needsStepUp {
					hasWall = false
				}

				if hasWall {
					predictedPos = mgl32.Vec3{currentPos.X(), nextY, currentPos.Z()}
				} else {
					predictedPos = mgl32.Vec3{targetX, nextY, targetZ}
				}
			} else {
				predictedPos = mgl32.Vec3{currentPos.X(), nextY, currentPos.Z()}
			}

			b.Mu.Lock()
			b.Pos = predictedPos
			b.VelY = velY
			b.Mu.Unlock()

			currentPos = predictedPos
			lastPredictedY = predictedPos.Y()
			moveDelta = currentPos.Sub(prevPos)

			// STATE-BASED LOOK CONTROLLER
			var targetYaw float32 = yaw
			var targetPitch float32 = pitch

			// wantsToMove is true if we are actively navigating (walking or following and not yet arrived)
			wantsToMove := mState == "walk_to" || (mState == "follow" && !(distToPlayer < 2.0 && playerHeightDiff < 1.5))

			if wantsToMove {
				if dist > 0.1 && hasHorizontalMove {
					yawRad := math.Atan2(float64(dz), float64(dx))
					targetYaw = float32(yawRad*180/math.Pi) - 90
					targetPitch = 0
				} else {
					// We are still navigating, but temporarily stopped horizontally (e.g. preparing for jump).
					// Keep current looking direction, do NOT look at player yet.
					targetYaw = yaw
					targetPitch = pitch
				}
			} else {
				// We have ARRIVED! (wantsToMove is false)
				if mState == "follow" {
					b.Mu.Lock()
					playerPos := b.TargetPos
					b.Mu.Unlock()
					
					dxP := playerPos.X() - currentPos.X()
					dzP := playerPos.Z() - currentPos.Z()
					dyP := (playerPos.Y() + 1.62) - (currentPos.Y() + 1.62) // Eye level
					
					distP := float32(math.Sqrt(float64(dxP*dxP + dzP*dzP)))
					if distP > 0.1 {
						yawRad := math.Atan2(float64(dzP), float64(dxP))
						targetYaw = float32(yawRad*180/math.Pi) - 90
						pitchRad := math.Atan2(float64(dyP), float64(distP))
						targetPitch = float32(-pitchRad * 180 / math.Pi)
					} else {
						targetYaw = yaw
						targetPitch = pitch
					}
				} else {
					targetYaw = yaw
					targetPitch = pitch
				}
			}

			var yawSpeed float32 = 25.0
			yaw = InterpolateAngle(yaw, targetYaw, yawSpeed)
			pitch = InterpolatePitch(pitch, targetPitch, 15.0)

			// Calculate MoveVector relative to the updated yaw we will send in this packet
			yawWorldRad := float64(yaw+90) * math.Pi / 180
			forwardX := float32(math.Cos(yawWorldRad))
			forwardZ := float32(math.Sin(yawWorldRad))

			if hasHorizontalMove && dist > 0.01 {
				moveDirX := dx / dist
				moveDirZ := dz / dist
				moveForward := moveDirX*forwardX + moveDirZ*forwardZ
				moveStrafe := moveDirX*(-forwardZ) + moveDirZ*forwardX
				moveVec = mgl32.Vec2{moveStrafe, moveForward}
			}

			if isOnLadder && activelyClimbing {
				if velY > 0 {
					moveVec = mgl32.Vec2{0, 1.0} // W key (Forward) to climb up
				} else if velY < 0 {
					moveVec = mgl32.Vec2{0, -1.0} // S key (Backward) to climb down
				}
			}

			inputData := protocol.NewBitset(packet.PlayerAuthInputBitsetSize)
			if isGrounded {
				inputData.Set(packet.InputFlagVerticalCollision)
			}
			if shouldJump {
				inputData.Set(packet.InputFlagJumping)
			}
			// Sneak-hold on ladder: prevents sliding down when not actively climbing
			if isOnLadder && velY <= 0.0 && !activelyClimbing {
				inputData.Set(packet.InputFlagSneaking)
			}
			// Also sneak while climbing down on ladder for controlled descent
			if isOnLadder && velY < 0 {
				inputData.Set(packet.InputFlagSneaking)
			}
			if moveVec.Y() > 0.1 {
				inputData.Set(packet.InputFlagUp)
			} else if moveVec.Y() < -0.1 {
				inputData.Set(packet.InputFlagDown)
			}
			if moveVec.X() > 0.1 {
				inputData.Set(packet.InputFlagRight)
			} else if moveVec.X() < -0.1 {
				inputData.Set(packet.InputFlagLeft)
			}
			if moveVec.Y() > 0.5 && !isOnLadder {
				inputData.Set(packet.InputFlagSprinting)
			}

			b.Mu.Lock()
			if b.EmoteTicks > 0 {
				b.EmoteTicks--
				isPathfindingState := mState == "follow" || mState == "walk_to"
				switch b.EmoteState {
				case "jump":
					inputData.Set(packet.InputFlagJumping)
				case "sneak":
					inputData.Set(packet.InputFlagSneaking)
				case "spin":
					if !isPathfindingState {
						yaw = InterpolateAngle(yaw, yaw+18, 18)
					}
				case "wiggle":
					if !isPathfindingState {
						if b.EmoteTicks%4 < 2 {
							yaw = InterpolateAngle(yaw, yaw+15, 15)
						} else {
							yaw = InterpolateAngle(yaw, yaw-15, 15)
						}
					}
				case "lookaround":
					if !isPathfindingState {
						if b.EmoteTicks%5 == 0 {
							yaw = InterpolateAngle(yaw, yaw+float32((tick%50)-25), 25)
							pitch = InterpolatePitch(pitch, pitch+float32((tick%30)-15), 15)
						}
					}
				case "nod":
					if !isPathfindingState {
						if b.EmoteTicks%8 < 4 {
							pitch = InterpolatePitch(pitch, 30, 10)
						} else {
							pitch = InterpolatePitch(pitch, -10, 10)
						}
					}
				case "shake":
					if !isPathfindingState {
						if b.EmoteTicks%8 < 4 {
							yaw = InterpolateAngle(yaw, yaw+20, 20)
						} else {
							yaw = InterpolateAngle(yaw, yaw-20, 20)
						}
					}
				}
				if b.EmoteTicks == 0 {
					b.EmoteState = ""
				}
			}
			b.Yaw = yaw
			b.Pitch = pitch
			b.Mu.Unlock()

			pk := &packet.PlayerAuthInput{
				Position:           currentPos.Add(mgl32.Vec3{0, 1.62, 0}),
				Pitch:              pitch,
				Yaw:                yaw,
				HeadYaw:            yaw,
				MoveVector:         moveVec,
				InputData:          inputData,
				InputMode:          packet.InputModeMouse,
				PlayMode:           packet.PlayModeNormal,
				InteractionModel:   packet.InteractionModelClassic,
				Tick:               tick,
				Delta:              moveDelta,
				AnalogueMoveVector: moveVec,
				RawMoveVector:      moveVec,
			}
			if err := b.Conn.WritePacket(pk); err != nil {
				b.Logger.Warn("SendInputLoop: connection closed or write failed", "error", err.Error())
				return
			}
			tick++
		}
	}
}

// InterpolateAngle smoothly moves current angle towards target by at most maxStep, handling 360 wrap-around
func InterpolateAngle(current, target, maxStep float32) float32 {
	diff := target - current
	for diff < -180 {
		diff += 360
	}
	for diff > 180 {
		diff -= 360
	}
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	res := current + diff
	for res < 0 {
		res += 360
	}
	for res >= 360 {
		res -= 360
	}
	return res
}

// InterpolatePitch smoothly moves current pitch towards target by at most maxStep
func InterpolatePitch(current, target, maxStep float32) float32 {
	diff := target - current
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	res := current + diff
	if res > 90 {
		res = 90
	} else if res < -90 {
		res = -90
	}
	return res
}

// RecalculatePath computes the shortest path to targetPos using A* search.
func RecalculatePath(b *bot.Bot) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	start := pathfinder.Node{
		X: int32(math.Floor(float64(b.Pos.X()))),
		Y: int32(math.Floor(float64(b.Pos.Y() + 0.1))),
		Z: int32(math.Floor(float64(b.Pos.Z()))),
	}
	targetY := b.TargetPos.Y()
	target := pathfinder.Node{
		X: int32(math.Floor(float64(b.TargetPos.X()))),
		Y: int32(math.Floor(float64(targetY))),
		Z: int32(math.Floor(float64(b.TargetPos.Z()))),
	}

	b.Logger.Debug("recalculating path using A*",
		"start_x", start.X, "start_y", start.Y, "start_z", start.Z,
		"target_x", target.X, "target_y", target.Y, "target_z", target.Z,
		"movement_state", b.MovementState,
	)

	startRID, _ := b.WorldCache.GetBlockRID(start.X, start.Y, start.Z)
	startHeadRID, _ := b.WorldCache.GetBlockRID(start.X, start.Y+1, start.Z)
	startFloorRID, _ := b.WorldCache.GetBlockRID(start.X, start.Y-1, start.Z)
	targetRID, _ := b.WorldCache.GetBlockRID(target.X, target.Y, target.Z)
	targetHeadRID, _ := b.WorldCache.GetBlockRID(target.X, target.Y+1, target.Z)
	targetFloorRID, _ := b.WorldCache.GetBlockRID(target.X, target.Y-1, target.Z)

	startBlockLeg, _, _ := chunk.RuntimeIDToState(startRID)
	startBlockHead, _, _ := chunk.RuntimeIDToState(startHeadRID)
	startBlockFloor, _, _ := chunk.RuntimeIDToState(startFloorRID)
	targetBlockLeg, _, _ := chunk.RuntimeIDToState(targetRID)
	targetBlockHead, _, _ := chunk.RuntimeIDToState(targetHeadRID)
	targetBlockFloor, _, _ := chunk.RuntimeIDToState(targetFloorRID)

	b.Logger.Info("A* Path Nodes block debug",
		"start_leg", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockLeg, startRID, b.WorldCache.IsRIDSolid(startRID)),
		"start_head", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockHead, startHeadRID, b.WorldCache.IsRIDSolid(startHeadRID)),
		"start_floor", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockFloor, startFloorRID, b.WorldCache.IsRIDSolid(startFloorRID)),
		"target_leg", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockLeg, targetRID, b.WorldCache.IsRIDSolid(targetRID)),
		"target_head", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockHead, targetHeadRID, b.WorldCache.IsRIDSolid(targetHeadRID)),
		"target_floor", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockFloor, targetFloorRID, b.WorldCache.IsRIDSolid(targetFloorRID)),
	)

	path := pathfinder.FindPath(start, target, b.WorldModel)
	if len(path) > 0 {
		b.CurrentPath = path
		if len(path) > 1 {
			b.PathIndex = 1
		} else {
			b.PathIndex = 0
		}
		b.TicksStuck = 0
		b.LastTickPos = b.Pos
		b.LastPathRecalcTime = time.Now()
		b.ConsecutiveStuckCount = 0
		nodeCoords := make([]string, len(path))
		for i, n := range path {
			nodeCoords[i] = fmt.Sprintf("(%d,%d,%d)", n.X, n.Y, n.Z)
		}
		b.Logger.Info("A* pathfinding completed", "nodes", len(path), "path", strings.Join(nodeCoords, " -> "), "movement_state", b.MovementState)
	} else {
		b.CurrentPath = nil
		b.LastPathRecalcTime = time.Now()
		b.Logger.Warn("A* pathfinding failed to resolve walkable path to destination",
			"start", start, "target", target, "movement_state", b.MovementState)
	}
}

func NavigateTo(b *bot.Bot, pos mgl32.Vec3) {
	b.WalkTo(pos)
}

func StopMovement(b *bot.Bot) {
	b.Stop()
}

func NavigateToBlock(b *bot.Bot, x, y, z int32, tolerance float32) bool {
	target := mgl32.Vec3{float32(x) + 0.5, float32(y), float32(z) + 0.5}
	b.WalkTo(target)

	// Wait up to 5 seconds for bot to reach coordinate
	for i := 0; i < 25; i++ {
		time.Sleep(200 * time.Millisecond)
		b.Mu.Lock()
		curPos := b.Pos
		mState := b.MovementState
		b.Mu.Unlock()

		dx := curPos.X() - target.X()
		dy := curPos.Y() - target.Y()
		dz := curPos.Z() - target.Z()
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
		if dist <= tolerance {
			return true
		}
		if mState == "idle" {
			break
		}
	}
	return false
}

func LookAt(b *bot.Bot, pos mgl32.Vec3) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	dx := pos.X() - b.Pos.X()
	dy := pos.Y() - b.Pos.Y()
	dz := pos.Z() - b.Pos.Z()

	distH := math.Sqrt(float64(dx*dx + dz*dz))
	if distH < 0.001 {
		distH = 0.001
	}

	yawRad := math.Atan2(float64(dz), float64(dx))
	yawDeg := yawRad * 180 / math.Pi
	b.Yaw = float32(yawDeg) - 90
	if b.Yaw < 0 {
		b.Yaw += 360
	}

	pitchRad := -math.Atan2(float64(dy), distH)
	b.Pitch = float32(pitchRad * 180 / math.Pi)
}
