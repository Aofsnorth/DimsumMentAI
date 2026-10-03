// Two bugs in the planner, both about a plan being replaced while work from the
// previous one is still in flight.
//
// A step is dispatched with its index and runs on its own goroutine — a gather
// can take ninety seconds. A replan can land inside that window, because a
// replan is exactly what the ten-minute cooldown eventually asks for. Nothing
// connected the two: the step carried an index, the index was resolved against
// whatever plan happened to be in play when the action finished, and the two
// were silently assumed to be the same. When they were not, a step marked an
// unrelated step of the new plan done and — if nothing was left pending —
// reported the whole plan complete for steps that had never run.
//
// The second is the same mistake one line apart. installPlan carries completed
// steps over from the plan it replaces, and then unconditionally marks the new
// plan's first step active, which undid the carry-over it had just performed.

package agi_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
)

// TestACompletedFirstStepIsCarriedOverNotReRun is the regression for the
// carry-over.
//
// The old plan's first step was done. The new plan's first step is the same
// step. Carrying it over and then starting it again means the bot re-chops the
// logs it already chopped on every replan — which is what a bot "stuck on the
// same first step" actually is.
func TestACompletedFirstStepIsCarriedOverNotReRun(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})
	now := time.Now()

	r.SetPlan(agi.Plan{
		ID:        "plan-1",
		Objective: "get wood",
		Steps: []agi.PlanStep{
			{Kind: agi.StepGather, Description: "chop oak logs", State: agi.StepDone},
			{Kind: agi.StepCraft, Description: "make planks", State: agi.StepPending},
		},
	})

	r.InstallPlanForTest(agi.Plan{
		Objective: "get wood",
		Steps: []agi.PlanStep{
			{Kind: agi.StepGather, Description: "chop oak logs"},
			{Kind: agi.StepCraft, Description: "make planks"},
		},
	}, now)

	plan := r.CurrentPlan()
	if plan.Steps[0].State != agi.StepDone {
		t.Errorf("first step state = %v, want StepDone: the step was already done in "+
			"the plan being replaced and must not be re-run by the new one", plan.Steps[0].State)
	}
	done, total := plan.ProgressForTest()
	if done != 1 || total != 2 {
		t.Errorf("progress = %d/%d, want 1/2: the carried-over step is not being counted", done, total)
	}
}

// TestAFreshFirstStepStillStarts guards the other direction. The carry-over must
// not stop a genuinely new plan from starting.
func TestAFreshFirstStepStillStarts(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})
	now := time.Now()

	r.SetPlan(agi.Plan{
		ID:        "plan-1",
		Objective: "old",
		Steps:     []agi.PlanStep{{Kind: agi.StepGather, Description: "chop oak logs", State: agi.StepDone}},
	})

	r.InstallPlanForTest(agi.Plan{
		Objective: "new",
		Steps: []agi.PlanStep{
			{Kind: agi.StepMine, Description: "mine stone"},
			{Kind: agi.StepCraft, Description: "make a pickaxe"},
		},
	}, now)

	plan := r.CurrentPlan()
	if plan.Steps[0].State != agi.StepActive {
		t.Errorf("first step state = %v, want StepActive: a step the previous plan "+
			"never touched has to start", plan.Steps[0].State)
	}
}

// TestAReplanGivesThePlanANewIdentity is the precondition for the guard below.
// installPlan stamps a fresh ID, which is what lets a late step outcome tell
// which plan it belonged to.
//
// The seeded ID is deliberately not of the generated form. installPlan numbers
// plans from a counter that starts at zero, so its first ID is always "plan-1";
// a fixture that seeded "plan-1" too would collide with it and pass a guard that
// compares IDs for the wrong reason.
func TestAReplanGivesThePlanANewIdentity(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})
	now := time.Now()

	r.SetPlan(agi.Plan{ID: "seeded-by-hand", Objective: "first", Steps: []agi.PlanStep{
		{Kind: agi.StepGather, Description: "chop", State: agi.StepActive},
	}})
	first := r.CurrentPlan().ID

	r.InstallPlanForTest(agi.Plan{Objective: "second", Steps: []agi.PlanStep{
		{Kind: agi.StepMine, Description: "mine"},
	}}, now)
	second := r.CurrentPlan().ID

	if first == second {
		t.Errorf("plan ID is %q both before and after a replan; a step that finishes "+
			"after a replan cannot tell the plans apart", second)
	}
}

// TestAStepFinishingAfterAReplanDoesNotTouchTheNewPlan is the critical
// regression.
//
// The old plan's step 0 finishes after a replan has installed a different plan.
// Completing it must not mark step 0 of the new plan done — and above all must
// not report the new plan complete, because that is a claim about work nobody
// did.
func TestAStepFinishingAfterAReplanDoesNotTouchTheNewPlan(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})

	r.SetPlan(agi.Plan{
		ID:        "plan-1",
		Objective: "get wood",
		Steps: []agi.PlanStep{
			{Kind: agi.StepGather, Description: "chop oak logs", State: agi.StepActive},
		},
	})
	stalePlanID := r.CurrentPlan().ID

	// The replan lands while the gather is still running.
	r.SetPlan(agi.Plan{
		ID:        "plan-2",
		Objective: "mine iron",
		Steps: []agi.PlanStep{
			{Kind: agi.StepMine, Description: "mine iron ore", State: agi.StepPending},
		},
	})

	r.FinishStepForTest(0, agi.PlanStep{
		Kind:        agi.StepGather,
		Description: "chop oak logs",
	}, stalePlanID, "")

	plan := r.CurrentPlan()
	if plan.ID != "plan-2" {
		t.Fatalf("plan ID changed to %q; the outcome reached across the replan", plan.ID)
	}
	if plan.Steps[0].State == agi.StepDone {
		t.Error("a step that finished against a replaced plan marked an unrelated " +
			"step of the new plan done")
	}
}

// TestASupersededFailureDoesNotReviveAnotherPlansStep is the same guard on the
// failure path, which is the worse of the two: failing a step also moves the
// plan's active step, so a stale failure could restart work in a plan that had
// already moved on.
func TestASupersededFailureDoesNotReviveAnotherPlansStep(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})

	r.SetPlan(agi.Plan{
		ID:        "plan-1",
		Objective: "get wood",
		Steps: []agi.PlanStep{
			{Kind: agi.StepGather, Description: "chop oak logs", State: agi.StepActive},
		},
	})
	stalePlanID := r.CurrentPlan().ID

	r.SetPlan(agi.Plan{
		ID:        "plan-2",
		Objective: "mine iron",
		Steps: []agi.PlanStep{
			{Kind: agi.StepMine, Description: "mine iron ore", State: agi.StepDone},
			{Kind: agi.StepCraft, Description: "smelt", State: agi.StepActive},
		},
	})

	r.FailStepWorkForTest(0, agi.PlanStep{
		Kind:        agi.StepGather,
		Description: "chop oak logs",
	}, stalePlanID, "a creeper was in the way")

	plan := r.CurrentPlan()
	if plan.Steps[0].State != agi.StepDone {
		t.Errorf("step 0 state = %v, want StepDone: a superseded failure reopened a "+
			"step of the plan now in play", plan.Steps[0].State)
	}
	if plan.Steps[1].State != agi.StepActive {
		t.Errorf("step 1 state = %v, want StepActive: a superseded failure moved the "+
			"new plan's active step", plan.Steps[1].State)
	}
}

// TestAnOutcomeForTheCurrentPlanStillLands is the other direction, so the guard
// cannot pass by discarding everything.
func TestAnOutcomeForTheCurrentPlanStillLands(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})

	r.SetPlan(agi.Plan{
		ID:        "plan-1",
		Objective: "get wood",
		Steps: []agi.PlanStep{
			{Kind: agi.StepGather, Description: "chop oak logs", State: agi.StepActive},
			{Kind: agi.StepCraft, Description: "make planks", State: agi.StepPending},
		},
	})
	planID := r.CurrentPlan().ID

	r.FinishStepForTest(0, agi.PlanStep{
		Kind:        agi.StepGather,
		Description: "chop oak logs",
	}, planID, "")

	plan := r.CurrentPlan()
	if plan.Steps[0].State != agi.StepDone {
		t.Errorf("step 0 state = %v, want StepDone: an outcome for the plan in play "+
			"must still be recorded", plan.Steps[0].State)
	}
	if plan.Steps[1].State != agi.StepActive {
		t.Errorf("step 1 state = %v, want StepActive: completing a step has to start "+
			"the next one", plan.Steps[1].State)
	}
}
