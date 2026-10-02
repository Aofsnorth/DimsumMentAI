package bot_test

import (
	"testing"

	"bedrock-ai/internal/bot"
)

// "Go around" is only the right answer while there is somewhere to go.
//
// A block the bot cannot cheaply remove overhead is a block it should route
// past, and the path drop that makes that happen is already there. The other
// half is the world with no other way: the path is dropped, the planner rebuilds
// around the obstruction, and the route comes back to the same ceiling. At that
// point the only difference between breaking through and standing still is that
// one of them ends, so the counter below is what turns a refusal into an action
// — and only after enough rounds to know the detour was tried.

// TestTheFirstDetourIsNeverTheLastOne keeps the default honest. A bot that
// tunnels through obsidian the first time it meets one has not avoided a cost,
// it has just refused to look for a shorter route.
func TestTheFirstDetourIsNeverTheLastOne(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()

	if b.NoteHeadroomDetour(1, 2, 3) {
		t.Error("the first detour at a node was treated as the last one: the bot would tunnel on sight")
	}
}

// TestTheLastDetourIsReachedAfterEnoughRounds is the other half: the bot does
// eventually stop asking. A counter that never fires is a bot that stands in
// front of a wall for the rest of the session.
func TestTheLastDetourIsReachedAfterEnoughRounds(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()

	var last bool
	for i := 0; i < bot.MaxHeadroomDetours; i++ {
		last = b.NoteHeadroomDetour(1, 2, 3)
	}
	if !last {
		t.Errorf("after %d detours at one node the bot was still looking for a way around", bot.MaxHeadroomDetours)
	}
}

// TestADifferentNodeGetsAFreshBudget stops the counter from becoming global. A
// bot that detoured once at a boulder must not tunnel through the next ceiling
// it meets, or the very first obstacle in the world teaches it to break
// everything.
func TestADifferentNodeGetsAFreshBudget(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()

	for i := 0; i < bot.MaxHeadroomDetours-1; i++ {
		b.NoteHeadroomDetour(1, 2, 3)
	}

	if b.NoteHeadroomDetour(9, 9, 9) {
		t.Error("a node the bot had never tried was treated as an exhausted one")
	}
	if b.NoteHeadroomDetour(1, 2, 3) {
		t.Error("returning to the original node lost its detour count")
	}
}

// TestTheCounterIsPerColumnAndNotPerTrip keeps the x/y/z comparison honest. A
// counter that matched on one axis would carry a node's detours into its
// neighbour one block up, which is exactly the staircase a climb builds.
func TestTheCounterIsPerColumnAndNotPerTrip(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()

	b.NoteHeadroomDetour(1, 2, 3)

	for _, node := range [][3]int32{{2, 2, 3}, {1, 3, 3}, {1, 2, 4}} {
		if b.NoteHeadroomDetour(node[0], node[1], node[2]) {
			t.Errorf("neighbouring node %v inherited the detour count", node)
		}
	}
}

// TestAnEmptyCellKeepsItsDetourBudget is the one that would have bitten first in
// play. A climb asks about the cell above every single step, and four blocks of
// open sky is four steps — so a counter that spends on empty cells is exhausted
// before the bot has seen a single obstruction, and the first obsidian it ever
// meets gets tunnelled instead of walked around. That is the exact opposite of
// what the budget is for.
func TestAnEmptyCellKeepsItsDetourBudget(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()

	// Four clear steps up a wall, each taking a count and giving it back.
	for range 4 {
		if b.NoteHeadroomDetour(1, 2, 3) {
			t.Fatal("an open cell was treated as an exhausted one")
		}
		b.RefundHeadroomDetour(1, 2, 3)
	}

	// The first real obstruction still gets the full budget.
	if b.NoteHeadroomDetour(1, 2, 3) {
		t.Error("climbing open sky exhausted the detour budget")
	}
}

// TestARefundCannotDriveTheCounterNegative keeps the arithmetic honest. A refund
// with no matching count must not hand out a negative budget that the next
// comparison then reads as "many detours already spent".
func TestARefundCannotDriveTheCounterNegative(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()

	for range 5 {
		b.RefundHeadroomDetour(1, 2, 3)
	}

	// One real detour, then the last resort must be the second, not the first.
	if b.NoteHeadroomDetour(1, 2, 3) {
		t.Error("a run of refunds left the node already at its last resort")
	}
}

// TestARefundForAnotherNodeIsIgnored stops a stale refund from crediting a node
// that never spent anything. The caller's coordinates are what makes a refund
// belong to a node, and a mismatch is a programming error rather than a
// harmless no-op.
func TestARefundForAnotherNodeIsIgnored(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()

	// One detour spent here.
	b.NoteHeadroomDetour(1, 2, 3)

	// A refund aimed at some other node must not touch this one. If it were
	// applied, the node below would still need the full budget, and the count
	// of rounds-to-last-resort would come out one higher than it should.
	b.RefundHeadroomDetour(9, 9, 9)

	notes := 1
	for {
		notes++
		if b.NoteHeadroomDetour(1, 2, 3) {
			break
		}
	}
	if notes != bot.MaxHeadroomDetours {
		t.Errorf("node reached last resort after %d detours, want %d: a refund aimed elsewhere was credited to it",
			notes, bot.MaxHeadroomDetours)
	}
}
