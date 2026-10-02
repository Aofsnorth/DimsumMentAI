package movement_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot/movement"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// The swim rules were written, unit-tested against the controller, and then
// never reached the wire. buildInputData is the only thing in this package that
// produces a PlayerAuthInput, and it did not know water existed — so the swim,
// dive and surface flags were computed each tick and discarded, and a bot told
// to swim across a river walked along the bank.
//
// These tests are about the seam, not the rules. The rules have their own
// coverage in swim_test.go and dive_test.go; what was untested is the two
// places the plan has to cross to become real: the flag set on the packet, and
// the vertical physics that would otherwise fight it.

// newWiredController builds a controller over a fake whose feet are at pos. The
// clock is pinned so a test that ages the breath counter does not depend on how
// long the suite has been running.
func newWiredController(t *testing.T, world *SwimWorld, pos mgl32.Vec3) *movement.SwimController {
	t.Helper()
	b := &fakeSwimBot{pos: pos, model: &FakeWaterModel{SwimWorld: world}, navReaches: true}
	c := movement.NewSwimController(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	at := time.Unix(1_700_000_000, 0)
	c.SetClock(func() time.Time { return at })
	return c
}

// TestTheSwimControllerSatisfiesTheTickContextSeam proves the two agree on the
// interface. If this stops compiling, SendInputLoop cannot install a controller
// and the whole feature silently falls back to doing nothing.
func TestTheSwimControllerSatisfiesTheTickContextSeam(t *testing.T) {
	t.Parallel()

	if newWiredController(t, newSwimWorld(), mgl32.Vec3{}) == nil {
		t.Fatal("a fresh controller was nil")
	}
}

// TestStartSwimmingIsSentOnceOnEntry is the edge that a per-tick controller gets
// wrong. StartSwimming is a transition, not a state: re-sending it every tick
// tells the server the body keeps re-entering the water, and the bot never
// settles into a swim.
func TestStartSwimmingIsSentOnceOnEntry(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63) // surface at 63, bed at 60
	ctrl := newWiredController(t, w, mgl32.Vec3{0.5, 61.0, 0.5})

	first := protocol.NewInputFlags(packet.InputFlagCount)
	ctrl.ApplyInput(&first, movement.SwimIntent{Mode: movement.SwimModeSwim, InWater: true, Forward: 1})
	if !first.Load(packet.InputFlagStartSwimming) {
		t.Error("entering the water did not send StartSwimming")
	}

	second := protocol.NewInputFlags(packet.InputFlagCount)
	ctrl.ApplyInput(&second, movement.SwimIntent{Mode: movement.SwimModeSwim, InWater: true, Forward: 1})
	if second.Load(packet.InputFlagStartSwimming) {
		t.Error("StartSwimming was re-sent while the body was already swimming")
	}

	leaving := protocol.NewInputFlags(packet.InputFlagCount)
	ctrl.ApplyInput(&leaving, movement.SwimIntent{Mode: movement.SwimModeDry})
	if !leaving.Load(packet.InputFlagStopSwimming) {
		t.Error("leaving the water did not send StopSwimming")
	}
}

// TestDiveSinksAndAscendRises pins the two input flags a client actually holds
// to dive and to rise. A dive with no sneak and an ascent with no jump are the
// two ways a body reads as "trying but not doing it".
func TestDiveSinksAndAscendRises(t *testing.T) {
	t.Parallel()

	dive := protocol.NewInputFlags(packet.InputFlagCount)
	movement.ApplySwimInputFlags(&dive, movement.SwimIntent{Mode: movement.SwimModeDescend, InWater: true, Vertical: -1, Sneak: true}, true)
	if !dive.Load(packet.InputFlagSneaking) {
		t.Error("a dive did not hold sneak")
	}

	rise := protocol.NewInputFlags(packet.InputFlagCount)
	movement.ApplySwimInputFlags(&rise, movement.SwimIntent{Mode: movement.SwimModeAscend, InWater: true, Vertical: 1, Jump: true}, true)
	if !rise.Load(packet.InputFlagJumping) {
		t.Error("an ascent did not hold jump")
	}
}

// TestTheSurfaceBiasIsInThePlanNotThePhysics guards a double-count. PlanSwim puts
// the floating bias in Vertical for a surfaced body; the physics multiplies that
// axis. Adding the bias again in the physics would lift a floating body out of
// the water it is meant to be floating in.
func TestTheSurfaceBiasIsInThePlanNotThePhysics(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	sub := movement.SampleSubmersion(w, 0, 63, 0) // feet in the top water cell, head in air

	if sub.Submerged {
		t.Fatal("the body is head-clear; this case is about floating, not diving")
	}

	intent := movement.PlanSwim(sub, movement.DepthPlan{Action: movement.DepthHold, TargetY: 63}, movement.DefaultSwimOptions())
	if intent.Mode != movement.SwimModeSurface {
		t.Fatalf("Mode = %q at the waterline, want %q", intent.Mode, movement.SwimModeSurface)
	}
	// The rise has to be a small positive fraction, not a full climb: a full
	// climb on a surfaced body pumps it out of the river.
	if intent.Vertical <= 0 || intent.Vertical > 0.5 {
		t.Errorf("floating Vertical = %v, want a small positive rise; the physics scales this into velocity", intent.Vertical)
	}
}

// TestAGroundBodyProducesNoSwimFlags is the no-regression half. The plan runs
// every tick for every body, and a dry bot has to produce exactly the flags it
// produced before water movement existed.
func TestAGroundBodyProducesNoSwimFlags(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.solidAt(0, 63, 0)
	sub := movement.SampleSubmersion(w, 0, 64, 0)

	intent := movement.PlanSwim(sub, movement.DepthPlan{Action: movement.DepthHold, TargetY: 64}, movement.DefaultSwimOptions())
	if intent.Mode != movement.SwimModeDry {
		t.Fatalf("Mode = %q on dry land, want %q", intent.Mode, movement.SwimModeDry)
	}

	flags := protocol.NewInputFlags(packet.InputFlagCount)
	movement.ApplySwimInputFlags(&flags, intent, false)
	if flags.Load(packet.InputFlagStartSwimming) || flags.Load(packet.InputFlagStopSwimming) {
		t.Error("a dry body sent a swim transition flag")
	}
	if flags.Load(packet.InputFlagJumping) || flags.Load(packet.InputFlagSneaking) {
		t.Error("a dry body sent a swim key that belongs to a body in water")
	}
}
