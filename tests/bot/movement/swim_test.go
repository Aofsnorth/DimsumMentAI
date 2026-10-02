package movement_test

import (
	"bytes"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/movement"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// SwimWorld is the narrow world view the swim planner reads. It is deliberately
// literal: a cell holds water or it does not, so a test can write "a river five
// blocks wide" instead of describing a collision model.
type SwimWorld struct {
	Water map[[3]int32]bool
	Solid map[[3]int32]bool
}

func newSwimWorld() *SwimWorld {
	return &SwimWorld{
		Water: make(map[[3]int32]bool),
		Solid: make(map[[3]int32]bool),
	}
}

// waterColumn fills x,z for every y in [lowY, highY] inclusive.
func (w *SwimWorld) waterColumn(x, z int32, lowY, highY int32) {
	for y := lowY; y <= highY; y++ {
		w.Water[[3]int32{x, y, z}] = true
	}
}

func (w *SwimWorld) solidAt(x, y, z int32) { w.Solid[[3]int32{x, y, z}] = true }

func (w *SwimWorld) IsWater(x, y, z int32) bool { return w.Water[[3]int32{x, y, z}] }
func (w *SwimWorld) IsSolid(x, y, z int32) bool { return w.Solid[[3]int32{x, y, z}] }

// FakeWaterModel is a SwimWorld that also answers the wider entity.WorldModel
// question, so it can be handed to a controller through the same narrow seam
// the real bot uses: GetLocalWorldModel returns the wide view, and the
// controller narrows it to the water view itself.
type FakeWaterModel struct {
	*SwimWorld
}

func (m *FakeWaterModel) SetSolid(x, y, z int32, solid bool) {
	if solid {
		m.solidAt(x, y, z)
	} else {
		delete(m.Solid, [3]int32{x, y, z})
	}
}

func (m *FakeWaterModel) IsHazard(x, y, z int32) bool          { return false }
func (m *FakeWaterModel) SetHazard(x, y, z int32, hazard bool) {}

// fakeSwimBot records what the controller asked the bot to do.
type fakeSwimBot struct {
	pos        mgl32.Vec3
	model      entity.WorldModel
	navigated  []protocol.BlockPos
	navReaches bool
}

func (b *fakeSwimBot) GetCoords() mgl32.Vec3 { return b.pos }

func (b *fakeSwimBot) GetLocalWorldModel() entity.WorldModel { return b.model }

func (b *fakeSwimBot) NavigateTo(pos mgl32.Vec3) {}

func (b *fakeSwimBot) StopMovement() {}

func (b *fakeSwimBot) NavigateToBlock(x, y, z int32, _ float32) bool {
	b.navigated = append(b.navigated, protocol.BlockPos{x, y, z})
	return b.navReaches
}

// testClock is a hand-driven clock so a test can age the submersion counter
// without sleeping through it.
type testClock struct{ at time.Time }

func (c *testClock) now() time.Time          { return c.at }
func (c *testClock) advance(d time.Duration) { c.at = c.at.Add(d) }

func newTestController(t *testing.T, world *SwimWorld, pos mgl32.Vec3) (*movement.SwimController, *fakeSwimBot, *testClock) {
	t.Helper()
	b := &fakeSwimBot{pos: pos, model: &FakeWaterModel{SwimWorld: world}, navReaches: true}
	c := movement.NewSwimController(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	clock := &testClock{at: time.Unix(1_700_000_000, 0)}
	c.SetClock(clock.now)
	return c, b, clock
}

// --- submersion sampling ---------------------------------------------------

// TestSampleSubmersionFindsSurfaceAboveTheBot pins the one number every breath
// and dive decision hangs off: how far the body must rise before the head is
// out of the water.
func TestSampleSubmersionFindsSurfaceAboveTheBot(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63) // surface cell is y=63; y=64 and up is air

	sub := movement.SampleSubmersion(w, 0, 60, 0)

	if !sub.InWater {
		t.Error("feet cell is water but InWater is false")
	}
	if !sub.Submerged {
		t.Error("head cell is water but Submerged is false")
	}
	if !sub.HasSurface {
		t.Fatal("a surface three blocks up was not found")
	}
	if sub.SurfaceY != 63 {
		t.Errorf("SurfaceY = %d, want 63", sub.SurfaceY)
	}
	if sub.ClimbBlocks != 3 {
		t.Errorf("ClimbBlocks = %d, want 3", sub.ClimbBlocks)
	}
}

// TestSampleSubmersionIsDryOnLand is the case that has to keep the planner
// asleep: a bot next to a river must not be told it is swimming.
func TestSampleSubmersionIsDryOnLand(t *testing.T) {
	t.Parallel()

	sub := movement.SampleSubmersion(newSwimWorld(), 4, 64, 0)

	if sub.InWater || sub.Submerged {
		t.Errorf("dry cell reported as wet: %+v", sub)
	}
	if sub.ClimbBlocks != 0 {
		t.Errorf("ClimbBlocks = %d on dry land, want 0", sub.ClimbBlocks)
	}
}

// TestSampleSubmersionWaistDeepIsNotSubmerged keeps a bot standing in a puddle
// working. Only the head cell drains the air bar.
func TestSampleSubmersionWaistDeepIsNotSubmerged(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 64, 64) // one cell of water, the feet

	sub := movement.SampleSubmersion(w, 0, 64, 0)

	if !sub.InWater {
		t.Error("the feet are in water but InWater is false")
	}
	if sub.Submerged {
		t.Error("the head is out of the water; the bot can breathe and must keep working")
	}
	if sub.ClimbBlocks != 0 {
		t.Errorf("ClimbBlocks = %d with the head already clear, want 0", sub.ClimbBlocks)
	}
}

// TestSampleSubmersionReportsNoSurfaceInsideTheWindow covers a flooded shaft.
// The planner has to be able to say "I cannot see air" rather than inventing a
// surface at some arbitrary height.
func TestSampleSubmersionReportsNoSurfaceInsideTheWindow(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 40, 120)

	sub := movement.SampleSubmersion(w, 0, 40, 0)

	if !sub.Submerged {
		t.Error("head cell is water but Submerged is false")
	}
	if sub.HasSurface {
		t.Error("a surface was reported inside a 80-block water column")
	}
	if sub.ClimbBlocks != movement.SurfaceSearchLimit {
		t.Errorf("ClimbBlocks = %d, want the search window %d", sub.ClimbBlocks, movement.SurfaceSearchLimit)
	}
}

// --- swim planning ---------------------------------------------------------

// TestPlanSwimStaysStillOnDryLand is the regression that matters most: adding a
// swim mode must not make the bot swim everywhere.
func TestPlanSwimStaysStillOnDryLand(t *testing.T) {
	t.Parallel()

	sub := movement.SampleSubmersion(newSwimWorld(), 0, 64, 0)
	depth := movement.DepthPlan{Action: movement.DepthHold, TargetY: 64}

	intent := movement.PlanSwim(sub, depth, movement.DefaultSwimOptions())

	if intent.Mode != movement.SwimModeDry {
		t.Errorf("Mode = %q on dry land, want %q", intent.Mode, movement.SwimModeDry)
	}
	if intent.Forward != 0 || intent.Strafe != 0 || intent.Vertical != 0 {
		t.Errorf("dry bot was told to move: %+v", intent)
	}
	if intent.Jump || intent.Sneak || intent.Sprint {
		t.Errorf("dry bot was told to press swim keys: %+v", intent)
	}
}

// TestPlanSwimPropelsForwardWhileSubmerged is the river crossing. Underwater
// the bot has to actually drive forwards, and it does it by sprint-swimming,
// which is the only way a real client crosses water quickly.
func TestPlanSwimPropelsForwardWhileSubmerged(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	sub := movement.SampleSubmersion(w, 0, 61, 0)

	intent := movement.PlanSwim(sub, movement.DepthPlan{Action: movement.DepthHold, TargetY: 61}, movement.DefaultSwimOptions())

	if intent.Mode != movement.SwimModeSwim {
		t.Errorf("Mode = %q fully submerged, want %q", intent.Mode, movement.SwimModeSwim)
	}
	if intent.Forward <= 0 {
		t.Errorf("Forward = %v, want a positive drive", intent.Forward)
	}
	if intent.Surfaced {
		t.Error("Surfaced = true with the head cell in water")
	}
	if !intent.Sprint {
		t.Error("a submerged bot is not sprint-swimming; the river crossing will crawl")
	}
}

// TestPlanSwimFloatsAtTheSurfaceWithARiseBias is the bobbing half. The movement
// loop applies gravity on every tick that is not grounded and knows nothing
// about water, so a body at the waterline that is not pushed up sinks a little
// further every tick until its head is under and the breath reflex has to
// rescue it from its own buoyancy.
func TestPlanSwimFloatsAtTheSurfaceWithARiseBias(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	sub := movement.SampleSubmersion(w, 0, 63, 0) // feet in the top water cell, head in air

	intent := movement.PlanSwim(sub, movement.DepthPlan{Action: movement.DepthHold, TargetY: 63}, movement.DefaultSwimOptions())

	if intent.Mode != movement.SwimModeSurface {
		t.Fatalf("Mode = %q at the waterline, want %q", intent.Mode, movement.SwimModeSurface)
	}
	if !intent.Surfaced {
		t.Error("Surfaced = false with the head out of the water")
	}
	if intent.Forward <= 0 {
		t.Error("a surfaced bot stopped moving; it will never reach the far bank")
	}
	if intent.Vertical <= 0 {
		t.Errorf("Vertical = %v at the waterline, want a rise that beats gravity", intent.Vertical)
	}
	if intent.Vertical >= 1 {
		t.Errorf("Vertical = %v at the waterline, want a gentle bias, not a full climb", intent.Vertical)
	}
	if intent.Jump {
		t.Error("Jump = true with the head already clear; that is swimming in place")
	}
}

// TestPlanSwimRisesOnAnAscendPlan wires the dive planner's climb into the input
// the server actually reads: jump plus the analogue up axis.
func TestPlanSwimRisesOnAnAscendPlan(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	sub := movement.SampleSubmersion(w, 0, 60, 0)

	intent := movement.PlanSwim(sub, movement.DepthPlan{Action: movement.DepthAscend, TargetY: 63}, movement.DefaultSwimOptions())

	if intent.Mode != movement.SwimModeAscend {
		t.Errorf("Mode = %q, want %q", intent.Mode, movement.SwimModeAscend)
	}
	if intent.Vertical <= 0 {
		t.Errorf("Vertical = %v, want a rise", intent.Vertical)
	}
	if !intent.Jump {
		t.Error("Jump = false while climbing; the client holds jump to swim up")
	}
	if intent.Pitch >= 0 {
		t.Errorf("Pitch = %v while climbing, want the head tipped up", intent.Pitch)
	}
}

// TestPlanSwimDivesOnADescendPlan is the mirror: sneak plus the down axis, the
// keys a real player holds to go under.
func TestPlanSwimDivesOnADescendPlan(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	sub := movement.SampleSubmersion(w, 0, 63, 0)

	intent := movement.PlanSwim(sub, movement.DepthPlan{Action: movement.DepthDescend, TargetY: 60}, movement.DefaultSwimOptions())

	if intent.Mode != movement.SwimModeDescend {
		t.Errorf("Mode = %q, want %q", intent.Mode, movement.SwimModeDescend)
	}
	if intent.Vertical >= 0 {
		t.Errorf("Vertical = %v, want a descent", intent.Vertical)
	}
	if !intent.Sneak {
		t.Error("Sneak = false while diving; the client holds sneak to swim down")
	}
	if intent.Pitch <= 0 {
		t.Errorf("Pitch = %v while diving, want the head tipped down", intent.Pitch)
	}
}

// TestSwimMoveVectorScalesTheHeading checks the seam with the steering layer:
// the swim plan owns the magnitude, the path owns the direction.
func TestSwimMoveVectorScalesTheHeading(t *testing.T) {
	t.Parallel()

	intent := movement.SwimIntent{Mode: movement.SwimModeSwim, Strafe: 0.5, Forward: 0.9}
	got := movement.SwimMoveVector(intent, mgl32.Vec2{0.5, 1})

	if math.Abs(float64(got.X()-0.25)) > 0.001 || math.Abs(float64(got.Y()-0.9)) > 0.001 {
		t.Errorf("SwimMoveVector = %v, want (0.25, 0.9)", got)
	}

	// The heading is preserved, not replaced: a diagonal path still comes out
	// diagonal, just slower.
	diagonal := movement.SwimMoveVector(intent, mgl32.Vec2{1, 1})
	if math.Abs(float64(diagonal.X()-0.5)) > 0.001 || math.Abs(float64(diagonal.Y()-0.9)) > 0.001 {
		t.Errorf("diagonal SwimMoveVector = %v, want (0.5, 0.9)", diagonal)
	}

	stopped := movement.SwimMoveVector(movement.SwimIntent{Mode: movement.SwimModeDry}, mgl32.Vec2{0, 1})
	if stopped != (mgl32.Vec2{}) {
		t.Errorf("dry swim plan produced a move vector %v, want zero", stopped)
	}
}

// --- input flags -----------------------------------------------------------

// TestApplySwimInputFlagsSetsTheSwimmingEdge covers the one-shot transitions.
// A client sends StartSwimming on the tick it enters water and never again
// until it leaves; sending it every tick is a different client.
func TestApplySwimInputFlagsSetsTheSwimmingEdge(t *testing.T) {
	t.Parallel()

	intent := movement.SwimIntent{Mode: movement.SwimModeSwim, InWater: true, Forward: 1, Vertical: 1, Jump: true, Sprint: true}

	entering := protocol.NewInputFlags(packet.InputFlagCount)
	movement.ApplySwimInputFlags(&entering, intent, false)
	for _, flag := range []int{
		packet.InputFlagStartSwimming,
		packet.InputFlagJumping,
		packet.InputFlagUp,
		packet.InputFlagSprinting,
	} {
		if !entering.Load(flag) {
			t.Errorf("entering the water did not set flag %d", flag)
		}
	}

	already := protocol.NewInputFlags(packet.InputFlagCount)
	movement.ApplySwimInputFlags(&already, intent, true)
	if already.Load(packet.InputFlagStartSwimming) {
		t.Error("StartSwimming was re-sent while the bot was already swimming")
	}
	if !already.Load(packet.InputFlagUp) {
		t.Error("the climb axis was dropped on a tick that was already swimming")
	}

	leaving := protocol.NewInputFlags(packet.InputFlagCount)
	movement.ApplySwimInputFlags(&leaving, movement.SwimIntent{Mode: movement.SwimModeDry}, true)
	if !leaving.Load(packet.InputFlagStopSwimming) {
		t.Error("leaving the water did not set StopSwimming")
	}
	if leaving.Load(packet.InputFlagStartSwimming) {
		t.Error("StartSwimming was set on a dry tick")
	}
}

// TestApplySwimInputFlagsIgnoresAnUnsizedFlagSet is a guard, not a style point:
// protocol.InputFlags.Set panics on an out-of-range index and the zero value has
// no size at all, so a helper that called it blindly would take the bot down
// from inside the movement loop.
func TestApplySwimInputFlagsIgnoresAnUnsizedFlagSet(t *testing.T) {
	t.Parallel()

	flags := protocol.InputFlags{}

	movement.ApplySwimInputFlags(&flags, movement.SwimIntent{Mode: movement.SwimModeSwim, InWater: true, Forward: 1, Vertical: 1, Jump: true, Sneak: true, Sprint: true}, false)
}

// --- controller ------------------------------------------------------------

// TestControllerSamplesTheWaterItIsIn wires the narrow world seam end to end:
// a world model that cannot answer water questions is a world model the swim
// controller must refuse rather than guess at.
func TestControllerSamplesTheWaterItIsIn(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	controller, _, _ := newTestController(t, w, mgl32.Vec3{0.5, 61.0, 0.5})

	sub, known := controller.Sample()
	if !known {
		t.Fatal("controller could not reach a water-capable world")
	}
	if !sub.Submerged {
		t.Errorf("controller did not see the bot as submerged: %+v", sub)
	}

	dry := newSwimWorld()
	noWater, _, _ := newTestController(t, dry, mgl32.Vec3{0.5, 70.0, 0.5})
	sub, known = noWater.Sample()
	if !known {
		t.Fatal("controller lost the world just because the bot is on land")
	}
	if sub.InWater {
		t.Error("a bot on dry land was reported as in water")
	}
}

// TestControllerRefusesWithoutAWaterCapableWorld is the honest failure. A
// world model with no water question gets no swim planning, rather than a
// planner that assumes every cell is dry and never breathes.
func TestControllerRefusesWithoutAWaterCapableWorld(t *testing.T) {
	t.Parallel()

	b := &fakeSwimBot{pos: mgl32.Vec3{0.5, 61.0, 0.5}, model: noWaterModel{}}
	controller := movement.NewSwimController(b, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, known := controller.Sample(); known {
		t.Error("controller invented a water answer from a model that cannot give one")
	}
	if _, inWater := controller.Plan(); inWater {
		t.Error("controller planned a swim without a world")
	}
	if ok, reason := controller.NavigateToUnderwater(0, 61, 0, 1.0); ok {
		t.Errorf("NavigateToUnderwater succeeded with no water world (%s)", reason)
	}
}

// noWaterModel satisfies entity.WorldModel but has no IsWater, which is exactly
// the shape of a world model written before the water vocabulary existed.
type noWaterModel struct{}

func (noWaterModel) IsSolid(x, y, z int32) bool           { return false }
func (noWaterModel) SetSolid(x, y, z int32, solid bool)   {}
func (noWaterModel) IsHazard(x, y, z int32) bool          { return false }
func (noWaterModel) SetHazard(x, y, z int32, hazard bool) {}

// TestControllerSurfacesBeforeTheAirRunsOut is the dive/breath acceptance run,
// driven with a hand-advanced clock: the bot is told to hold a depth three
// blocks down, the air bar runs down with it, and the controller has to break
// the dive and bring it up before the budget is spent.
func TestControllerSurfacesBeforeTheAirRunsOut(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63) // surface feet-Y is 63
	controller, b, clock := newTestController(t, w, mgl32.Vec3{0.5, 60.5, 0.5})

	controller.SetDive(60)
	if target, ok := controller.DiveIntent(); !ok || target != 60 {
		t.Fatalf("DiveIntent = (%d, %v), want (60, true)", target, ok)
	}

	// swimStepPerTick is roughly the rise a real client achieves holding jump
	// in water; the test applies the plan's own vertical drive, so a plan that
	// stops climbing stops the bot rising.
	const swimStepPerTick = float32(0.08)
	const tick = 50 * time.Millisecond

	longestUnder := time.Duration(0)
	submergedFor := time.Duration(0)
	surfaceEvents := 0
	wasUnder := false
	lowestFeet := int32(64)

	for i := 0; i < 900; i++ { // 45 seconds of swimming
		clock.advance(tick)
		intent, inWater := controller.Plan()
		if !inWater {
			continue
		}
		sub := movement.SampleSubmersion(w, 0, int32(math.Floor(float64(b.pos.Y()))), 0)
		if sub.Submerged {
			submergedFor += tick
			if submergedFor > longestUnder {
				longestUnder = submergedFor
			}
			wasUnder = true
		} else {
			if wasUnder {
				surfaceEvents++
			}
			wasUnder = false
			submergedFor = 0
		}
		if sub.FeetY < lowestFeet {
			lowestFeet = sub.FeetY
		}
		b.pos = mgl32.Vec3{b.pos.X(), clampY(b.pos.Y()+intent.Vertical*swimStepPerTick, 60, 64), b.pos.Z()}
	}

	// Guard against the test passing for the wrong reason: the bot has to have
	// actually gone down, and to have come back up more than once, or "it never
	// drowned" proves nothing about the reflex.
	if lowestFeet > 60 {
		t.Fatalf("the bot never descended below the surface (lowest feet cell %d); the dive never ran", lowestFeet)
	}
	if longestUnder < 5*time.Second {
		t.Fatalf("the bot was only under for %v; the reserve never came into play", longestUnder)
	}
	if surfaceEvents < 2 {
		t.Fatalf("the bot surfaced %d times in 45 s of diving; the breath cycle did not repeat", surfaceEvents)
	}
	budget := movement.DefaultBreathBudget
	if longestUnder >= budget {
		t.Errorf("stayed under for %v, at or past the %v breath budget", longestUnder, budget)
	}
}

func clampY(v, low, high float32) float32 {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

// TestControllerStopsSwimmingOnDryLand checks the exit edge is driven by the
// observation, not by a caller remembering to clear it.
func TestControllerStopsSwimmingOnDryLand(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	controller, b, clock := newTestController(t, w, mgl32.Vec3{0.5, 61.0, 0.5})

	inWater := protocol.NewInputFlags(packet.InputFlagCount)
	intent, _ := controller.Plan()
	controller.ApplyInput(&inWater, intent)
	if !controller.IsSwimming() {
		t.Fatal("controller is not swimming while standing in a river")
	}

	clock.advance(time.Second)
	b.pos = mgl32.Vec3{0.5, 70.0, 0.5} // stepped out onto the bank

	dry := protocol.NewInputFlags(packet.InputFlagCount)
	intent, ok := controller.Plan()
	if !ok {
		t.Fatal("controller lost the world on dry land")
	}
	controller.ApplyInput(&dry, intent)

	if controller.IsSwimming() {
		t.Error("controller is still swimming after leaving the water")
	}
	if !dry.Load(packet.InputFlagStopSwimming) {
		t.Error("StopSwimming was never sent; the client stays in the swim pose on land")
	}
}

// TestControllerNavigateToUnderwaterRefusesADryTarget is the truthfulness
// check on the one function that reports a result: a dive to a cell with no
// water in it is a mistake, and it has to be reported as one.
func TestControllerNavigateToUnderwaterRefusesADryTarget(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	controller, _, _ := newTestController(t, w, mgl32.Vec3{0.5, 61.0, 0.5})

	ok, reason := controller.NavigateToUnderwater(0, 60, 0, 1.0)
	if !ok {
		t.Fatalf("diving to a water cell failed: %s", reason)
	}
	if target, diving := controller.DiveIntent(); !diving || target != 60 {
		t.Errorf("DiveIntent = (%d, %v) after a dive, want (60, true)", target, diving)
	}

	ok, reason = controller.NavigateToUnderwater(0, 66, 0, 1.0)
	if ok {
		t.Error("a dive to a dry cell reported success")
	}
	if reason == "" {
		t.Error("a refused dive returned no reason")
	}
}

// TestControllerClearsTheDiveIntent is the reset that keeps a finished dive from
// dragging the bot back under later.
func TestControllerClearsTheDiveIntent(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	controller, _, _ := newTestController(t, w, mgl32.Vec3{0.5, 61.0, 0.5})

	controller.SetDive(60)
	controller.ClearDive()

	if _, diving := controller.DiveIntent(); diving {
		t.Error("dive intent survived ClearDive")
	}
}

// TestControllerPlanFromReusesOneSample is the movement loop's seam: the loop
// already knows where the body is in the water and must not pay for a second
// world read to plan the swim.
func TestControllerPlanFromReusesOneSample(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	controller, _, clock := newTestController(t, w, mgl32.Vec3{0.5, 61.0, 0.5})
	controller.SetDive(60)

	clock.advance(time.Second)
	sub, known := controller.SubmersionAt(0, 61, 0)
	if !known {
		t.Fatal("controller could not sample a world it is standing in")
	}

	intent := controller.PlanFrom(sub)

	if intent.Mode != movement.SwimModeDescend {
		t.Errorf("Mode = %q three seconds into a dive to y=60, want %q", intent.Mode, movement.SwimModeDescend)
	}
	if !intent.Sneak {
		t.Error("Sneak = false while diving")
	}
	if controller.SubmergedSince().IsZero() {
		t.Error("the submersion clock did not start while the head was under")
	}
}

// TestControllerBreathBudgetIsConfigurable covers the one tuning a server with
// a different air bar needs, and pins the direction: a shorter budget surfaces
// the bot earlier, never later.
func TestControllerBreathBudgetIsConfigurable(t *testing.T) {
	t.Parallel()

	w := newSwimWorld()
	w.waterColumn(0, 0, 60, 63)
	controller, b, clock := newTestController(t, w, mgl32.Vec3{0.5, 61.0, 0.5})
	controller.SetBreathBudget(4 * time.Second)
	controller.SetDive(60)

	// The first plan starts the submersion clock; nothing before it is
	// something the controller could have known about.
	if _, inWater := controller.Plan(); !inWater {
		t.Fatal("controller lost the world mid-dive")
	}

	// Three seconds under on a four-second budget is one second from the end:
	// the reflex has to be climbing already.
	clock.advance(3 * time.Second)
	intent, inWater := controller.Plan()
	if !inWater {
		t.Fatal("controller lost the world mid-dive")
	}
	if intent.Mode != movement.SwimModeAscend {
		t.Errorf("Mode = %q with 1 s of a 4 s budget left, want %q", intent.Mode, movement.SwimModeAscend)
	}

	// A nonsense budget is ignored rather than adopted.
	controller.SetBreathBudget(0)
	clock.advance(6 * time.Second)
	b.pos = mgl32.Vec3{0.5, 61.0, 0.5}
	if _, inWater := controller.Plan(); !inWater {
		t.Error("a rejected budget broke the world seam")
	}
}

// TestControllerClimbsBlindInAFloodedShaft is the honest-failure path. With no
// air anywhere in the search window the plan still climbs — holding at the
// bottom is how a bot drowns on schedule — but it says so, and the controller
// logs it.
func TestControllerClimbsBlindInAFloodedShaft(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer
	w := newSwimWorld()
	w.waterColumn(0, 0, 40, 120) // eighty blocks of water, no air at all
	b := &fakeSwimBot{pos: mgl32.Vec3{0.5, 40.5, 0.5}, model: &FakeWaterModel{SwimWorld: w}}
	controller := movement.NewSwimController(b, slog.New(slog.NewTextHandler(&logged, nil)))
	clock := &testClock{at: time.Unix(1_700_000_000, 0)}
	controller.SetClock(clock.now)
	controller.SetDive(30)

	if _, inWater := controller.Plan(); !inWater {
		t.Fatal("controller lost the world in the shaft")
	}
	clock.advance(30 * time.Second) // the air bar is long gone
	intent, inWater := controller.Plan()
	if !inWater {
		t.Fatal("controller lost the world in the shaft")
	}
	if intent.Mode != movement.SwimModeAscend {
		t.Errorf("Mode = %q at the bottom of a flooded shaft, want %q", intent.Mode, movement.SwimModeAscend)
	}
	if intent.Vertical <= 0 {
		t.Error("the body is not climbing; it will drown where it stands")
	}
	if !strings.Contains(logged.String(), "climbing blind") {
		t.Errorf("the blind climb was not logged, so it would be a mystery in the field: %q", logged.String())
	}
}
