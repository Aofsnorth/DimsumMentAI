package movement_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// A scaffold placement the server honoured still has to be told to the world
// model, because the world model is what the physics asks whether the bot is
// standing on anything.
//
// The live failure was exactly this. The bot placed a block, the server
// confirmed it, and the log said "scaffold: support placed" — and then the next
// step up failed with "the body never cleared the cell", three times, gave up,
// replanned, produced the identical path, and failed identically forever.
//
// The chain is short and every link is in the log:
//
//	placed [-40 81 231], server confirmed, world model never told
//	  -> IsGrounded=false, because physics asks WorldModel.IsSolid under the feet
//	    -> takeRequestedJump refuses to consume the request (packet.go:310)
//	      -> JumpAndWait polls for 800ms and gives up
//	        -> the cell is never cleared
//
// The repair in the movement loop cannot break the cycle either: it only runs
// on a tick the bot is already grounded, and grounding is the thing that is
// broken. So the write has to come from the placement itself.

func newScaffoldWorldBot() *bot.Bot {
	return &bot.Bot{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Pos:    mgl32.Vec3{-39.5, 82, 231.5},
	}
}

// TestAConfirmedPlacementIsSolidInTheWorldModel is the write itself. Without it
// the bot is standing on air by its own account, and nothing downstream can
// recover.
func TestAConfirmedPlacementIsSolidInTheWorldModel(t *testing.T) {
	t.Parallel()

	b := newScaffoldWorldBot()
	b.WorldModel = pathfinder.NewLocalWorldModel()

	support := protocol.BlockPos{-40, 81, 231}
	movement.RecordPlacedSupportForTest(b, support)

	if !b.WorldModel.IsSolid(support.X(), support.Y(), support.Z()) {
		t.Error("a placement the server confirmed was not solid in the world model")
	}
}

// TestTheBotIsGroundedOnABlockItJustPlaced is the consequence, and the thing
// the jump depends on. The bot's feet are at y=82, so the cell under them is
// [-40 81 231] — the block it just placed.
func TestTheBotIsGroundedOnABlockItJustPlaced(t *testing.T) {
	t.Parallel()

	b := newScaffoldWorldBot()
	b.WorldModel = pathfinder.NewLocalWorldModel()
	feet := mgl32.Vec3{-39.5, 82, 231.5}

	if movement.IsGroundedInWorldModelForTest(b, feet) {
		t.Fatal("the bot reads as grounded on empty air; the test is not modelling the bug")
	}

	movement.RecordPlacedSupportForTest(b, protocol.BlockPos{-40, 81, 231})

	if !movement.IsGroundedInWorldModelForTest(b, feet) {
		t.Error("the bot stood on a block it had just placed and still reads as not grounded: the jump request will never be consumed")
	}
}

// TestAnUnrecordedPlacementLeavesTheBotUngrounded pins the failure rather than
// the fix. If a future change to the world model makes an empty cell read as
// solid, this is the test that says the whole chain was resting on a lie.
func TestAnUnrecordedPlacementLeavesTheBotUngrounded(t *testing.T) {
	t.Parallel()

	b := newScaffoldWorldBot()
	b.WorldModel = pathfinder.NewLocalWorldModel()

	// The support the log showed: placed, confirmed, never recorded.
	if movement.IsGroundedInWorldModelForTest(b, mgl32.Vec3{-39.5, 82, 231.5}) {
		t.Error("a world model with nothing in it reports solid ground: " +
			"the grounded test cannot be trusted to catch an unrecorded placement")
	}
}

// TestAStaircaseOfPlacementsKeepsTheBotGrounded walks the shape the bot was
// actually trying to climb — four blocks straight up in one column — because a
// single block proves the write and the staircase proves the climb.
func TestAStaircaseOfPlacementsKeepsTheBotGrounded(t *testing.T) {
	t.Parallel()

	b := newScaffoldWorldBot()
	b.WorldModel = pathfinder.NewLocalWorldModel()

	for y := int32(81); y <= 84; y++ {
		support := protocol.BlockPos{-40, y, 231}
		movement.RecordPlacedSupportForTest(b, support)

		// After placing at y the bot stands with its feet at y+1.
		feet := mgl32.Vec3{-39.5, float32(y) + 1, 231.5}
		if !movement.IsGroundedInWorldModelForTest(b, feet) {
			t.Fatalf("after placing %v the bot is not grounded on it at feet y=%.0f: the next step up cannot be jumped", support, feet.Y())
		}
	}
}

// TestTheRecordingIsIdempotent matters because the same support is confirmed
// again on the tick after a placement — CellSatisfied normally short-circuits it,
// but a path rebuilt around the column lands on the same node and re-runs the
// step. Recording the same block twice must be a no-op, not a double entry.
func TestTheRecordingIsIdempotent(t *testing.T) {
	t.Parallel()

	b := newScaffoldWorldBot()
	b.WorldModel = pathfinder.NewLocalWorldModel()
	support := protocol.BlockPos{-40, 81, 231}

	movement.RecordPlacedSupportForTest(b, support)
	movement.RecordPlacedSupportForTest(b, support)

	if !b.WorldModel.IsSolid(support.X(), support.Y(), support.Z()) {
		t.Error("recording the same placement twice lost the block")
	}
}
