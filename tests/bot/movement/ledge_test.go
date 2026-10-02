package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// A cliff is a fact the body has to be able to see, not a surprise it discovers
// by falling.
//
// The pathfinder routes around drops, so a following bot is fine. A wandering
// one has no path at all — it walks in whatever direction it picked, and
// "straight ahead" includes straight off a mountain. That is the gap these tests
// close: one measurement, read by both the body that acts on it and the model
// that decides about it.
func flatWorld() *pathfinder.LocalWorldModel {
	model := pathfinder.NewLocalWorldModel()
	// Solid at y=62, because a body with its feet at y=63 is standing ON the
	// block at 62.
	//
	// Bounds matter more than they look. Without them the model answers any
	// unset cell at or below y=62 with "solid, sea level", so a downward scan
	// for the bottom of a drop stops one block down and every drop measures as
	// one block. That is the right default for a bot joining a world it cannot
	// see yet, and the wrong one for a test describing a world it just built.
	model.SetPathBounds(pathfinder.Node{X: 0, Y: 63, Z: 0}, pathfinder.Node{X: 8, Y: 63, Z: 0})
	for x := int32(-8); x <= 8; x++ {
		for z := int32(-8); z <= 8; z++ {
			model.SetSolid(x, 62, z, true)
		}
	}
	return model
}

// TestFlatGroundIsNotACliff is the control. Everything below is worthless if
// ordinary walking reads as a drop.
func TestFlatGroundIsNotACliff(t *testing.T) {
	t.Parallel()

	model := flatWorld()
	ledge := movement.SenseLedge(model, mgl32.Vec3{0.5, 63, 0.5}, 90)

	if !ledge.Known {
		t.Fatal("flat ground read as unknown, so nothing can be decided about it")
	}
	if ledge.IsCliff() {
		t.Errorf("flat ground read as a cliff: fall %.1f", ledge.Fall)
	}
}

// TestAKerbIsNotACliff pins the threshold. A one-block step down is something a
// player walks off without thinking, and a bot that stops at every one of them
// is a bot that cannot move.
func TestAKerbIsNotACliff(t *testing.T) {
	t.Parallel()

	model := flatWorld()
	// Dig the ground away from x=2 onward so the drop begins immediately.
	// Ground removed from x=2 onward, with a floor exactly one block down, so
	// the drop is a step rather than a fall.
	for x := int32(2); x <= 8; x++ {
		model.SetSolid(x, 62, 0, false)
		model.SetSolid(x, 61, 0, true)
	}

	ledge := movement.SenseLedge(model, mgl32.Vec3{0.5, 63, 0.5}, 90)
	if ledge.IsCliff() {
		t.Errorf("a one-block kerb read as a cliff: fall %.1f", ledge.Fall)
	}
}

// TestARealDropIsACliff is the case that matters. Four blocks is survivable in a
// way that eight is not, and the threshold sits between them on purpose.
func TestARealDropIsACliff(t *testing.T) {
	t.Parallel()

	model := flatWorld()
	for x := int32(2); x <= 8; x++ {
		model.SetSolid(x, 62, 0, false)
	}
	// A floor eight blocks down, so the drop has somewhere to land.
	for x := int32(2); x <= 8; x++ {
		model.SetSolid(x, 55, 0, true)
	}

	ledge := movement.SenseLedge(model, mgl32.Vec3{0.5, 63, 0.5}, 90)
	if !ledge.Known {
		t.Fatal("a drop with a visible floor read as unknown")
	}
	if !ledge.IsCliff() {
		t.Errorf("an eight-block drop was not a cliff: fall %.1f, landing y=%.1f", ledge.Fall, ledge.LandingY)
	}
	if ledge.Fall < 7 || ledge.Fall > 9 {
		t.Errorf("fall = %.1f, want about 8: the measurement is not reporting the real drop", ledge.Fall)
	}
}

// TestTheDropIsMeasuredInTheDirectionFacing is the part a naive implementation
// gets wrong: a cliff to the left is not a cliff in front. A bot that reads
// eight directions and panics on any of them stops moving everywhere.
func TestTheDropIsMeasuredInTheDirectionFacing(t *testing.T) {
	t.Parallel()

	model := flatWorld()
	// A trench three blocks east, inside the four-block probe reach. Putting it
	// further out measures the reach limit rather than the facing, which is a
	// real limit and worth knowing — but not what this test is about.
	for z := int32(-8); z <= 8; z++ {
		model.SetSolid(3, 62, z, false)
		model.SetSolid(3, 55, z, true)
	}
	from := mgl32.Vec3{0.5, 63, 0.5}

	// Facing away from the trench, there is nothing in the way.
	away := movement.SenseLedge(model, from, 270)
	if away.IsCliff() {
		t.Errorf("facing away from the trench still read as a cliff: fall %.1f", away.Fall)
	}

	// Facing into it, there very much is.
	into := movement.SenseLedge(model, from, 90)
	if !into.IsCliff() {
		t.Errorf("facing the trench did not read as a cliff: fall %.1f", into.Fall)
	}
}

// TestAnUnknownWorldIsNotACliff keeps the gate from becoming the "enabled but
// inert" failure. A bot that stops on every chunk still decoding is a bot that
// answers chat and never takes a step, which is worse than walking off something
// occasionally.
func TestAnUnknownWorldIsNotACliff(t *testing.T) {
	t.Parallel()

	model := pathfinder.NewLocalWorldModel()
	// Bounds are what make "unknown" expressible at all. With none, the model
	// answers every unset cell at or below sea level with "solid", which is the
	// right guess for a bot joining a world it cannot see — and it means an
	// unbounded empty model is not a world with no ground, it is a world the
	// model is guessing about. Setting bounds is how a test says "this world
	// exists and I know nothing in it".
	//
	// The bounds start is deliberately far away: the model assumes the block
	// under its start position is solid so a path can begin from somewhere, and
	// probing that exact cell would read as known ground in an empty world.
	model.SetPathBounds(pathfinder.Node{X: 50, Y: 63, Z: 50}, pathfinder.Node{X: 58, Y: 63, Z: 50})

	ledge := movement.SenseLedge(model, mgl32.Vec3{0.5, 63, 0.5}, 90)
	if ledge.Known {
		t.Error("an empty world reported itself as known")
	}
	if ledge.IsCliff() {
		t.Error("an unknown world read as a cliff: the bot would freeze on every undecoded chunk")
	}
}

// TestANilWorldModelDoesNotPanic is the robustness case. The gate runs every
// tick at 20Hz, and a nil model during reconnect must not take the process down.
func TestANilWorldModelDoesNotPanic(t *testing.T) {
	t.Parallel()

	ledge := movement.SenseLedge(nil, mgl32.Vec3{0.5, 63, 0.5}, 90)
	if ledge.IsCliff() {
		t.Error("a nil world model read as a cliff")
	}
}
