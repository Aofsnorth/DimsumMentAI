// Per-mob tactics: how to move against a particular hostile, separately from
// the swing.
//
// The generic loop closes on everything, which is exactly the wrong thing for
// the three mobs that punish it: a creeper explodes at point-blank, a skeleton
// wins any straight-line charge, and an enderman takes a stare at its face as
// a declaration of war. The decisions are keyed by the normalized mob name and
// live over plain data for the same reason the weapon choice does: a tactic
// that can only be tested by getting blown up is a tactic that never gets
// tested.

package combat

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// Distances at which the tactics change, in blocks.
const (
	// creeperPanicDistance is how close a creeper may get before the bot runs.
	// Its blast reaches roughly three blocks, so this sits one block outside
	// the blast: close enough that the bot fights a creeper normally until the
	// moment it must not.
	creeperPanicDistance float32 = 3.0
	// creeperSafeDistance is where fleeing stops. Past it the explosion cannot
	// reach even if the creeper detonates at the moment the bot turns around.
	creeperSafeDistance float32 = 6.0
	// skeletonBandMin and skeletonBandMax are the distance band the bot keeps
	// against a skeleton. Inside the band the bot strafes sideways instead of
	// closing: a straight run at a skeleton is an arrow to the face, while
	// lateral motion forces it to keep re-aiming.
	skeletonBandMin float32 = 7.0
	skeletonBandMax float32 = 12.0
)

// LookHeight is where the bot aims on the target's body.
type LookHeight int

const (
	// LookCenter aims at the torso, the default for everything.
	LookCenter LookHeight = iota
	// LookFeet aims at the base of the body. Endermen read eye contact as a
	// challenge, so the bot looks at their feet while still tracking them.
	LookFeet
)

// movePlan is what to do about the target this tick: how to move relative to
// it and where to aim.
type movePlan struct {
	// flee ends the fight and runs to safeDistance from the target.
	flee         bool
	safeDistance float32
	// strafe keeps the current distance while moving sideways around the
	// target, with direction flipped every so often so the motion is not a
	// predictable circle.
	strafe bool
	// bandMin and bandMax are the distance limits to hold. Outside them the
	// bot closes or backs away along the line to the target instead of
	// strafing. Zero means no band: just close on the target.
	bandMin float32
	bandMax float32
	look    LookHeight
}

// mobMovePlan returns the tactic for a normalized mob name at a horizontal
// distance. Unknown mobs get the default melee answer: close on the target.
func mobMovePlan(mob string, dist float32) movePlan {
	switch mob {
	case "creeper":
		return creeperTactic(dist)
	case "skeleton", "stray":
		return skeletonTactic(dist)
	case "enderman":
		return endermanTactic(dist)
	default:
		return movePlan{}
	}
}

// creeperTactic fights normally until the creeper is inside blast range and
// then runs. The fuse is not readable from the tracked entity data, so
// closeness stands in for primed: a creeper that close is one that is about
// to detonate either way.
func creeperTactic(dist float32) movePlan {
	if dist < creeperPanicDistance {
		return movePlan{flee: true, safeDistance: creeperSafeDistance}
	}
	return movePlan{}
}

// skeletonTactic keeps the distance band and strafes inside it. Outside the
// band the bot moves along the line — closing from far, backing off when the
// skeleton pushed inside the band — but never charges straight through it.
func skeletonTactic(dist float32) movePlan {
	return movePlan{strafe: true, bandMin: skeletonBandMin, bandMax: skeletonBandMax}
}

// endermanTactic keeps the aim on the feet and never on the head, so tracking
// the target does not aggro it. In the fight itself an enderman is a charger,
// so the bot meets it at melee range once it closes.
func endermanTactic(dist float32) movePlan {
	return movePlan{look: LookFeet}
}

// retreatPoint is the point on the line away from threat, safeDistance away
// from it. Height is kept: fleeing up or down a slope is the pathfinder's
// problem, not the tactic's.
func retreatPoint(botPos, threat mgl32.Vec3, safeDistance float32) mgl32.Vec3 {
	dx, dz := botPos.X()-threat.X(), botPos.Z()-threat.Z()
	d := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if d < 1e-4 {
		// Standing exactly on the threat: any horizontal direction is equally
		// away, so pick one rather than standing still.
		return threat.Add(mgl32.Vec3{safeDistance, 0, 0})
	}
	scale := safeDistance / d
	return threat.Add(mgl32.Vec3{dx * scale, 0, dz * scale})
}

// strafePoint is a point on the circle of radius dist around target, a quarter
// turn from the bot's current bearing. sign picks the side, and flipping it
// over time is what makes the strafing change direction instead of tracing
// one predictable circle.
func strafePoint(botPos, target mgl32.Vec3, dist float32, sign float32) mgl32.Vec3 {
	dx, dz := botPos.X()-target.X(), botPos.Z()-target.Z()
	d := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if d < 1e-4 {
		return target.Add(mgl32.Vec3{dist, 0, 0})
	}
	ux, uz := dx/d, dz/d
	// Rotate the bearing ninety degrees.
	sx, sz := -uz*sign, ux*sign
	return target.Add(mgl32.Vec3{sx * dist, 0, sz * dist})
}

// within reports whether v lies inside [lo, hi].
func within(v, lo, hi float32) bool { return v >= lo && v <= hi }
