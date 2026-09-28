package agi

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
)

// newGoalRunner builds a runner around a configured operator goal. The bot is
// only there to carry a logger, and the goal is pure runner state, so this is
// enough to pin the whole behaviour without a live connection.
func newGoalRunner(t *testing.T, goal string, deadlineMin int) *Runner {
	t.Helper()
	b := &bot.Bot{Logger: slog.Default()}
	return New(b, Config{Enabled: true, Goal: goal, GoalDeadlineMin: deadlineMin})
}

// TestAnOperatorGoalIsInstalledBeforeTheFirstTick is the feature. A goal written
// down in the config has to be the goal the bot is already holding when it
// joins, not one it adopts later if a model happens to agree — otherwise a
// working setting and a typo are indistinguishable until the recording ends.
func TestAnOperatorGoalIsInstalledBeforeTheFirstTick(t *testing.T) {
	t.Parallel()

	r := newGoalRunner(t, "kill the ender dragon", 0)

	goal := r.currentGoal()
	if goal.Name == "" {
		t.Fatal("a configured goal was not installed")
	}
	if !goal.Pinned {
		t.Fatal("a configured goal is not pinned, so the model can replace it")
	}
	if goal.Description != "kill the ender dragon" {
		t.Fatalf("goal text = %q, want the operator's words", goal.Description)
	}
	if !goal.Deadline.IsZero() {
		t.Fatalf("deadline = %v, want none when the operator set none", goal.Deadline)
	}
}

// TestTheModelCannotTalkTheBotOutOfAnOperatorGoal is what "pinned" is for. Left
// to itself the model proposes a goal every tick, and without this the bot
// abandons what it was told to do in favour of whichever idea arrived last.
func TestTheModelCannotTalkTheBotOutOfAnOperatorGoal(t *testing.T) {
	t.Parallel()

	r := newGoalRunner(t, "kill the ender dragon", 0)
	now := time.Now()

	for _, choice := range []string{"gather wood", "explore", "rest", "build a shelter"} {
		if r.considerGoal(choice, 0.99, now) {
			t.Fatalf("the model replaced the operator's goal with %q", choice)
		}
	}
	if got := r.currentGoal().Description; got != "kill the ender dragon" {
		t.Fatalf("goal is now %q, want the operator's goal intact", got)
	}
}

// TestAnOperatorGoalDoesNotLockTheBotIntoStandingStill is the trap this feature
// walks straight into. A goal narrows the activity menu to what advances it, and
// free text like "kill the ender dragon" has no activities attached — so a
// pinned goal has to be exempt, or the only thing left in the menu is "rest".
func TestAnOperatorGoalDoesNotLockTheBotIntoStandingStill(t *testing.T) {
	t.Parallel()

	curriculum := []string{"gather", "mine", "explore", "wander", "rest", "craft"}

	r := newGoalRunner(t, "kill the ender dragon", 0)
	snap := Snapshot{}
	full := Curriculum(snap)
	narrowed := r.menu(snap)

	if len(narrowed) != len(full) {
		t.Fatalf("menu shrank to %v under an operator goal, want the full curriculum %v", narrowed, full)
	}
	if len(NarrowToGoal(curriculum, Goal{Name: operatorGoalName, Pinned: true})) != len(curriculum) {
		t.Fatal("a pinned goal narrowed the curriculum; only 'rest' would be left")
	}
}

// TestAModelChosenGoalStillNarrows is the other half: pinning is for operator
// goals only. A goal the model chose must keep constraining the bot, or
// narrowing stops meaning anything.
func TestAModelChosenGoalStillNarrows(t *testing.T) {
	t.Parallel()

	curriculum := []string{"gather", "mine", "explore", "wander", "rest"}
	narrowed := NarrowToGoal(curriculum, Goal{Name: "gather_wood", Advances: []string{"mine", "gather"}})

	if len(narrowed) != 3 {
		t.Fatalf("narrowed = %v, want the two advancing activities plus rest", narrowed)
	}
	for _, activity := range narrowed {
		if activity == "explore" || activity == "wander" {
			t.Fatalf("%q survived narrowing, want the goal to constrain the menu", activity)
		}
	}
}

// TestThePlannerIsToldTheGoalIsAnInstruction covers the half that actually makes
// the bot work toward it. The planner is otherwise handed a world description
// and asked for a plan, and it will cheerfully plan to chop wood.
func TestThePlannerIsToldTheGoalIsAnInstruction(t *testing.T) {
	t.Parallel()

	r := newGoalRunner(t, "kill the ender dragon", 0)
	message := r.plannerMessageFor(Snapshot{HP: 20, Now: time.Now(), Coords: "X:0 Y:64 Z:0"})

	if !strings.Contains(message, "kill the ender dragon") {
		t.Fatalf("the planner was not shown the goal:\n%s", message)
	}
	if !strings.Contains(message, "must move it toward that goal") {
		t.Fatalf("the goal was shown as context, not as an instruction:\n%s", message)
	}
	if !strings.Contains(message, "as JSON") {
		t.Fatalf("the instruction replaced the output contract:\n%s", message)
	}
}

// TestNoConfiguredGoalMeansNoInstruction is the default path. An extra line in
// the planner prompt for a bot nobody gave a goal is noise that costs tokens
// and makes every plan read as if it were constrained.
func TestNoConfiguredGoalMeansNoInstruction(t *testing.T) {
	t.Parallel()

	r := newGoalRunner(t, "", 0)
	if got := r.operatorGoalInstruction(); got != "" {
		t.Fatalf("an unconfigured bot still sent the planner a goal line: %q", got)
	}
	if goal := r.currentGoal(); goal.Name != "" {
		t.Fatalf("a bot with no configured goal invented one: %+v", goal)
	}
}

// TestAnOperatorGoalCanExpireWhenTheOperatorSetsADeadline. "Find a village" is
// a reasonable twenty-minute errand; a goal that outlives its welcome is how a
// bot ends up doing the same thing for six hours.
func TestAnOperatorGoalCanExpireWhenTheOperatorSetsADeadline(t *testing.T) {
	t.Parallel()

	r := newGoalRunner(t, "find a village", 20)
	goal := r.currentGoal()
	now := goal.Started

	if goal.Deadline.IsZero() {
		t.Fatal("a goal with a configured deadline has none")
	}
	if goal.Expired(now) {
		t.Fatal("a goal that was just set is already expired")
	}
	if !goal.Expired(now.Add(21 * time.Minute)) {
		t.Fatal("a goal outlived its deadline")
	}
}
