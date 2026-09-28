package agi

import (
	"testing"
	"time"

	"bedrock-ai/internal/jev"
)

// TestAvailableGoalsNeverOfferTheImpossible is the rule the whole feature rests
// on: the model must never be asked to pursue something that cannot succeed.
// "Get wood" with a full inventory and no wood in sight is a goal that spends a
// decision on nothing.
func TestAvailableGoalsNeverOfferTheImpossible(t *testing.T) {
	t.Parallel()

	// Nothing to gather, nobody around, not night: the only honest goal is idle.
	bare := Snapshot{NearBlocks: "none", FreeSlots: 36, HP: 20, Hunger: 20}
	goals := AvailableGoals(bare)
	if len(goals) != 2 {
		// idle + explore (explore always has a way to succeed)
		t.Fatalf("bare world offered %d goals (%v), want just idle and explore", len(goals), GoalsFor(goals))
	}
	for _, g := range goals {
		if g.Name == jev.GoalStockUp || g.Name == jev.GoalGatherWood {
			t.Errorf("offered %q with no blocks in sight and %d free slots", g.Name, bare.FreeSlots)
		}
		if g.Name == jev.GoalSocialise {
			t.Errorf("offered %q with nobody nearby", g.Name)
		}
		if g.Name == jev.GoalBuildShelter {
			t.Errorf("offered %q in broad daylight", g.Name)
		}
	}
}

// TestIdleIsAlwaysAvailable is the guard against a bot that is never allowed to
// stop. A goal set with no way to rest guarantees permanent busyness, which is
// as robotic as never moving.
func TestIdleIsAlwaysAvailable(t *testing.T) {
	t.Parallel()

	worlds := []Snapshot{
		{NearBlocks: "oak_log", FreeSlots: 36},
		{NearBlocks: "none", FreeSlots: 0},
		{NearBlocks: "chest", FreeSlots: 10, IsNight: true},
		{NearBlocks: "none", FreeSlots: 36, VisibleSigns: []string{"BAHAN"}},
	}
	for i, s := range worlds {
		found := false
		for _, g := range AvailableGoals(s) {
			if g.Name == jev.GoalIdle {
				found = true
			}
		}
		if !found {
			t.Errorf("world %d did not offer the idle goal: %v", i, GoalsFor(AvailableGoals(s)))
		}
	}
}

// TestGatherGoalNeedsBlocksAndRoom pins the two conditions separately, because
// satisfying only one of them is the easy mistake.
func TestGatherGoalNeedsBlocksAndRoom(t *testing.T) {
	t.Parallel()

	has := func(goals []Goal, name string) bool {
		for _, g := range goals {
			if g.Name == name {
				return true
			}
		}
		return false
	}

	withBlocks := Snapshot{NearBlocks: "oak_log", FreeSlots: 5}
	if !has(AvailableGoals(withBlocks), jev.GoalGatherWood) {
		t.Error("did not offer gather_wood with logs visible and 5 free slots")
	}

	full := Snapshot{NearBlocks: "oak_log", FreeSlots: 0}
	if has(AvailableGoals(full), jev.GoalGatherWood) {
		t.Error("offered gather_wood with a full inventory; the wood would have nowhere to go")
	}
}

// TestNarrowToGoalRemovesOffGoalActivities is the entire point of having a
// goal. A menu the goal does not constrain would let the bot keep flipping a
// coin while merely claiming it is making progress.
func TestNarrowToGoalRemovesOffGoalActivities(t *testing.T) {
	t.Parallel()

	curriculum := Curriculum(Snapshot{NearBlocks: "oak_log", FreeSlots: 20, HP: 20, Hunger: 20})
	goal := newGoal(goalCatalogue[jev.GoalGatherWood], time.Now(), time.Minute)

	narrowed := NarrowToGoal(curriculum, goal)

	has := func(list []string, name string) bool {
		for _, v := range list {
			if v == name {
				return true
			}
		}
		return false
	}
	if !has(narrowed, jev.ActivityMine) {
		t.Error("narrowing to gather_wood removed mining, which is the main way to advance it")
	}
	if has(narrowed, jev.ActivityChat) {
		t.Error("narrowing to gather_wood kept an activity that has nothing to do with wood")
	}
	// Rest is always reachable, whatever the goal. A bot locked into a goal with
	// no way to stop is not persistent, it is compulsive.
	if !has(narrowed, jev.ActivityRest) {
		t.Error("narrowing removed rest; a goal that cannot be paused is not a goal")
	}
}

// TestNarrowToGoalWithoutAGoalIsANoOp keeps the brain working exactly as before
// when no goal is active, which is the state at the start of a session.
func TestNarrowToGoalWithoutAGoalIsANoOp(t *testing.T) {
	t.Parallel()

	curriculum := []string{jev.ActivityRest, jev.ActivityMine, jev.ActivityExplore}
	if got := NarrowToGoal(curriculum, Goal{}); len(got) != len(curriculum) {
		t.Errorf("NarrowToGoal with no goal returned %d activities, want the %d it was given",
			len(got), len(curriculum))
	}
}

// TestGoalAdvancesDecidesWhatCountsAsProgress keeps "did this tick help?" honest.
// An activity that does not advance the goal must not be scored as progress,
// or the goal would look successful while the bot walked in circles.
func TestGoalAdvancesDecidesWhatCountsAsProgress(t *testing.T) {
	t.Parallel()

	goal := goalCatalogue[jev.GoalGatherWood]
	if !goal.advances(jev.ActivityMine) {
		t.Error("mining does not count towards gather_wood")
	}
	if goal.advances(jev.ActivityChat) {
		t.Error("chat counts towards gather_wood, which it plainly does not")
	}
}

// TestExpiredGoalReportsItself keeps a stale intention from lingering.
func TestExpiredGoalReportsItself(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fresh := newGoal(goalCatalogue[jev.GoalExplore], now, time.Minute)
	if fresh.Expired(now.Add(30 * time.Second)) {
		t.Error("a goal expired before its lifetime elapsed")
	}
	if !fresh.Expired(now.Add(2 * time.Minute)) {
		t.Error("a goal did not expire well past its lifetime")
	}
}

// TestDescribeGoalReadsAsAnIntention checks the state text a model actually
// reads. A goal rendered as an empty string or a raw enum would give the model
// nothing to reason about.
func TestDescribeGoalReadsAsAnIntention(t *testing.T) {
	t.Parallel()

	if got := describeGoal(Goal{}); got != "no goal" {
		t.Errorf("describeGoal(no goal) = %q, want %q", got, "no goal")
	}

	goal := newGoal(goalCatalogue[jev.GoalStockUp], time.Now(), 4*time.Minute)
	goal.Progress = 2
	got := describeGoal(goal)
	if got == "no goal" {
		t.Fatal("describeGoal dropped an active goal")
	}
	if !containsAll(got, "stock_up", "2") {
		t.Errorf("describeGoal = %q, want the goal name and its progress", got)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		found := false
		for i := 0; i+len(p) <= len(s); i++ {
			if s[i:i+len(p)] == p {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
