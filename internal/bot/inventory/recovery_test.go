package inventory

import "testing"

// The gap between what the bot believes it is holding and what the server says
// it is holding is where the silent failures live: the bot crafts with a stack
// it does not have, or leaves an item on the cursor where no slot operation can
// see it, and the resulting error points three steps away from the cause.
//
// These tests pin the diagnosis order and the recovery choices, over a plain
// view — because inventory drift cannot be reproduced on demand, and a policy
// that can only be exercised by genuinely losing items is a policy that never
// gets exercised.

func view(mutate func(*View)) View {
	v := View{
		SlotCounts: map[uint32]int{0: 64, 1: 10},
	}
	mutate(&v)
	return v
}

// TestAStuckCursorOutranksEverything is the ordering claim. An item on the
// cursor is invisible to every slot operation, so a resync issued while it is
// still attached reports a view the bot cannot act on, and a container closed
// under an in-flight transfer loses the transfer's result. It goes first.
func TestAStuckCursorOutranksEverything(t *testing.T) {
	t.Parallel()

	v := view(func(v *View) {
		v.CursorStack = "minecraft:diamond"
		v.SlotCounts[0] = 999 // also drifted, and also wrong
		v.StaleContainer()
	})
	v.ContainerOpen = true
	v.LastRequestFailed = true

	problem, recovery := Plan(v)
	if problem != ProblemCursorStuck {
		t.Errorf("problem = %v, want ProblemCursorStuck; everything else is unreliable until this is fixed", problem)
	}
	if recovery != RecoverCursor {
		t.Errorf("recovery = %v, want RecoverCursor", recovery)
	}
}

// TestDriftIsOnlyDriftWhenBothSidesHaveAnOpinion is the false-positive guard.
// A slot the bot has never heard of is a gap in its knowledge, not a mismatch,
// and treating that as drift would make a freshly-joined bot resync on every
// tick forever.
func TestDriftIsOnlyDriftWhenBothSidesHaveAnOpinion(t *testing.T) {
	t.Parallel()

	// No confirmation at all: the normal state right after joining.
	never := view(func(v *View) { v.ConfirmedSlotCounts = nil })
	if never.Drifted() {
		t.Error("an unconfirmed view was reported as drifted")
	}
	if problem := Diagnose(never); problem != ProblemNone {
		t.Errorf("problem = %v with no confirmation to compare against, want ProblemNone", problem)
	}

	// Confirmed, and the two agree on every slot the server mentioned.
	agreed := view(func(v *View) { v.ConfirmedSlotCounts = map[uint32]int{0: 64} })
	if agreed.Drifted() {
		t.Error("a matching view was reported as drifted")
	}

	// A non-nil confirmation is a complete snapshot, so a slot in it that the
	// bot has no entry for is an item the bot is about to act without. That is
	// the bug this file exists for, not a gap to be shrugged off.
	missing := view(func(v *View) { v.ConfirmedSlotCounts = map[uint32]int{0: 64, 7: 3} })
	if !missing.Drifted() {
		t.Error("an item the server has and the bot does not know about was not flagged")
	}
}

// TestARealMismatchIsCaught is the case that matters. The bot believes it has
// 64 cobblestone and the server says 12; the next thing it does is craft with
// the difference.
func TestARealMismatchIsCaught(t *testing.T) {
	t.Parallel()

	v := view(func(v *View) { v.ConfirmedSlotCounts = map[uint32]int{0: 12, 1: 10} })

	if !v.Drifted() {
		t.Fatal("a genuine count mismatch was not detected")
	}
	problem, recovery := Plan(v)
	if problem != ProblemCountDrift || recovery != RecoverResync {
		t.Errorf("got %v/%v, want ProblemCountDrift/RecoverResync", problem, recovery)
	}

	drifted := v.DriftedSlots()
	if len(drifted) != 1 || drifted[0] != 0 {
		t.Errorf("DriftedSlots = %v, want just [0]", drifted)
	}
}

// TestDriftedSlotsAreSorted pins the log-readability rule that runs through this
// codebase: an unordered list of problems is a log that reads differently every
// time, which makes it impossible to tell whether anything actually changed.
func TestDriftedSlotsAreSorted(t *testing.T) {
	t.Parallel()

	v := view(func(v *View) {
		v.SlotCounts = map[uint32]int{9: 1, 2: 5, 30: 7, 1: 3}
		v.ConfirmedSlotCounts = map[uint32]int{9: 99, 2: 99, 30: 99, 1: 99}
	})

	for i := 0; i < 20; i++ {
		drifted := v.DriftedSlots()
		want := []uint32{1, 2, 9, 30}
		if len(drifted) != len(want) {
			t.Fatalf("DriftedSlots = %v, want %v", drifted, want)
		}
		for j := range want {
			if drifted[j] != want[j] {
				t.Fatalf("DriftedSlots = %v, want %v", drifted, want)
			}
		}
	}
}

// TestAContainerLeftOpenAfterAFailedRequestIsStale is the specific leak. The
// session it is writing to belongs to an exchange that already timed out, so
// every transfer against it fails with a rejection the bot cannot explain.
func TestAContainerLeftOpenAfterAFailedRequestIsStale(t *testing.T) {
	t.Parallel()

	stale := view(func(v *View) { v.ContainerOpen = true; v.LastRequestFailed = true })
	if !stale.StaleContainer() {
		t.Error("an open container with a dead request was not reported as stale")
	}
	problem, recovery := Plan(stale)
	if problem != ProblemStaleContainer || recovery != RecoverCloseContainer {
		t.Errorf("got %v/%v, want ProblemStaleContainer/RecoverCloseContainer", problem, recovery)
	}

	// An open container with a live request is the normal case, not a fault.
	healthy := view(func(v *View) { v.ContainerOpen = true; v.LastRequestFailed = false })
	if healthy.StaleContainer() {
		t.Error("a container the bot is actively using was reported as stale")
	}
	if problem := Diagnose(healthy); problem != ProblemNone {
		t.Errorf("problem = %v during a normal container session, want ProblemNone", problem)
	}
}

// TestAHealthyInventoryIsLeftAlone is the false-positive guard for the whole
// file. A recovery policy that fires when nothing is wrong is a policy that
// makes things worse.
func TestAHealthyInventoryIsLeftAlone(t *testing.T) {
	t.Parallel()

	healthy := view(func(v *View) {
		v.ConfirmedSlotCounts = map[uint32]int{0: 64, 1: 10}
	})
	problem, recovery := Plan(healthy)
	if problem != ProblemNone || recovery != RecoverNone {
		t.Errorf("got %v/%v on a healthy inventory, want ProblemNone/RecoverNone", problem, recovery)
	}
	if healthy.DriftedSlots() != nil {
		t.Error("DriftedSlots reported problems on a healthy inventory")
	}
}

// TestTheServerIsRightAboutCounts pins the merge policy. A count is a fact the
// server owns; an argument about it is a bug on this side.
func TestTheServerIsRightAboutCounts(t *testing.T) {
	t.Parallel()

	merged := MergeConfirmed(
		map[uint32]int{0: 64, 1: 10, 4: 5}, // what the bot believed
		map[uint32]int{0: 12, 1: 10, 2: 3}, // what the server said
	)

	if merged[0] != 12 {
		t.Errorf("slot 0 = %d, want the server's 12, not the bot's 64", merged[0])
	}
	if merged[1] != 10 {
		t.Errorf("slot 1 = %d, want 10", merged[1])
	}
	if merged[2] != 3 {
		t.Errorf("slot 2 = %d, want the server's new slot at 3", merged[2])
	}
	// Slot 4 was never mentioned by the server. That means "not confirmed",
	// not "empty", and throwing the belief away for everything unconfirmed would
	// empty a whole inventory the server simply has not enumerated yet.
	if merged[4] != 5 {
		t.Errorf("slot 4 = %d, want the bot's unconfirmed belief at 5 to survive", merged[4])
	}
}

// TestMergeDropsSlotsTheServerEmptied is the other half. A slot the server says
// is empty has to go, or a phantom entry lingers and the bot goes looking for
// something that is not there.
func TestMergeDropsSlotsTheServerEmptied(t *testing.T) {
	t.Parallel()

	merged := MergeConfirmed(
		map[uint32]int{0: 64, 3: 8},
		map[uint32]int{0: 64, 3: 0},
	)

	if _, present := merged[3]; present {
		t.Error("a slot the server reported as empty survived the merge as a phantom entry")
	}
	if merged[0] != 64 {
		t.Errorf("slot 0 = %d, want it untouched at 64", merged[0])
	}
}

// TestMergeDoesNotMutateItsInputs keeps the merge safe to call against the
// bot's live inventory map. A merge that aliased its argument would quietly
// rewrite the thing it was asked to read.
func TestMergeDoesNotMutateItsInputs(t *testing.T) {
	t.Parallel()

	believed := map[uint32]int{0: 64}
	confirmed := map[uint32]int{0: 12}

	merged := MergeConfirmed(believed, confirmed)
	merged[5] = 99

	if believed[0] != 64 {
		t.Error("MergeConfirmed rewrote the bot's own view")
	}
	if confirmed[0] != 12 {
		t.Error("MergeConfirmed rewrote the server's view")
	}
	if _, leaked := believed[5]; leaked {
		t.Error("the merged map aliases the bot's map")
	}
}
