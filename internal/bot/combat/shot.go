// Ranged shots: the decision, kept apart from the packets.
//
// choice.go already decides that a bow is the right thing to hold when the
// target is out of sword reach; what was missing was anything happening after
// the swap. A bow is not a swing — it is a hold of about 1.1 seconds and a
// release — so the shot is a small state machine: draw, wait out the draw,
// release. The decisions live here over plain data so they can be tested
// without starting a fight, the same way the weapon choice is.

package combat

import (
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// fullBowDraw is how long a bow must be held before release for a full-
	// power, flat-trajectory shot. The release packet is duration-based, so a
	// short draw is not a miss but a weak, arcing shot.
	fullBowDraw = 1100 * time.Millisecond
	// crossbowLoadTime is the crossbow's load phase. It plays the same draw
	// animation as a bow but loads the bolt into the weapon; once loaded the
	// shot fires instantly and stays loaded until it goes off.
	crossbowLoadTime = 1250 * time.Millisecond
	// bowShotInterval is the pause between shots. It covers the release, the
	// arrow's flight, and the next draw without pretending the interval can be
	// shorter than the animation allows.
	bowShotInterval = 1500 * time.Millisecond
)

// Arrow flight, used to aim.
const (
	// arrowSpeed is the horizontal blocks-per-tick of a fully drawn arrow. It
	// is the number that makes a shot at twenty blocks reach at all.
	arrowSpeed = 3.0
	// arrowGravity is the vertical pull on an arrow in flight, per tick.
	arrowGravity = 0.05
)

// bowAimHeight is how far up a normal mob body the aim point sits: the middle
// of a two-block-tall target rather than its base, so a shot at a zombie does
// not sail over its head.
const bowAimHeight float32 = 1.2

// bowAimPoint is where to aim so the arrow arrives at the target rather than
// burying itself in the ground short of them.
//
// An arrow does not travel in a straight line. It is launched fast and pulled
// down the whole way, so a shot aimed straight at the body's centre lands
// below the feet at any real distance — badly so at twenty blocks, which is
// exactly where a bow is worth holding. Dropping the calculation into a pure
// function keeps the arithmetic honest and testable: the aim point is the
// target's centre lifted by half a gravity times the flight time squared.
func bowAimPoint(from, target mgl32.Vec3) mgl32.Vec3 {
	return liftAimPoint(from, target, bowAimHeight)
}

// liftAimPoint is bowAimPoint for a target that is not a two-block mob.
//
// The gravity correction is the same either way; only the height the arrow is
// aimed at changes, so the arithmetic lives here once and the callers say how
// tall their target is.
func liftAimPoint(from, target mgl32.Vec3, centre float32) mgl32.Vec3 {
	point := target.Add(mgl32.Vec3{0, centre, 0})
	ticks := float64(horizontalDistance(from, target)) / arrowSpeed
	drop := float32(0.5 * arrowGravity * ticks * ticks)
	return point.Add(mgl32.Vec3{0, drop, 0})
}

// ShotAction is what the ranged state machine does next.
type ShotAction int

const (
	// ShotNone: keep the weapon held and wait — drawing, out of arrows, or
	// cooling down between shots.
	ShotNone ShotAction = iota
	// ShotDraw: start holding the weapon down to draw it.
	ShotDraw
	// ShotFire: release the draw and let the shot go.
	ShotFire
)

// planShot decides the next ranged step from plain data: the chosen weapon,
// the ammunition, and the timing of the shot currently in flight.
func planShot(kind WeaponKind, hasArrows bool, now time.Time, s shot) ShotAction {
	if kind != WeaponBow && kind != WeaponCrossbow {
		return ShotNone
	}
	if kind == WeaponBow && !hasArrows {
		// A bow with nothing to shoot is a stick; holding it down would only
		// lock the bot in place.
		return ShotNone
	}
	if s.drawStart.IsZero() {
		if !s.lastShot.IsZero() && now.Sub(s.lastShot) < bowShotInterval {
			return ShotNone
		}
		return ShotDraw
	}
	need := fullBowDraw
	if kind == WeaponCrossbow {
		// A crossbow that already holds a bolt fires on the spot; the load
		// phase was paid for earlier in this same shot.
		if !s.loaded {
			need = crossbowLoadTime
		} else {
			need = 0
		}
	}
	if now.Sub(s.drawStart) >= need {
		return ShotFire
	}
	return ShotNone
}

// shot is the state of the shot currently in flight. It lives on the manager
// because it must survive across ticks: the draw is held for about a second
// while the tick loop keeps running.
type shot struct {
	// kind and slot are the weapon the draw was started with, so the release
	// carries the same item the server saw drawn.
	kind WeaponKind
	slot uint32
	// drawStart is zero while no draw is in flight.
	drawStart time.Time
	// loaded is whether the crossbow in flight already holds a bolt.
	loaded bool
	// lastShot gates the next draw, keeping shots at a human pace.
	lastShot time.Time
}

// recordDraw marks the beginning of a draw.
func (s *shot) recordDraw(kind WeaponKind, slot uint32, now time.Time) {
	s.kind = kind
	s.slot = slot
	s.drawStart = now
}

// recordRelease marks the moment the shot went off and resets the draw.
func (s *shot) recordRelease(now time.Time) {
	s.drawStart = time.Time{}
	s.loaded = false
	s.lastShot = now
}
