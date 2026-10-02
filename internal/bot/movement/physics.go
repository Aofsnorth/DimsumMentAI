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
	tc.UpdateGroundedState()
	tc.ApplyServerPositionAnchor()
	tc.ApplyVerticalVelocity()
	tc.applyStepDownAssist(tc.FeetY)
	tc.applyCeilingCollision()
	tc.ApplyGroundLanding()
	tc.syncGrounded()
}

// serverAnchorWindow is how long a server-confirmed Y stays authoritative. The
// single-player host corrects position roughly every 6 ticks, so this covers
// several corrections while still expiring quickly if the bot is in genuine
// mid-air after a jump.
const serverAnchorWindow = 400 * time.Millisecond

// ApplyServerPositionAnchor holds a stationary bot at the last Y the server
// confirmed, instead of letting gravity pull it down when the local world model
// has no floor decoded beneath it.
//
// The bug this fixes: `UpdateGroundedState` only reports grounded when the local
// world model says the block below is solid. On a freshly joined LAN world that
// block is frequently not decoded yet, so IsGrounded stayed false, gravity
// accumulated every tick, and the bot fell — then the server snapped it back on
// the next CorrectPlayerMovePrediction. That fall/snap cycle moved the body by
// over a block, which the look code turned into a visible head tremor and an
// upward aim bias whenever the bot was watching a nearby player.
//
// The anchor deliberately does nothing while the bot is moving, jumping or on a
// ladder: those states have their own vertical model and must not be pinned.
func (tc *TickContext) ApplyServerPositionAnchor() {
	if tc.IsGrounded || tc.IsOnLadder || tc.ShouldJump || tc.IsParkourJump {
		return
	}
	if tc.HasHorizontalMove || tc.HasPath {
		return
	}
	if !tc.hasFreshServerAnchor() {
		return
	}
	if absFloat32(tc.serverAnchorDelta()) > anchorCorrectionLimit {
		// Too far from the server's Y in either direction to be a decoding gap;
		// this is a real displacement (respawn/teleport) and must be handled by
		// the normal movement code rather than silently snapped.
		return
	}

	tc.NextY = tc.serverAnchorY()
	tc.VelY = 0
	tc.IsGrounded = true

	// Self-renew the anchor. The server simulates our input and corrects any
	// position it disagrees with, so silence while we hold its last confirmed Y
	// means it still agrees. Without the renewal the anchor expired every
	// 400 ms, the bot sagged until the next correction snapped it back, and
	// that fall/snap cycle repeated forever — a continuous body bob that read
	// as the head trembling up and down while the bot tracked a player.
	// Partial-height floors (snow layers, slabs) trigger exactly this: the
	// local world model never classifies them as solid, so gravity always wins
	// locally and only the server's correction holds the bot up.
	tc.B.Mu.Lock()
	tc.B.ServerGroundAt = time.Now()
	tc.B.Mu.Unlock()
}

// anchorCorrectionLimit is the largest Y gap the anchor will silently correct.
// Anything larger means the bot really moved (teleport, respawn) and the
// discrepancy is meaningful data, not a decode gap.
const anchorCorrectionLimit = 1.5

// hasFreshServerAnchor reports whether the server confirmed a position recently
// enough to trust.
func (tc *TickContext) hasFreshServerAnchor() bool {
	tc.B.Mu.Lock()
	at := tc.B.ServerGroundAt
	tc.B.Mu.Unlock()
	if at.IsZero() {
		return false
	}
	return time.Since(at) <= serverAnchorWindow
}

// serverAnchorDelta is the signed distance from the bot's simulated feet to the
// server-confirmed Y.
func (tc *TickContext) serverAnchorDelta() float32 {
	return tc.CurrPos.Y() - tc.serverAnchorY()
}

// serverAnchorY returns the last Y the server confirmed for this bot.
func (tc *TickContext) serverAnchorY() float32 {
	tc.B.Mu.Lock()
	anchorY := tc.B.ServerGroundY
	tc.B.Mu.Unlock()
	return anchorY
}

func absFloat32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
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

func (tc *TickContext) UpdateGroundedState() {
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

func (tc *TickContext) ApplyVerticalVelocity() {
	if tc.IsOnLadder {
		tc.applyLadderVerticalVelocity()
		return
	}
	if tc.applySwimVerticalVelocity() {
		return
	}
	if tc.IsGrounded {
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

// applySwimVerticalVelocity replaces gravity with the swim drive while the body
// is in water, and reports whether it did.
//
// The gravity branch above is unconditional: every ungrounded tick it subtracts
// 0.08 and clamps at -3.92, which is right on land and actively wrong in a river.
// A submerged body accumulated terminal-velocity downward every tick and fought
// the swim flags the input packet was sending, so the bot sank while the server
// was being told to rise.
//
// The drive is the plan's own Vertical axis rather than a second opinion about
// where the body should go, so the packet and the physics cannot disagree. The
// surface bias is not added here: PlanSwim already puts it in Vertical for a
// floating body, and adding it twice would lift a body out of the water it is
// supposed to be floating in.
func (tc *TickContext) applySwimVerticalVelocity() bool {
	if !tc.SwimPlanned || !tc.SwimIntent.InWater {
		return false
	}

	tc.VelY = tc.SwimIntent.Vertical * swimVerticalDrive
	tc.NextY = tc.CurrPos.Y() + tc.VelY
	return true
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

func (tc *TickContext) ApplyGroundLanding() {
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

// Ground-check sample offsets are shared, not rebuilt per call. The callers only
// ever read them, and this is on the movement tick's hot path: four probes per
// call, several calls a tick, and a fresh slice each time is pure garbage.
var (
	groundCheckOffsetsNarrow  = []float32{0.0}
	groundCheckOffsetsDefault = []float32{0.0, -0.3, 0.3}
)

func groundCheckOffsets(isDescending, isParkourJump bool) []float32 {
	if isDescending {
		return groundCheckOffsetsNarrow
	}
	return groundCheckOffsetsDefault
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
