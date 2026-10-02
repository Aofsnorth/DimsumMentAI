package scaffold_test

import (
	"testing"

	"bedrock-ai/internal/bot/scaffold"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The bot spent a run wedged on a single block, telling the player it had failed
// fifty times a second, and the cause was a question nobody had asked.
//
// A tower places the block it is about to stand on. The next tick the same path
// node runs again, and now the cell holds the dirt the bot itself just put
// there. "Is the cell empty?" answers no, and nothing in the loop moved the path
// along, so the node came back forever.
//
// The missing question is the other one: is the cell already full of something
// the bot can stand on? If so the step is done, not broken.

var cell = protocol.BlockPos{-41, 81, 231}

func TestACellHoldingTheBlocksOwnDirtIsAlreadySatisfied(t *testing.T) {
	t.Parallel()

	// Exactly the live world: the bot's own block from the previous tick.
	b := newFakeBot(map[[3]int32]string{{-41, 81, 231}: "dirt"})

	name, satisfied := scaffold.CellSatisfied(b, cell)
	if !satisfied {
		t.Fatal("CellSatisfied = false, want true: the cell already holds the support the step asked for")
	}
	if name != "dirt" {
		t.Errorf("name = %q, want %q so the log says what the cell holds", name, "dirt")
	}
}

func TestAnEmptyCellIsNotSatisfied(t *testing.T) {
	t.Parallel()

	b := newFakeBot(map[[3]int32]string{{-41, 81, 231}: "air"})

	if _, satisfied := scaffold.CellSatisfied(b, cell); satisfied {
		t.Error("CellSatisfied = true on an empty cell, want false: there is still a block to place")
	}
}

func TestAGrassCellIsNotSatisfiedBecauseItStillHasToBeCleared(t *testing.T) {
	t.Parallel()

	// A tuft of grass is occupied, but the bot would be standing on the grass
	// rather than on solid ground. Calling that satisfied is the bug this
	// function exists to avoid, in the other direction.
	b := newFakeBot(map[[3]int32]string{{-41, 81, 231}: "short_grass"})

	if _, satisfied := scaffold.CellSatisfied(b, cell); satisfied {
		t.Error("CellSatisfied = true on grass, want false: grass is cleared and replaced, not stood on")
	}
}

func TestALadderCellIsNotSatisfiedBecauseALadderIsNotAFloor(t *testing.T) {
	t.Parallel()

	// The obvious over-correction. Once "occupied means done" is the rule, a
	// ladder in the cell would satisfy the step and the bot would walk past the
	// one thing in the world that would have let it climb.
	b := newFakeBot(map[[3]int32]string{{-41, 81, 231}: "ladder"})

	if _, satisfied := scaffold.CellSatisfied(b, cell); satisfied {
		t.Error("CellSatisfied = true on a ladder, want false: a ladder is something to climb, not to stand on")
	}
}

func TestAFenceCellIsNotSatisfiedForTheSameReason(t *testing.T) {
	t.Parallel()

	b := newFakeBot(map[[3]int32]string{{-41, 81, 231}: "oak_fence"})

	if _, satisfied := scaffold.CellSatisfied(b, cell); satisfied {
		t.Error("CellSatisfied = true on a fence, want false")
	}
}

func TestAnUnloadedCellIsNotSatisfied(t *testing.T) {
	t.Parallel()

	// The bot cannot see it, so it must not claim the step is done. Treating
	// "I cannot see it" as "it is fine" would skip the placement and walk the
	// bot off a block that was never there.
	b := newFakeBot(nil)

	if _, satisfied := scaffold.CellSatisfied(b, cell); satisfied {
		t.Error("CellSatisfied = true on an unseen cell, want false")
	}
}

func TestTheSatisfiedCellIsNamedTheWayTheOccupantIs(t *testing.T) {
	t.Parallel()

	// Names arrive namespaced and mixed-case from the world cache; the rest of
	// the bot keys on the bare lower-case name, and so must this.
	b := newFakeBot(map[[3]int32]string{{-41, 81, 231}: "minecraft:Stone"})

	name, satisfied := scaffold.CellSatisfied(b, cell)
	if !satisfied {
		t.Fatal("CellSatisfied = false on a stone block, want true")
	}
	if name != "stone" {
		t.Errorf("name = %q, want %q", name, "stone")
	}
}
