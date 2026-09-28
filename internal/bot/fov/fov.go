// Package fov implements the bot's field of view as pure geometry.
//
// It lives in its own package, depending on nothing but the vector library,
// because two other packages need the same cone and either one importing the
// other would close an import cycle: the perception package reports what the
// bot can see, and the storage package decides what the bot may act on, while
// the bot type itself owns both.
//
// Keeping the cone here is what makes "visible" mean the same thing
// everywhere. Before this existed, the block summary tested line of sight over
// a full sphere, so the bot "saw" the tree behind its own head; the chest
// search had its own separate rule; and the AGI had a third. One geometry, one
// answer, no drift.
package fov

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// HalfAngleDeg is the half-angle of the vision cone, in degrees.
	//
	// 100 gives a full 200° of arc, which is generous but honest: peripheral
	// vision really does reach roughly that far, and the line-of-sight walk is
	// what actually decides whether a specific block is visible. A tighter cone
	// would not make the bot smarter, only blinder — a player notices the
	// chest in the corner of their eye, and a bot that "cannot" is a different
	// failure from a bot that is careful.
	HalfAngleDeg = 100.0

	// CloseRadius is the radius inside which everything is noticed regardless of
	// angle.
	//
	// A player does not fail to notice a creeper at their feet because it is
	// behind them; peripheral vision at that distance is close to total. This
	// radius keeps close-range awareness intact, which matters because a
	// creeper the cone hid would be a death the bot could not explain.
	CloseRadius = 3.0

	// AboveDeg and BelowDeg bound the vertical arc.
	//
	// A player can look down at their feet or up at the sky, and a purely
	// horizontal cone would hide the block they are standing beside. They are
	// asymmetric because looking up and looking down are not equally
	// comfortable, and a head pinned at +80° reads as broken rather than
	// curious.
	AboveDeg = 80.0
	BelowDeg = 70.0
)

// Within reports whether a world point falls inside the vision cone.
//
// eye is the point the cone originates from (the bot's eyes, not its feet) and
// headYaw is the heading the head is actually pointing along — which is not
// necessarily the direction the body is travelling, because the head leads and
// lags the torso independently.
func Within(point, eye mgl32.Vec3, headYaw float32) bool {
	dx := point.X() - eye.X()
	dy := point.Y() - eye.Y()
	dz := point.Z() - eye.Z()

	// A point on the eye itself is trivially noticed.
	if dx*dx+dy*dy+dz*dz < 1e-4 {
		return true
	}

	horizontal := float32(math.Hypot(float64(dx), float64(dz)))
	if horizontal <= CloseRadius {
		return true
	}

	// Vertical arc is checked before the horizontal cone. A block directly
	// overhead is common (a low ceiling, a tree canopy) and is nearly vertical,
	// so a cone tested on horizontal angle alone would make the "angle" for it
	// meaningless and could drop it.
	pitch := pitchTo(point, eye)
	if pitch > AboveDeg || pitch < -BelowDeg {
		return false
	}

	return AngleWithin(point, eye, headYaw, HalfAngleDeg)
}

// AngleWithin reports whether the horizontal direction from `from` to `point`
// lies within halfAngle degrees of heading.
func AngleWithin(point, from mgl32.Vec3, heading, halfAngle float32) bool {
	dx := point.X() - from.X()
	dz := point.Z() - from.Z()
	if dx == 0 && dz == 0 {
		return true
	}

	target := headingTo(point, from)
	diff := target - heading
	// Wraparound matters: without it a target at 179° and one at -179° are two
	// degrees apart in the world but 358° apart by subtraction, which would put
	// every point behind the bot outside a wide cone.
	for diff > 180 {
		diff -= 360
	}
	for diff < -180 {
		diff += 360
	}
	if diff < 0 {
		diff = -diff
	}
	return diff <= halfAngle
}

// headingTo is the yaw of the direction from → point, in the project convention
// (yaw 0 faces +Z, yaw 90 faces -X), normalised to [0, 360).
func headingTo(point, from mgl32.Vec3) float32 {
	heading := float32(math.Atan2(float64(point.Z()-from.Z()), float64(point.X()-from.X()))*180/math.Pi) - 90
	for heading < 0 {
		heading += 360
	}
	for heading >= 360 {
		heading -= 360
	}
	return heading
}

// pitchTo is the vertical angle from eye to point, in degrees, with Bedrock's
// sign convention: negative looks up, positive looks down.
func pitchTo(point, eye mgl32.Vec3) float32 {
	dx := point.X() - eye.X()
	dy := point.Y() - eye.Y()
	dz := point.Z() - eye.Z()
	horizontal := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if horizontal < 1e-3 {
		horizontal = 1e-3
	}
	return float32(-math.Atan2(float64(dy), float64(horizontal)) * 180 / math.Pi)
}
