package agi_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/jev"
)

// TestAvailableGoalsNeverOfferTheImpossible is the rule the whole feature rests
// on: the model must never be asked to pursue something that cannot succeed.
// "Get wood" with a full inventory and no wood in sight is a goal that spends a
// decision on nothing.
func TestAvailableGoalsNeverOfferTheImpossible(t *testing.T) {
	t.Parallel()

	// Nothing to gather, nobody around, not night: the only honest goal is idle.
	bare := agi.Snapshot{NearBlocks: "none", FreeSlots: 36, HP: 20, Hunger: 20}
	goals := agi.AvailableGoals(bare)
	if len(goals) != 2 {
		// idle + explore (explore always has a way to succeed)
		t.Fatalf("bare world offered %d goals (%v), want just idle and explore", len(goals), agi.GoalsFor(goals))
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

	worlds := []agi.Snapshot{
		{NearBlocks: "oak_log", FreeSlots: 36},
		{NearBlocks: "none", FreeSlots: 0},
		{NearBlocks: "chest", FreeSlots: 10, IsNight: true},
		{NearBlocks: "none", FreeSlots: 36, VisibleSigns: []string{"BAHAN"}},
	}
	for i, s := range worlds {
		found := false
		for _, g := range agi.AvailableGoals(s) {
			if g.Name == jev.GoalIdle {
				found = true
			}
		}
		if !found {
			t.Errorf("world %d did not offer the idle goal: %v", i, agi.GoalsFor(agi.AvailableGoals(s)))
		}
	}
}

// TestGatherGoalNeedsBlocksAndRoom pins the two conditions separately, because
// satisfying only one of them is the easy mistake.
func TestGatherGoalNeedsBlocksAndRoom(t *testing.T) {
	t.Parallel()

	has := func(goals []agi.Goal, name string) bool {
		for _, g := range goals {
			if g.Name == name {
				return true
			}
		}
		return false
	}

	withBlocks := agi.Snapshot{NearBlocks: "oak_log", FreeSlots: 5}
	if !has(agi.AvailableGoals(withBlocks), jev.GoalGatherWood) {
		t.Error("did not offer gather_wood with logs visible and 5 free slots")
	}

	full := agi.Snapshot{NearBlocks: "oak_log", FreeSlots: 0}
	if has(agi.AvailableGoals(full), jev.GoalGatherWood) {
		t.Error("offered gather_wood with a full inventory; the wood would have nowhere to go")
	}
}

// TestNarrowToGoalRemovesOffGoalActivities is the entire point of having a
// goal. A menu the goal does not constrain would let the bot keep flipping a
// coin while merely claiming it is making progress.
func TestNarrowToGoalRemovesOffGoalActivities(t *testing.T) {
	t.Parallel()

	curriculum := agi.Curriculum(agi.Snapshot{NearBlocks: "oak_log", FreeSlots: 20, HP: 20, Hunger: 20})
	goal := agi.NewGoal(agi.GoalCatalogue[jev.GoalGatherWood], time.Now(), time.Minute)

	narrowed := agi.NarrowToGoal(curriculum, goal)

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
	if got := agi.NarrowToGoal(curriculum, agi.Goal{}); len(got) != len(curriculum) {
		t.Errorf("NarrowToGoal with no goal returned %d activities, want the %d it was given",
			len(got), len(curriculum))
	}
}

// TestGoalAdvancesDecidesWhatCountsAsProgress keeps "did this tick help?" honest.
// An activity that does not advance the goal must not be scored as progress,
// or the goal would look successful while the bot walked in circles.
func TestGoalAdvancesDecidesWhatCountsAsProgress(t *testing.T) {
	t.Parallel()

	goal := agi.GoalCatalogue[jev.GoalGatherWood]
	if !goal.AdvancesActivity(jev.ActivityMine) {
		t.Error("mining does not count towards gather_wood")
	}
	if goal.AdvancesActivity(jev.ActivityChat) {
		t.Error("chat counts towards gather_wood, which it plainly does not")
	}
}

// TestExpiredGoalReportsItself keeps a stale intention from lingering.
func TestExpiredGoalReportsItself(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fresh := agi.NewGoal(agi.GoalCatalogue[jev.GoalExplore], now, time.Minute)
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

	if got := agi.DescribeGoal(agi.Goal{}); got != "no goal" {
		t.Errorf("describeGoal(no goal) = %q, want %q", got, "no goal")
	}

	goal := agi.NewGoal(agi.GoalCatalogue[jev.GoalStockUp], time.Now(), 4*time.Minute)
	goal.Progress = 2
	got := agi.DescribeGoal(goal)
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

// Narrowing may only ever remove what the world permits.
//
// NarrowToGoal used to take the curriculum and throw it away, replacing it with
// whatever the goal's Advances happened to list. That reinstated every activity
// the curriculum had just ruled out: a bot with a full inventory and a "stock
// up" goal was handed "gather" and "mine" every tick, had both refused, and
// then had them offered again on the next tick.
//
// This is the same defect as the one that produced thirty-nine consecutive
// skipped gathers, in a second place. Fixing the curriculum did not fix it,
// because the narrowing was writing over the top of the fix.

func TestNarrowingDoesNotReAddWhatTheWorldRuledOut(t *testing.T) {
	t.Parallel()

	// Nothing in the world to collect, which is the reason the curriculum drops
	// both gather and mine. A goal cannot make wood appear.
	s := agi.Snapshot{NearBlocks: "none", FreeSlots: 20, HP: 20, Hunger: 20}
	curriculum := agi.Curriculum(s)

	for _, excluded := range []string{jev.ActivityGather, jev.ActivityMine} {
		if hasActivityIn(curriculum, excluded) {
			t.Fatalf("the fixture is wrong: %q is still on the curriculum, so this "+
				"test cannot tell whether narrowing reinstated it", excluded)
		}
	}

	goal := agi.NewGoal(agi.GoalCatalogue[jev.GoalStockUp], time.Now(), time.Minute)
	narrowed := agi.NarrowToGoal(curriculum, goal)

	for _, excluded := range []string{jev.ActivityGather, jev.ActivityMine} {
		if hasActivityIn(narrowed, excluded) {
			t.Errorf("narrowing reinstated %q with nothing in the world to collect: "+
				"the bot picks it, the gatherer declines, and the model picks it "+
				"again next tick", excluded)
		}
	}
	if !hasActivityIn(narrowed, jev.ActivityRest) {
		t.Error("narrowing removed rest: a goal that cannot be paused is not a goal")
	}
}

// TestNarrowingStillKeepsWhatTheGoalIsFor is the other direction, so the fix
// cannot pass by emptying the menu.
func TestNarrowingStillKeepsWhatTheGoalIsFor(t *testing.T) {
	t.Parallel()

	s := agi.Snapshot{NearBlocks: "oak_log", FreeSlots: 20, HP: 20, Hunger: 20}
	curriculum := agi.Curriculum(s)
	goal := agi.NewGoal(agi.GoalCatalogue[jev.GoalStockUp], time.Now(), time.Minute)

	narrowed := agi.NarrowToGoal(curriculum, goal)

	if !hasActivityIn(narrowed, jev.ActivityGather) {
		t.Errorf("a bot holding logs with room to spare was not offered gather: %v", narrowed)
	}
	if hasActivityIn(narrowed, jev.ActivityChat) {
		t.Errorf("narrowing kept an activity that does nothing for stocking up: %v", narrowed)
	}
}

// hasActivityIn reports whether a menu holds a named activity.
func hasActivityIn(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}
