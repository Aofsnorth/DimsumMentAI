package entity

import "math"

// Drop geometry tuning. These describe how a bot should orient itself to toss
// an item so it lands within a recipient's pickup radius. They are expressed as
// Bedrock look angles (yaw degrees, pitch degrees where negative looks up).
const (
	// yawOffsetDegrees converts an atan2 world angle into Bedrock body yaw.
	yawOffsetDegrees = 90.0
	// fullCircleDegrees normalizes yaw into [0,360).
	fullCircleDegrees = 360.0
	// dropEpsilon is the minimum horizontal separation before direction math
	// is meaningful.
	dropEpsilon = 0.001

	// DropPitchNear is the flatter upward pitch used when the recipient is
	// close, so the tossed item lands directly in their pickup radius.
	DropPitchNear = -22.0
	// DropPitchFar is the higher upward pitch used at the edge of range so the
	// item has enough arc to reach the recipient.
	DropPitchFar = -30.0
	// DropPitchCliffSafe is a gentle, nearly flat toss used when the ground
	// beyond the recipient falls away, keeping the item from sailing into a gap.
	DropPitchCliffSafe = -6.0
	// DropAimMaxDistance bounds the horizontal distance (blocks) used when
	// interpolating toss pitch between near and far.
	DropAimMaxDistance = 4.0
	// DropLandingProbeDepth is how many blocks below the landing cell are
	// probed for solid ground when deciding whether a toss is cliff-safe.
	DropLandingProbeDepth = 3
	// DropStandoffDistance is the horizontal distance (blocks) the bot should
	// stand from a recipient before tossing. It is deliberately kept a little
	// farther than melee range so the tossed item lands in the recipient's
	// pickup radius WITHOUT the bot re-collecting it — this replaces the old
	// post-drop backstep. A Bedrock item toss has a small initial velocity and
	// short reach, so a consistent standoff is what makes the arc land reliably.
	DropStandoffDistance = 2.75

	// --- Natural variation --------------------------------------------------
	// Real players don't stop at a millimetre-perfect distance or aim with a
	// fixed pitch every time. These bounds add small, bounded randomness so the
	// give/drop reads as human without hurting reliability.

	// DropCloseApproachChance is the probability the bot walks right up to the
	// recipient and tosses from point-blank with a slight downward tuck, rather
	// than tossing from the normal standoff.
	DropCloseApproachChance = 0.3
	// DropCloseStandoffMin/Max bound the point-blank approach distance.
	DropCloseStandoffMin = 0.9
	DropCloseStandoffMax = 1.4
	// DropStandoffJitterMin/Max bound the random offset added to the normal
	// standoff distance so the bot doesn't always stop on the exact same spot.
	DropStandoffJitterMin = -0.4
	DropStandoffJitterMax = 0.6
	// DropCloseDistance is the distance at or below which a toss is treated as
	// point-blank: instead of an upward arc the bot tucks its head slightly
	// down so the item drops right at the recipient's feet rather than sailing
	// past them.
	DropCloseDistance = 1.6
	// DropPitchClose is the slight downward pitch (positive = looking down) used
	// for a point-blank toss.
	DropPitchClose = 6.0
	// DropPitchJitterRange bounds the random pitch wobble (± degrees) added to
	// every toss so the aim is never mathematically identical.
	DropPitchJitterRange = 2.5
)

// GroundReader reports whether a world cell is solid, used to decide whether a
// tossed item would land on stable ground or sail into a gap.
type GroundReader interface {
	IsSolid(x, y, z int32) bool
}

// DropAim describes the yaw/pitch a bot should face to toss an item toward a
// recipient, computed from current (post-navigation) positions.
type DropAim struct {
	Yaw   float32
	Pitch float32
}

func dropHorizontalDistance(from, to [3]float32) float32 {
	dx := to[0] - from[0]
	dz := to[2] - from[2]
	return float32(math.Sqrt(float64(dx*dx + dz*dz)))
}

// dropYaw returns the Bedrock body yaw (degrees, normalized to [0,360)) facing
// from the bot toward the target on the horizontal plane.
func dropYaw(botPos, targetPos [3]float32) float32 {
	dx := targetPos[0] - botPos[0]
	dz := targetPos[2] - botPos[2]
	yaw := float32(math.Atan2(float64(dz), float64(dx))*(180.0/math.Pi)) - yawOffsetDegrees
	for yaw < 0 {
		yaw += fullCircleDegrees
	}
	for yaw >= fullCircleDegrees {
		yaw -= fullCircleDegrees
	}
	return yaw
}

// dropPitchForDistance interpolates the upward toss pitch between the near and
// far presets based on recipient distance, clamped to the aim range.
func dropPitchForDistance(dist float32) float32 {
	if dist <= 0 {
		return DropPitchNear
	}
	if dist >= DropAimMaxDistance {
		return DropPitchFar
	}
	t := dist / DropAimMaxDistance
	return DropPitchNear + (DropPitchFar-DropPitchNear)*t
}

// landingCellHasGround reports whether the cell one block beyond the bot toward
// the target has solid ground within the probe depth. Unknown/air columns are
// treated as unsafe (cliff/gap).
func landingCellHasGround(world GroundReader, botPos, targetPos [3]float32) bool {
	if world == nil {
		return false
	}
	dist := dropHorizontalDistance(botPos, targetPos)
	if dist < dropEpsilon {
		return true
	}
	dx := (targetPos[0] - botPos[0]) / dist
	dz := (targetPos[2] - botPos[2]) / dist
	landingX := int32(math.Floor(float64(botPos[0] + dx)))
	landingZ := int32(math.Floor(float64(botPos[2] + dz)))
	feetY := int32(math.Floor(float64(botPos[1])))
	for depth := int32(0); depth <= DropLandingProbeDepth; depth++ {
		if world.IsSolid(landingX, feetY-1-depth, landingZ) {
			return true
		}
	}
	return false
}

// ComputeDropAim returns the yaw/pitch the bot should face to toss an item so
// it reaches the recipient. When the ground beyond the recipient falls away it
// uses a gentle, short toss so the item can't be lost over a ledge. Both
// positions must be current (refreshed after any navigation).
func ComputeDropAim(world GroundReader, botPos, targetPos [3]float32) DropAim {
	return ComputeDropAimWithJitter(world, botPos, targetPos, 0)
}

// ComputeDropAimWithJitter is ComputeDropAim plus a caller-supplied pitch
// wobble in [-1,1] that is scaled to DropPitchJitterRange, so the aim is never
// mathematically identical between tosses. It is pure: pass a random roll from
// the caller to get natural variation, or 0 for a deterministic result.
//
// At point-blank range (distance <= DropCloseDistance) the bot tucks its head
// slightly DOWN (DropPitchClose) instead of arcing up, so the item drops right
// at the recipient's feet rather than sailing past them.
func ComputeDropAimWithJitter(world GroundReader, botPos, targetPos [3]float32, pitchRoll float32) DropAim {
	yaw := dropYaw(botPos, targetPos)
	dist := dropHorizontalDistance(botPos, targetPos)

	var pitch float32
	switch {
	case !landingCellHasGround(world, botPos, targetPos):
		pitch = DropPitchCliffSafe
	case dist <= DropCloseDistance:
		pitch = DropPitchClose
	default:
		pitch = dropPitchForDistance(dist)
	}

	pitch += clampUnit(pitchRoll) * DropPitchJitterRange
	return DropAim{Yaw: yaw, Pitch: pitch}
}

// clampUnit clamps a value to [-1,1].
func clampUnit(v float32) float32 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

// DropStandoffTarget returns the horizontal position the bot should walk to
// before tossing: a point DropStandoffDistance blocks from the recipient along
// the current bot->recipient line. Standing at a consistent close distance is
// what makes the toss arc land in the recipient's pickup radius reliably. The Y
// component mirrors the recipient's feet so callers can floor it for a block
// navigation target. When the bot is already effectively on top of the
// recipient the recipient position is returned unchanged.
func DropStandoffTarget(botPos, targetPos [3]float32) [3]float32 {
	return dropStandoffAt(botPos, targetPos, DropStandoffDistance)
}

// DropStandoffTargetNatural is DropStandoffTarget with human-like variation
// driven by two caller-supplied rolls in [0,1):
//
//   - approachRoll < DropCloseApproachChance: the bot walks right up to the
//     recipient (point-blank, DropCloseStandoffMin..Max) and will toss with a
//     slight downward tuck (see ComputeDropAimWithJitter).
//   - otherwise: the normal standoff distance is used with a small random
//     jitter (DropStandoffJitterMin..Max) so the bot never stops on the exact
//     same spot twice.
//
// It is pure; pass random rolls from the caller for natural behaviour, or fixed
// rolls in tests for determinism.
func DropStandoffTargetNatural(botPos, targetPos [3]float32, approachRoll, distanceRoll float32) [3]float32 {
	var standoff float32
	if approachRoll < DropCloseApproachChance {
		standoff = lerpRoll(DropCloseStandoffMin, DropCloseStandoffMax, distanceRoll)
	} else {
		standoff = DropStandoffDistance + lerpRoll(DropStandoffJitterMin, DropStandoffJitterMax, distanceRoll)
	}
	return dropStandoffAt(botPos, targetPos, standoff)
}

// dropStandoffAt returns a point `standoff` blocks from targetPos along the
// bot->target line (on the bot's side).
func dropStandoffAt(botPos, targetPos [3]float32, standoff float32) [3]float32 {
	dx := botPos[0] - targetPos[0]
	dz := botPos[2] - targetPos[2]
	dist := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if dist < dropEpsilon {
		return targetPos
	}
	ux := dx / dist
	uz := dz / dist
	return [3]float32{
		targetPos[0] + ux*standoff,
		targetPos[1],
		targetPos[2] + uz*standoff,
	}
}

// lerpRoll maps a roll in [0,1) to the inclusive range [lo,hi].
func lerpRoll(lo, hi, roll float32) float32 {
	return lo + (hi-lo)*clamp01(roll)
}

// clamp01 clamps a value to [0,1].
func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
