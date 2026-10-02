// Ledge sensing: what is under the bot's feet, and what is not under the step
// it is about to take.
//
// A bot with a pathfinder will happily walk off a cliff if the path says so,
// because the pathfinder routes around drops rather than over them, and a
// wandering bot has no path at all — it walks in whatever direction it picked,
// and "straight ahead" includes straight off a mountain.
//
// So the body needs one measurement the higher layers can reason about: how far
// the ground continues in the direction of travel, and how far down it lands.
// That number is what makes a cliff a fact rather than a surprise.

package movement

import (
	"log/slog"
	"math"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
)

// LedgeAhead is what a drop looks like from the edge of it.
type LedgeAhead struct {
	// Distance is how many blocks of clear ground are left before the drop, in
	// the direction of travel. Zero means the bot is already over air.
	Distance float32
	// DropTo is the feet height of the first solid ground below the edge. The
	// bot's own height minus this is the fall.
	Fall float32
	// LandingY is where the feet would end up. Equal to the bot's height on
	// flat ground, so a zero fall is a step and not a drop.
	LandingY float32
	// Known is false when the world cannot answer, which is not the same as
	// "there is no cliff". An unknown answer must never read as safety.
	Known bool
}

// SafeToWalk is the largest fall a bot will take without being told to.
const SafeToWalk = 3.0

// ledgeProbeReach is how far ahead the ground is sampled. Beyond this a cliff is
// not something the bot is about to walk into on this tick, and probing further
// only costs map reads at 20Hz.
const ledgeProbeReach = 4

// SenseLedge measures the ground ahead of a body facing yaw, and reports where
// it stops.
//
// It reads the world model rather than the position stream, because the model is
// what the physics and the pathfinder already agree on; a second opinion from
// the network position would be a third source of truth to keep consistent.
//
// A body standing still still gets an answer. That is deliberate: "is there a
// cliff where I am" is the question that decides whether standing still is a
// good idea, and it is worth asking before the bot picks somewhere to go.
func SenseLedge(world entity.WorldModel, from mgl32.Vec3, yaw float32) LedgeAhead {
	ledge := LedgeAhead{LandingY: from.Y(), Distance: math.MaxFloat32}
	if world == nil {
		return ledge
	}

	// The direction the body faces, matching ApplyMoveLookTarget's convention.
	radians := (yaw - 90) * bot.DegreesToRadians
	dirX := float32(math.Cos(float64(radians)))
	dirZ := float32(math.Sin(float64(radians)))

	feetX := int32(math.Floor(float64(from.X())))
	feetY := int32(math.Floor(float64(from.Y())))
	feetZ := int32(math.Floor(float64(from.Z())))

	// Footing first. A model with nothing solid under the body cannot be read
	// forward, because "no ground ahead" and "this chunk has not decoded yet" are
	// the same answer — and reading the second as the first is how a bot freezes
	// solid on a world that is still arriving.
	if !world.IsSolid(feetX, feetY-1, feetZ) {
		return ledge
	}

	for step := 1; step <= ledgeProbeReach; step++ {
		x := feetX + int32(math.Round(float64(dirX*float32(step))))
		z := feetZ + int32(math.Round(float64(dirZ*float32(step))))

		if world.IsSolid(x, feetY-1, z) {
			// Ground continues. Keep scanning for the first block that does not.
			ledge.Distance = float32(step)
			ledge.Known = true
			continue
		}

		// The edge. Look down for the landing, because how far it drops is the
		// difference between a step off a kerb and a four-storey drop.
		ledge.Distance = float32(step - 1)
		ledge.Known = true
		for y := feetY - 2; y > feetY-32; y-- {
			if world.IsSolid(x, y, z) {
				ledge.LandingY = float32(y) + 1
				ledge.Fall = from.Y() - ledge.LandingY
				if ledge.Fall < 0 {
					ledge.Fall = 0
				}
				return ledge
			}
		}
		// Nothing below inside the probe window: this is a bottomless-looking
		// drop as far as the bot can tell. Treated as unsafe below.
		ledge.LandingY = from.Y() - 32
		ledge.Fall = 32
		return ledge
	}

	return ledge
}

// IsCliff reports whether the drop ahead is one the bot should not walk into
// without being told to.
//
// A body that cannot see the ground gets the benefit of the doubt: reporting an
// unknown world as a cliff would make the bot stand still on every chunk that
// has not finished decoding, which is the "enabled but inert" failure this whole
// layer exists to avoid.
func (l LedgeAhead) IsCliff() bool {
	return l.Known && l.Fall > SafeToWalk
}

// stopsAtLedge reports whether the body must refuse the step it is about to
// take because there is a drop under it.
//
// The reading is cached on the tick because the prompt layer asks for it in the
// same tick the body acts on it: two probes of the same four cells per tick at
// 20Hz is map reads nobody needs.
func (tc *TickContext) stopsAtLedge() bool {
	if tc == nil || tc.B == nil {
		return false
	}
	ledge := SenseLedge(tc.B.WorldModel, tc.CurrPos, tc.Yaw)
	tc.LedgeAheadOfBody = ledge

	if !ledge.IsCliff() {
		return false
	}

	// INFO, and deliberately so.
	//
	// This gate was silent, which is the same defect the narration suppression
	// had: a feature that changes behaviour and never says so cannot be
	// verified against anything but its unit tests, and a reader watching a run
	// has no way to tell "the bot stopped at a cliff" from "the bot is stuck".
	//
	// It is rate-limited per node rather than per tick, because a body pressed
	// against a cliff is refused every single tick and twenty lines a second
	// would bury everything else in the log.
	tc.noteLedgeRefusal(ledge)
	return true
}

// lastLedgeNote remembers which ledge was last reported, so the refusal is
// logged once per approach rather than once per tick.
var lastLedgeNote struct {
	x, y, z int32
	at      time.Time
}

// ledgeNoteWindow is how long the same cliff stays reported. Long enough that
// approaching the same edge again later still logs, short enough that a bot
// genuinely working is not silent about it.
const ledgeNoteWindow = 10 * time.Second

func (tc *TickContext) noteLedgeRefusal(ledge LedgeAhead) {
	p := tc.CurrPos
	cx, cy, cz := int32(p.X()), int32(p.Y()), int32(p.Z())

	if cx == lastLedgeNote.x && cy == lastLedgeNote.y && cz == lastLedgeNote.z &&
		time.Since(lastLedgeNote.at) < ledgeNoteWindow {
		return
	}
	lastLedgeNote = struct {
		x, y, z int32
		at      time.Time
	}{cx, cy, cz, time.Now()}

	tc.B.Logger.Info("movement: refused a step over a drop",
		slog.Float64("fall", float64(ledge.Fall)),
		slog.Float64("ground_ahead", float64(ledge.Distance)),
		slog.Float64("landing_y", float64(ledge.LandingY)),
		slog.Float64("safe_to_walk", SafeToWalk),
		slog.Float64("x", float64(p.X())),
		slog.Float64("y", float64(p.Y())),
		slog.Float64("z", float64(p.Z())))
}

// DropAuthorised reports whether Jev asked for this drop.
//
// This is the whole point of the gate existing. Refusing to walk off a cliff is
// only correct until the moment a player deliberately leaps off one — that is a
// decision, not an accident, and a bot that cannot make it is walking a
// staircase in both directions. The authority is consumed on use so it lasts
// one jump rather than becoming "walk off anything, from now on".
func (tc *TickContext) DropAuthorised() bool {
	if tc == nil || !tc.DropOK {
		return false
	}
	tc.DropOK = false
	return true
}

// takeRequestedDrop spends a deliberate-drop authority, if one is pending.
//
// It is read from the bot rather than carried on the tick because the AGI sets
// it on a different goroutine, and spending it here is what makes it one jump
// rather than a standing permission.
func (tc *TickContext) takeRequestedDrop() {
	if tc == nil || tc.B == nil || tc.dropTaken {
		return
	}
	if !tc.B.ConsumeDrop() {
		return
	}
	tc.dropTaken = true
	tc.DropOK = true
}
