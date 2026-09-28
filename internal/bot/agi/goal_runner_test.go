package agi

import (
	"testing"
	"time"

	"bedrock-ai/internal/jev"
)

// TestAdoptingAGoalNarrowsTheMenu is the end-to-end claim: once a goal exists,
// the brain stops offering activities that do not serve it. Without this the
// goal is decoration — the model would still pick from the full menu and the
// bot would be no more directed than before.
func TestAdoptingAGoalNarrowsTheMenu(t *testing.T) {
	t.Parallel()

	r := &Runner{}
	snap := Snapshot{NearBlocks: "oak_log", FreeSlots: 20, HP: 20, Hunger: 20, Now: time.Now()}

	before := r.menu(snap)
	if !hasActivity(before, jev.ActivityChat) {
		// Precondition: the unfiltered menu really does offer chat, otherwise
		// this test cannot show the goal removing it.
		t.Skip("unfiltered menu did not offer chat; nothing to narrow")
	}

	r.adoptGoal(jev.GoalGatherWood, snap.Now)

	after := r.menu(snap)
	if hasActivity(after, jev.ActivityChat) {
		t.Errorf("with goal gather_wood the menu still offers chat: %v", after)
	}
	if !hasActivity(after, jev.ActivityMine) {
		t.Errorf("with goal gather_wood the menu dropped mining: %v", after)
	}
}

// TestReAdoptingTheSameGoalKeepsProgress is what makes a goal a goal rather than
// a fresh start each tick. Resetting progress on every confirmation would make
// the counter meaningless and would make the bot look like it never gets
// anywhere.
func TestReAdoptingTheSameGoalKeepsProgress(t *testing.T) {
	t.Parallel()

	r := &Runner{}
	now := time.Now()

	r.adoptGoal(jev.GoalStockUp, now)
	r.goalProgress(jev.ActivityMine)
	r.goalProgress(jev.ActivityMine)
	if got := r.currentGoal().Progress; got != 2 {
		t.Fatalf("progress = %d after two advancing activities, want 2", got)
	}

	first := r.currentGoal()
	r.adoptGoal(jev.GoalStockUp, now.Add(time.Minute))

	second := r.currentGoal()
	if second.Progress != 2 {
		t.Errorf("re-adopting the same goal reset progress to %d, want 2", second.Progress)
	}
	if !second.Deadline.After(first.Deadline) {
		t.Error("re-adopting the same goal did not extend its deadline")
	}
	if second.Started != first.Started {
		t.Error("re-adopting the same goal reset its start time")
	}
}

// TestSwitchingGoalsResetsProgress is the other half: a genuinely new goal is a
// new beginning, and carrying the old count over would claim progress it never
// made.
func TestSwitchingGoalsResetsProgress(t *testing.T) {
	t.Parallel()

	r := &Runner{}
	now := time.Now()

	r.adoptGoal(jev.GoalStockUp, now)
	r.goalProgress(jev.ActivityMine)
	r.adoptGoal(jev.GoalExplore, now.Add(time.Minute))

	goal := r.currentGoal()
	if goal.Name != jev.GoalExplore {
		t.Fatalf("goal = %q, want explore", goal.Name)
	}
	if goal.Progress != 0 {
		t.Errorf("new goal inherited progress %d from the previous one", goal.Progress)
	}
}

// TestProgressOnlyCountsAdvancingActivities is the honesty rule: a bot must not
// be able to look busy while going nowhere.
func TestProgressOnlyCountsAdvancingActivities(t *testing.T) {
	t.Parallel()

	r := &Runner{}
	r.adoptGoal(jev.GoalGatherWood, time.Now())

	r.goalProgress(jev.ActivityChat) // not part of gathering wood
	if got := r.currentGoal().Progress; got != 0 {
		t.Errorf("chat scored as progress towards gather_wood (progress = %d)", got)
	}

	r.goalProgress(jev.ActivityMine)
	if got := r.currentGoal().Progress; got != 1 {
		t.Errorf("mining did not score as progress (progress = %d)", got)
	}
}

// TestGoalChangeNeedsConfidence guards the flip-flop failure. A model that is
// unsure must not be allowed to talk the bot out of what it is already doing —
// otherwise the bot oscillates between two intentions every tick, which looks
// busy and accomplishes nothing.
func TestGoalChangeNeedsConfidence(t *testing.T) {
	t.Parallel()

	applyGoal := func(current, choice string, confidence float64) string {
		r := &Runner{}
		now := time.Now()
		if current != "" {
			r.adoptGoal(current, now)
		}
		r.considerGoal(choice, confidence, now)
		got := r.currentGoal().Name
		if got == "" {
			return "<none>"
		}
		return got
	}

	// A confident change is adopted.
	if got := applyGoal(jev.GoalGatherWood, jev.GoalExplore, 0.9); got != jev.GoalExplore {
		t.Errorf("a confident goal change was refused; the bot kept %q", got)
	}
	// An unconfident change is refused, keeping the current goal.
	if got := applyGoal(jev.GoalGatherWood, jev.GoalExplore, 0.2); got != jev.GoalGatherWood {
		t.Errorf("an unconfident goal change was accepted; the bot became %q", got)
	}
	// With no current goal there is nothing to defend, so even a weak answer
	// establishes one — otherwise the bot could never start.
	if got := applyGoal("", jev.GoalExplore, 0.2); got != jev.GoalExplore {
		t.Errorf("a first goal was refused at low confidence; the bot stayed %q", got)
	}
}

// TestRepeatingAnOffGoalActivityStillStepsAside keeps the anti-loop guard
// working. The guard exists for a reason that outlives goals.
func TestRepeatingAnOffGoalActivityStillStepsAside(t *testing.T) {
	t.Parallel()

	r := &Runner{}
	// No goal: every repeat is off-goal and should be broken up.
	if got := r.alternativeActivity(jev.ActivityWander); got == jev.ActivityWander {
		t.Error("repeating an activity with no goal active was not stepped aside")
	}
}

// TestRepeatingAnOnGoalActivityIsAllowed is the exception that makes goals
// achievable. Mining three times in a row while trying to get wood is the whole
// point, not a loop.
func TestRepeatingAnOnGoalActivityIsAllowed(t *testing.T) {
	t.Parallel()

	r := &Runner{}
	r.adoptGoal(jev.GoalGatherWood, time.Now())

	if got := r.alternativeActivity(jev.ActivityMine); got != jev.ActivityMine {
		t.Errorf("repeating mining while pursuing gather_wood was broken up into %q", got)
	}
	// But an activity that does not serve the goal is still stepped aside.
	if got := r.alternativeActivity(jev.ActivityWander); got == jev.ActivityWander {
		t.Error("wandering was not stepped aside while pursuing gather_wood")
	}
}

// hasActivity reports whether a menu contains an entry.
func hasActivity(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}
