package movement_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// The drop gate has two halves and only one of them had ever been tested.
//
// ledge_test.go proves SenseLedge measures a cliff correctly. That is not the
// behaviour. The behaviour is the body refusing the step, and nothing drove the
// real steering path to check it — which is why a cliff has never once been seen
// to stop anything in a live run, and why nothing could say whether the gate
// works or is merely correct about geometry nobody consults.
//
// These drive the actual steering step.

func cliffWorld(t *testing.T) *bot.Bot {
	t.Helper()

	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(pathfinder.Node{X: 0, Y: 63, Z: 0}, pathfinder.Node{X: 8, Y: 63, Z: 0})
	// Flat ground with a hole starting three blocks east, and a floor well
	// below it. Eight blocks of air is a drop the gate exists to refuse.
	for x := int32(-8); x <= 8; x++ {
		for z := int32(-8); z <= 8; z++ {
			model.SetSolid(x, 62, z, true)
		}
	}
	for x := int32(3); x <= 8; x++ {
		model.SetSolid(x, 62, 0, false)
		model.SetSolid(x, 55, 0, true)
	}

	return &bot.Bot{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		WorldModel:    model,
		Pos:           mgl32.Vec3{0.5, 63, 0.5},
		MovementState: "walk_to",
	}
}

// newWalkingTick builds a tick that wants to go forward, facing east — into the
// hole.
func newWalkingTick(b *bot.Bot) *movement.TickContext {
	return &movement.TickContext{
		B:       b,
		CurrPos: mgl32.Vec3{0.5, 63, 0.5},
		Yaw:     90,
		MState:  "walk_to",
		HasPath: true,
		// Both fields matter and both were missing at first. Without them
		// UpdateShouldMoveState reports "do not move" and the steering step
		// returns before the gate is ever reached — so a test asserting "the
		// body did not move" passes without proving anything at all.
		//
		// That is the shape of a vacuous test, and it is the whole reason the
		// cliff has never been seen to stop anything: the measurement was
		// proven, and nobody ever asked the body.
		AllowDirectSteering: true,
	}
}

// TestTheBodyRefusesToWalkOffACliff is the behaviour, not the measurement.
//
// It drives the real steering step with a body facing a hole, and asserts the
// body did not take a horizontal step. Everything else in this feature was
// satisfied by the cliff being measured correctly; this is the part where the
// bot survives it.
func TestTheBodyRefusesToWalkOffACliff(t *testing.T) {
	t.Parallel()

	b := cliffWorld(t)
	tc := newWalkingTick(b)

	tc.SteerForTest()

	if tc.HasHorizontalMove {
		t.Error("the body took a horizontal step off an eight-block drop: " +
			"the gate measures the cliff and then lets the walk happen anyway")
	}
}

// TestTheBodyStillWalksOnFlatGround is the control that stops the gate from
// being "stop always". A bot that refuses to move is not a cautious bot; it is
// a broken one, and this is the test that says which failure you have.
func TestTheBodyStillWalksOnFlatGround(t *testing.T) {
	t.Parallel()

	b := cliffWorld(t)
	// Face away from the hole.
	tc := newWalkingTick(b)
	tc.Yaw = 270

	tc.SteerForTest()

	if !tc.HasHorizontalMove {
		t.Error("the body refused to walk on flat ground: the gate has become a wall")
	}
}

// TestAnAuthorisedDropIsTaken is the other half of the gate, and the reason it
// exists at all: a bot that cannot be told to jump is walking a staircase in
// both directions. The authority must actually let the body through.
func TestAnAuthorisedDropIsTaken(t *testing.T) {
	t.Parallel()

	b := cliffWorld(t)
	tc := newWalkingTick(b)
	tc.DropOK = true

	tc.SteerForTest()

	if !tc.HasHorizontalMove {
		t.Error("the body refused a drop the model explicitly asked for: " +
			"a bot that cannot be told to jump never explores")
	}
}

// TestTheAuthorityIsSpentOnUse stops it becoming a standing permission. One
// authorisation, one leap — otherwise "leap this" becomes "leap everything, from
// now on", which is the exact opposite of a decision.
func TestTheAuthorityIsSpentOnUse(t *testing.T) {
	t.Parallel()

	b := cliffWorld(t)
	tc := newWalkingTick(b)
	tc.DropOK = true

	if !tc.DropAuthorised() {
		t.Fatal("the authority was refused before it could be spent")
	}
	if tc.DropAuthorised() {
		t.Error("the same authority was honoured twice: one decision became a permanent permission")
	}
}

// TestTheGateAsksTheBodyNotTheModel is the wiring that makes this testable at
// all. The gate runs inside the movement tick, so a test that called the model
// instead would be asserting about a different program.
func TestTheGateAsksTheBodyNotTheModel(t *testing.T) {
	t.Parallel()

	b := cliffWorld(t)
	tc := newWalkingTick(b)

	if !tc.DropsOverTest() {
		t.Fatal("the tick does not recognise the cliff in front of it: " +
			"the gate would never fire in a run, whatever the geometry says")
	}
}
