package scaffold_test

import (
	"testing"

	"bedrock-ai/internal/bot/scaffold"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The bot kept placing blocks in front of itself while jumping, and then swung
// its arm with nothing appearing for the second block of a pillar.
//
// Both came from the same line of code. To work out what a "place" step means,
// the old code read where the bot's BODY was and built the placement from that.
// When the body lagged the path by a block, the block went to the wrong level;
// when the columns differed, it went beside the bot instead of under it. And the
// jump was tied to the same misreading, so the second block of a climb was
// placed with no jump at all — the server refuses a placement aimed at the cell
// the player is standing in, which is exactly what it was aiming at.
//
// So the geometry is now derived from the node, once, and shared by the planner
// and the executor. These tests pin it down.

func TestAStepNeedsItsSupportOneBelowAndAFaceOneBelowThat(t *testing.T) {
	t.Parallel()

	// A node is a place the bot STANDS. Its feet end up at the node's own Y, so
	// the block it needs is the one below, and the face it clicks is the one
	// below that.
	node := protocol.BlockPos{-40, 82, 230}
	step := scaffold.NewStep(node)

	wantSupport := protocol.BlockPos{-40, 81, 230}
	if step.Support != wantSupport {
		t.Errorf("Support = %v, want the cell one below the node %v", step.Support, wantSupport)
	}
	wantClick := protocol.BlockPos{-40, 80, 230}
	if step.Click != wantClick {
		t.Errorf("Click = %v, want the cell two below the node %v", step.Click, wantClick)
	}
	if step.Feet != node {
		t.Errorf("Feet = %v, want the node itself %v", step.Feet, node)
	}
}

func TestABotStandingInTheColumnHasToJump(t *testing.T) {
	t.Parallel()

	// The cell being filled is the one the body is in. A player jumps here and
	// places on the way up; a bot that does not is aiming at itself and the
	// server refuses the placement.
	step := scaffold.NewStep(protocol.BlockPos{-40, 82, 230})
	b := newFakeBot(nil)
	b.pos = mgl32.Vec3{-39.5, 81, 230.5}

	if !scaffold.BodyBlocksCell(b.GetCoords(), step.Support) {
		t.Error("BodyBlocksCell = false for a bot standing in the column, want true: this is the case that needs a jump")
	}
}

func TestABotStandingBesideTheColumnMustNotJump(t *testing.T) {
	t.Parallel()

	// The other half of the same bug. Filling a gap from next to it needs no jump,
	// and jumping anyway is what made the bot hop while putting a block somewhere
	// it was not standing.
	step := scaffold.NewStep(protocol.BlockPos{-40, 82, 230})
	b := newFakeBot(nil)
	b.pos = mgl32.Vec3{-39.5, 81, 231.5}

	if scaffold.BodyBlocksCell(b.GetCoords(), step.Support) {
		t.Error("BodyBlocksCell = true for a bot a block away, want false: nothing is in the way, so no jump")
	}
}

func TestABotFarBelowTheColumnIsNotInTheWay(t *testing.T) {
	t.Parallel()

	// Height has to matter as well as column. A bot on the ground two storeys down
	// in the same column is nowhere near the cell.
	step := scaffold.NewStep(protocol.BlockPos{-40, 82, 230})
	b := newFakeBot(nil)
	b.pos = mgl32.Vec3{-39.5, 70, 230.5}

	if scaffold.BodyBlocksCell(b.GetCoords(), step.Support) {
		t.Error("BodyBlocksCell = true for a bot two storeys below, want false")
	}
}

func TestTheColumnToleranceIsNotTheWholeWorld(t *testing.T) {
	t.Parallel()

	// One and a half blocks away is beside, not in. A tolerance wide enough to
	// call that "in the column" would put the jump back on every gap fill.
	step := scaffold.NewStep(protocol.BlockPos{-40, 82, 230})
	b := newFakeBot(nil)
	b.pos = mgl32.Vec3{-39.5, 81, 232.0}

	if scaffold.BodyBlocksCell(b.GetCoords(), step.Support) {
		t.Error("BodyBlocksCell = true a block and a half away, want false")
	}
}

// TestObsidianAndBedrockAreNotWorthBreaking covers the class that no tool ever
// takes out. Obsidian is not in it any more — it is a wall for a bot with a
// wooden pickaxe and a nine-second job for a bot with a diamond one, and a table
// with no tools in it could only say one of those two things.
func TestTheUnbreakableWallsAreNotWorthBreaking(t *testing.T) {
	t.Parallel()

	// Tested at the best tier available, so a failure here means the block is
	// unconditionally refused rather than merely out of reach of this bot.
	for _, block := range []string{"bedrock", "nether_portal", "end_portal_frame", "command_block"} {
		if scaffold.WorthBreakingWith(block, scaffold.TierNetherite) {
			t.Errorf("WorthBreakingWith(%q, netherite) = true, want false: no tool takes this out", block)
		}
	}
}

func TestOrdinaryTerrainIsWorthBreaking(t *testing.T) {
	t.Parallel()

	// The control. A block the bot will not break is a block the bot walks around,
	// and if that list is "everything" the bot stops climbing entirely.
	for _, block := range []string{"dirt", "stone", "oak_log", "cobblestone", "oak_fence", "chest"} {
		if !scaffold.WorthBreaking(block) {
			t.Errorf("WorthBreaking(%q) = false, want true: a scaffold step may break this", block)
		}
	}
}

// TestBreakThroughStillRespectsTheToolTier is the same rule one tier down. The
// tower asks for breakThrough and passes TierHand because it has no detour, and
// "through" is still not a licence to spend four minutes on obsidian: it gets
// the "go around" answer, and a bot that then finds itself a diamond pickaxe
// gets a different one.
func TestBreakThroughStillRespectsTheToolTier(t *testing.T) {
	t.Parallel()

	b := newFakeBot(map[[3]int32]string{{-40, 81, 230}: "obsidian"})

	ok, reason := scaffold.ClearCell(t.Context(), b, protocol.BlockPos{-40, 81, 230}, true, scaffold.TierHand)
	if ok {
		t.Error("ClearCell cleared obsidian bare-handed even with breakThrough, want the cell left alone")
	}
	if reason == "" {
		t.Error("ClearCell gave no reason, so the log would not say why the bot stopped")
	}

	// The same block, the same breakThrough, one tier up: this is the case the
	// old unconditional table refused, and refusing it left a bot with a diamond
	// pickaxe standing in front of a floor it could have opened in nine seconds.
	picker := newFakeBot(map[[3]int32]string{{-40, 81, 230}: "obsidian"})
	ok, reason = scaffold.ClearCell(t.Context(), picker, protocol.BlockPos{-40, 81, 230}, true, scaffold.TierDiamond)
	if !ok {
		t.Errorf("ClearCell refused obsidian to a bot with a diamond pickaxe: %s", reason)
	}
}

func TestThereHasToBeSomethingToPlaceOnto(t *testing.T) {
	t.Parallel()

	// A placement is a click on a face. With nothing underneath there is no face,
	// the transaction goes out aimed at nothing, and the server refuses it — one
	// of the reasons a climb only sometimes worked.
	empty := newFakeBot(map[[3]int32]string{{-40, 80, 230}: "air"})
	if ok, _ := scaffold.SupportBelowReady(empty, protocol.BlockPos{-40, 80, 230}); ok {
		t.Error("SupportBelowReady = true with nothing underneath, want false")
	}

	grass := newFakeBot(map[[3]int32]string{{-40, 80, 230}: "short_grass"})
	if ok, _ := scaffold.SupportBelowReady(grass, protocol.BlockPos{-40, 80, 230}); ok {
		t.Error("SupportBelowReady = true on grass, want false: a plant cannot hold a block")
	}

	solid := newFakeBot(map[[3]int32]string{{-40, 80, 230}: "dirt"})
	if ok, reason := scaffold.SupportBelowReady(solid, protocol.BlockPos{-40, 80, 230}); !ok {
		t.Errorf("SupportBelowReady = false on dirt, want true (%s)", reason)
	}
}
