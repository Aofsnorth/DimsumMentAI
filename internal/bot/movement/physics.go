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
	tc.applyServerSupport()
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
//
// It must also cover the slower cadence: a Geyser minigame host echoes the
// idle body about once a second (measured live: corrections exactly 1.000s
// apart), and with a 400ms window the anchor expired 600ms before every
// echo — gravity won the gap, the body sank two blocks below the server's
// truth, and every position claim it made from down there was rejected and
// rubber-banded. A body that cannot hold its Y cannot hold anything else,
// knockback included.
const serverAnchorWindow = 1200 * time.Millisecond

// anchorFlightSkin is how far above the server-confirmed Y a body may sit
// before the anchor refuses to pin it down. Anything past this while not
// grounded is a jump apex or a knockback rise — flight, which has its own
// physics — while a body at or below the confirmed Y is the decode-gap sag
// the anchor was written to hold.
const anchorFlightSkin float32 = 0.2

// assignedRiseThreshold is the upward velocity above which a grounded body is
// treated as having been assigned flight by the server (a jump impulse or a
// hit hop) rather than standing. Below it the grounded branch keeps zeroing
// the residual jitter as always.
const assignedRiseThreshold float32 = 0.05

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
	// A body the server just set in motion — the hop a PvP hit carries — has
	// its own vertical arc to finish. Pinning it mid-rise cancels the hop the
	// server is simulating on its side, and the divergence comes back as a
	// correction that erases the whole knockback.
	if absFloat32(tc.VelY) > 0.05 {
		return
	}
	// The velocity guard above has one hole: the apex, where a rise crosses
	// zero. There the old behaviour yanked the body straight back down to the
	// confirmed Y mid-jump — the rise ended at the apex and the claim said
	// floor, which rendered as a fraction-block hop under heavy gravity.
	// Being ABOVE the confirmed Y is flight, not a decode gap; being BELOW it
	// is the sag this anchor exists for, and that direction keeps the pin.
	if tc.serverAnchorDelta() > anchorFlightSkin {
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
	if tc.floatingOverAir() {
		// Not a decode gap: the cells under the feet are loaded air with a floor
		// right below. The host echoes our own Y back, so holding it would keep
		// a body that landed a block high floating until it next walks.
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

// floatingOverAir reports (Geyser only) that every cell under the body's box is
// loaded, genuinely air, and the cell below that is solid. Partial floors (snow,
// slabs, carpet) are not air, so they keep using the server anchor.
func (tc *TickContext) floatingOverAir() bool {
	wc := tc.B.WorldCache
	if wc == nil || !wc.GeyserCompat() {
		return false
	}
	const half = 0.299
	x, z := tc.CurrPos.X(), tc.CurrPos.Z()
	cy := int32(math.Floor(float64(tc.CurrPos.Y() - 0.001)))
	minX, maxX := int32(math.Floor(float64(x-half))), int32(math.Floor(float64(x+half)))
	minZ, maxZ := int32(math.Floor(float64(z-half))), int32(math.Floor(float64(z+half)))
	for bx := minX; bx <= maxX; bx++ {
		for bz := minZ; bz <= maxZ; bz++ {
			rid, loaded := wc.GetBlockRID(bx, cy, bz)
			if !loaded {
				return false
			}
			if name, ok := wc.BlockName(rid); !ok || name != "minecraft:air" {
				return false
			}
		}
	}
	cx, cz := int32(math.Floor(float64(x))), int32(math.Floor(float64(z)))
	rid, loaded := wc.GetBlockRID(cx, cy-1, cz)
	return loaded && wc.IsRIDSolid(rid)
}

// serverSupportWindow is how long a server push-up keeps the body standing.
const serverSupportWindow = 350 * time.Millisecond

// applyServerSupport treats the body as standing when the host just refused its
// fall by lifting it a little.
//
// The local world model sees a stair as a full cube and a slab edge as air, so
// a body overlapping the lower step of a stair (or a slab's rim) reads as
// airborne, sinks into it, and the host snaps it back up. Every snap also
// reset the horizontal position, so the bot never got off the block: it stood
// still with walk inputs on the wire. While the host keeps holding the feet at
// a height, trust it and keep walking; the window is short, so a body that
// walks off the edge starts falling again almost at once.
func (tc *TickContext) applyServerSupport() {
	if tc.IsGrounded || tc.IsOnLadder || tc.IsParkourJump || tc.VelY > risingSkinLimit {
		return
	}
	tc.B.Mu.Lock()
	sy, at := tc.B.ServerSupportY, tc.B.ServerSupportAt
	tc.B.Mu.Unlock()
	if at.IsZero() || time.Since(at) > serverSupportWindow {
		return
	}
	if absFloat32(tc.CurrPos.Y()-sy) > 0.02 {
		return
	}
	tc.NextY = sy
	tc.VelY = 0
	tc.IsGrounded = true
}

// holdServerSupport reports whether the host recently lifted the body to its
// current height, and renews that claim while the body's box still overlaps a
// solid block under it. The renewal stops the moment the box clears the block,
// so a body that walks off the edge starts falling again at once.
func (tc *TickContext) holdServerSupport() bool {
	tc.B.Mu.Lock()
	sy, at := tc.B.ServerSupportY, tc.B.ServerSupportAt
	tc.B.Mu.Unlock()
	if at.IsZero() || time.Since(at) > serverSupportWindow {
		return false
	}
	if absFloat32(tc.CurrPos.Y()-sy) > 0.02 {
		return false
	}
	if tc.bodyOverlapsSupportAt(tc.CurrPos.X(), tc.CurrPos.Z(), int32(math.Floor(float64(sy)))) {
		tc.B.Mu.Lock()
		tc.B.ServerSupportAt = time.Now()
		tc.B.Mu.Unlock()
	}
	return true
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
	if tc.HasPath {
		tc.B.Mu.Lock()
		if tc.B.PathIndex < len(tc.B.CurrentPath) {
			nextNode := tc.B.CurrentPath[tc.B.PathIndex]
			if float32(nextNode.Y) < tc.CurrPos.Y()-0.05 {
				tc.IsDescending = true
			}
		}
		tc.B.Mu.Unlock()
	}
	if !tc.IsDescending && tc.B != nil && tc.B.WorldModel != nil {
		// Probe below the resting skin, not 1cm under the feet: a body resting
		// a little above its surface (echo residue) probed into its own air
		// cell, found solid one block down, and read as descending while it
		// was standing — which slowed it to descent speed and widened the
		// grounded window at the wrong moment.
		footCellX := int32(math.Floor(float64(tc.CurrPos.X())))
		footCellY := int32(math.Floor(float64(tc.CurrPos.Y() - groundRestSkin)))
		footCellZ := int32(math.Floor(float64(tc.CurrPos.Z())))
		if !tc.B.WorldModel.IsSolid(footCellX, footCellY, footCellZ) {
			for drop := int32(1); drop <= 3; drop++ {
				if tc.B.WorldModel.IsSolid(footCellX, footCellY-drop, footCellZ) {
					tc.IsDescending = true
					break
				}
			}
		}
	}
}

// groundCellBelow converts a sampled foot position into the one cell that can
// possibly be a floor: the cell containing the point 1cm under the feet.
//
// That is necessary but not sufficient. With the body leaning on a wall the
// foot-offset sweep can still touch the wall cell at foot level: the 1cm dip
// keeps the probe inside the wall band and the wall gets voted as a floor.
// groundedCell adds the real test -- the cell below the feet must carry the
// body on TOP of it, i.e. the feet must rest within a thin skin above its
// top face, exactly what a grounded body looks like and what a body floating
// past a wall never does.
func groundCellBelow(x, z float32, feetY float32) (int32, int32, int32) {
	return int32(math.Floor(float64(x))),
		int32(math.Floor(float64(feetY - 0.01))),
		int32(math.Floor(float64(z)))
}

// groundSkin is how far above a block's top face the feet may sit and still
// count as resting on it: 2cm absorbs float error and sub-block positioning,
// and is far too thin for a body flying past to ever fall into. The tests
// push 67.02 against a top face at 67.0, which lands exactly on the boundary
// in float32, so the upper comparison carries an epsilon of its own rather
// than pretending arithmetic is exact.
const groundSkin = 0.02

const groundSkinEpsilon = 1e-4

// groundRestSkin is the wider window a SETTLED body gets: how far above a
// block's top face the feet may hover and still count as standing on it.
//
// A body can come to rest slightly above the surface it stands on — sub-block
// residue from a mid-stride server echo, a step-down assist that stopped
// 0.14 high, or a correction that planted the feet 10cm into the floor. With
// only the 2cm skin that body read as airborne, ApplyVerticalVelocity cleared
// every jump request each tick, and a body facing a one-block step could
// never hop onto it: checkWallCollision (which only ignores the step row for
// a grounded body) cancelled the horizontal move, and the bot froze pressed
// against the ledge forever — the permanent stall at a step_jump node.
const groundRestSkin = 0.15

// The resting window applies only to a body that is settling or settled.
// A rising body is mid-jump and must keep the tight skin, or a ledge beside
// the arc reads as ground and truncates the jump (the mid-air double-jump
// bug); a fast-falling body has the swept landing in ApplyGroundLanding and
// must not stick to a block edge it is falling past.
const (
	risingSkinLimit   = 0.05
	settlingSkinFloor = -0.25
)

// groundSkinFor picks the grounding window for this tick's velocity.
func (tc *TickContext) groundSkinFor() float32 {
	if tc.VelY > risingSkinLimit || tc.VelY < settlingSkinFloor {
		return groundSkin
	}
	return groundRestSkin
}

// groundedCell reports the cell the body is standing on, or false. A solid
// cell only votes when the feet rest on its top face within the skin. The
// probe cell and the cell below it are both candidates: a body hovering 2cm
// above a block's top face probes into the empty cell above (67.02 dips to
// 67.01, which floors to 67, while the floor it rests on is 66), so checking
// only the probe cell would call a genuinely resting body airborne. The
// top-face window is what stops a wall band from voting, not the cell choice.
func (tc *TickContext) groundedCell(dxOffset, dzOffset, feetY float32) (int32, bool) {
	if tc.B == nil || tc.B.WorldModel == nil {
		return 0, false
	}
	skin := tc.groundSkinFor()
	cx, cy, cz := groundCellBelow(tc.CurrPos.X()+dxOffset, tc.CurrPos.Z()+dzOffset, feetY)
	for _, vote := range []int32{cy, cy - 1} {
		if !tc.B.WorldModel.IsSolid(cx, vote, cz) {
			continue
		}
		top := tc.surfaceTopY(cx, vote, cz)
		if feetY >= top-groundSkinEpsilon-skin && feetY <= top+skin+groundSkinEpsilon {
			return vote, true
		}
	}
	return cy, false
}

// surfaceTopY returns the height of the walkable surface of the block in cell
// (x, y, z): the cell's top face for a full block, but only 0.5 for a bottom
// slab and 0.15 for a snow layer.
//
// Grounding against cell+1 unconditionally put the feet half a block BELOW the
// real surface of a slab, so the body never read as grounded: gravity ran
// every tick, the wall scan read the slab as a wall in front of the feet, and
// the bot stood pressed against a slab path forever.
func (tc *TickContext) surfaceTopY(x, y, z int32) float32 {
	if tc.B != nil && tc.B.WorldCache != nil {
		return float32(y) + tc.B.WorldCache.TopOffsetAt(x, y, z)
	}
	return float32(y + 1)
}

func (tc *TickContext) UpdateGroundedState() {
	checkOffsets := groundCheckOffsets(tc.IsDescending, tc.IsParkourJump)

	for _, dxOffset := range checkOffsets {
		for _, dzOffset := range checkOffsets {
			if _, ok := tc.groundedCell(dxOffset, dzOffset, tc.CurrPos.Y()); ok {
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
	// A request is one tick of impulse, never a held key. Consume it in both
	// branches, because a tick that fires it owes gravity the payback and a
	// tick that refuses it must not leave it armed for a second impulse on the
	// rising body -- what the player sees as jumping again off the sky.
	if tc.IsGrounded {
		if tc.VelY > assignedRiseThreshold {
			// The server assigned this body an upward motion — the hop a PvP hit
			// carries in SetActorMotion. The model's floor vote (the ground is
			// right below) cannot cancel an assignment: zeroing the rise here is
			// exactly why the knockback slide happened while the hop never did —
			// the body glided flat instead of flying.
			tc.IsGrounded = false
		} else {
			tc.VelY = 0.0
			if tc.ShouldJump {
				tc.B.Logger.Debug("jump triggered", "reason", tc.JumpReason, "dist", tc.Dist, "pos", tc.CurrPos, "mState", tc.MState)
				tc.VelY = 0.42
				tc.IsGrounded = false
			}
		}
	} else {
		// When airborne, ShouldJump must not apply any upward impulse, and
		// must be cleared so the packet does not claim a jump mid-air.
		tc.ShouldJump = false
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

	// The predicted height is derived from the velocity on every path through
	// this function, including the two that return early. It used to be
	// assigned only on the "already at the target" branch, so a climb — which
	// is the only case a ladder exists for — left it at whatever the struct
	// was built with. The struct is built fresh every tick and never set it, so
	// it was zero: the bot's predicted position came out at Y=0 on every
	// ladder, the server rejected the move, and the correction that followed
	// discarded the path it was climbing. Every ladder went nowhere.
	target := tc.CurrPos.Y()
	settled := false
	if tc.HasPath {
		tc.B.Mu.Lock()
		if tc.B.PathIndex < len(tc.B.CurrentPath) {
			nextNode := tc.B.CurrentPath[tc.B.PathIndex]
			nodeY := float32(nextNode.Y)
			actualY := tc.CurrPos.Y()
			switch {
			case actualY < nodeY-0.15:
				tc.VelY = 0.2
			case actualY > nodeY+0.15:
				tc.VelY = -0.2
			default:
				// Close enough to call the rung arrived: snap to the node's
				// height rather than letting 0.08 of gravity creep in and
				// re-trigger the same comparison next tick.
				target = nodeY
				settled = true
			}
		}
		tc.B.Mu.Unlock()
	}
	if !settled {
		target = tc.CurrPos.Y() + tc.VelY
	}
	tc.NextY = target
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

	// Resting catch: a body hovering just above the surface it stands on
	// settles onto it instead of hovering outside the skin. Left alone, that
	// residue keeps UpdateGroundedState reporting airborne and the frozen
	// step_jump stall follows. Only the centre probe votes, so a block beside
	// the feet can never pull the body onto itself.
	if cell, ok := tc.groundedCell(0, 0, tc.CurrPos.Y()); ok {
		top := tc.surfaceTopY(int32(math.Floor(float64(tc.CurrPos.X()))), cell, int32(math.Floor(float64(tc.CurrPos.Z()))))
		// A carpet on the floor raises the surface the host collides with. Stay
		// on top of it while the box overlaps it; once clear, the body sinks
		// back to the floor.
		if ct, okc := tc.carpetSurface(tc.CurrPos.X(), tc.CurrPos.Z(), tc.CurrPos.Y()); okc && ct > top && tc.CurrPos.Y() >= ct-0.05 {
			top = ct
		}
		if tc.CurrPos.Y() >= top-0.05 {
			if tc.CurrPos.Y() > top+groundSkin && tc.holdServerSupport() {
				// The host holds the feet above the modelled floor (a carpet on
				// it): snapping down to the model's top is refused every tick.
				tc.NextY = tc.CurrPos.Y()
				tc.VelY = 0.0
				tc.IsGrounded = true
				return
			}
			tc.NextY = top
			tc.VelY = 0.0
			tc.IsGrounded = true
			return
		}
	}

	checkOffsets := groundCheckOffsets(tc.IsDescending, tc.IsParkourJump)
	hasGroundBelow := false
	var landingTop float32
	var landingCy int32 = -999

	for _, dxOffset := range checkOffsets {
		for _, dzOffset := range checkOffsets {
			cx := int32(math.Floor(float64(tc.CurrPos.X() + dxOffset)))
			cz := int32(math.Floor(float64(tc.CurrPos.Z() + dzOffset)))

			// Sweep the vertical block column from where the feet started this
			// tick down to where velocity would carry them.
			startCy := int32(math.Floor(float64(tc.CurrPos.Y())))
			endCy := int32(math.Floor(float64(tc.NextY - 0.01)))
			for cy := startCy; cy >= endCy; cy-- {
				if !tc.B.WorldModel.IsSolid(cx, cy, cz) {
					continue
				}
				top := tc.surfaceTopY(cx, cy, cz)
				// The body must have started at or above this block's top face,
				// and its descent must cross or reach this top face. A wall
				// beside the body (whose top is above CurrPos.Y) can never match.
				if tc.CurrPos.Y() >= top-0.05 && tc.NextY <= top {
					if !hasGroundBelow || cy > landingCy {
						hasGroundBelow = true
						landingCy = cy
						landingTop = top
					}
				}
			}
		}
	}

	if hasGroundBelow {
		tc.NextY = landingTop
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
var groundCheckOffsetsDefault = []float32{0.0, -0.3, 0.3}

// groundCheckOffsets returns the footprint samples of the body. They are the same
// while descending: a body walking off a ledge stays supported until its whole
// 0.6-wide box has cleared the edge (centre 0.3 past it), exactly the rule the
// host applies. Sampling only the centre while descending made the bot sink the
// moment its centre crossed the edge, the host refused that position (the box
// still overlapped the ledge) and snapped the bot back every tick, forever.
func groundCheckOffsets(isDescending, isParkourJump bool) []float32 {
	return groundCheckOffsetsDefault
}

func parkourWindowActive(until time.Time) bool {
	return !until.IsZero() && time.Now().Before(until)
}

func (tc *TickContext) applyStepDownAssist(feetY int32) {
	if !tc.IsDescending || tc.IsOnLadder {
		return
	}
	// IsDescending fires for every body with ground 1-3 blocks below its feet —
	// which includes the apex of a jump, the rise of a knockback hop, and any
	// real free fall. This assist drives a fixed -0.16 descent and FORCE-SETS
	// VelY to match, so applying it to those bodies reversed rises mid-air
	// (the quarter-height jump, the flat knockback hop) and held falls at a
	// constant crawl (the slow-motion drop). A body still rising, or already
	// falling faster than this assist's own step, belongs to its own arc and
	// to gravity — only the slow walk off a ledge is a step.
	if tc.VelY > 0.05 || tc.VelY < -0.3 {
		return
	}
	var targetY float32
	if tc.HasPath {
		tc.B.Mu.Lock()
		if tc.B.PathIndex < len(tc.B.CurrentPath) {
			nextNode := tc.B.CurrentPath[tc.B.PathIndex]
			targetY = float32(nextNode.Y)
		}
		tc.B.Mu.Unlock()
	}
	if targetY == 0 && tc.B != nil && tc.B.WorldModel != nil {
		footCellX := int32(math.Floor(float64(tc.CurrPos.X())))
		footCellY := int32(math.Floor(float64(tc.CurrPos.Y() - 0.01)))
		footCellZ := int32(math.Floor(float64(tc.CurrPos.Z())))
		for drop := int32(1); drop <= 3; drop++ {
			if tc.B.WorldModel.IsSolid(footCellX, footCellY-drop, footCellZ) {
				targetY = float32(footCellY - drop + 1)
				break
			}
		}
	}
	if targetY == 0 {
		return
	}

	if targetY >= tc.CurrPos.Y()-0.05 || tc.CurrPos.Y()-targetY > 3.5 {
		return
	}
	if tc.HasPath && tc.Dist > 1.4 {
		return
	}
	// Still resting on the ledge being left (box overlaps it): the host keeps
	// that body up, so lowering it would be refused and snapped back.
	if tc.IsGrounded && tc.CurrPos.Y()-targetY > 0.3 {
		return
	}
	if tc.CurrPos.Y() > targetY {
		nextY := tc.CurrPos.Y() - 0.16
		if nextY < targetY {
			nextY = targetY
		}
		tc.NextY = nextY
		tc.VelY = tc.NextY - tc.CurrPos.Y()
		tc.IsGrounded = (tc.NextY <= targetY)
	}
}
