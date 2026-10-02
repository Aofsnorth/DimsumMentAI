package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"
)

// Dive planning is the half of water movement that decides how deep to be. It
// is pure on purpose: "surface before drowning" is a claim that has to be
// checkable without a server, a world and a body.

// submerged builds the submersion a body reports at a given depth under a
// surface whose top water cell is surfaceY.
func submerged(surfaceY, feetY int32) movement.Submersion {
	sub := movement.Submersion{
		FeetX: 0, FeetY: feetY, FeetZ: 0,
		InWater:    true,
		Submerged:  feetY < surfaceY,
		SurfaceY:   surfaceY,
		HasSurface: true,
	}
	if sub.Submerged {
		sub.ClimbBlocks = surfaceY - feetY
	}
	return sub
}

// TestBreathStateSecondsLeftNeverGoesNegative keeps a spent air bar from
// reading as a fresh one.
func TestBreathStateSecondsLeftNeverGoesNegative(t *testing.T) {
	t.Parallel()

	state := movement.NewBreathState(40, 15)

	if state.SecondsLeft() != 0 {
		t.Errorf("SecondsLeft = %d after 40 s under a 15 s budget, want 0", state.SecondsLeft())
	}
	if !state.MustSurface() {
		t.Error("MustSurface = false with no air left")
	}
	if !state.ReserveReached() {
		t.Error("ReserveReached = false with no air left")
	}
}

// TestBreathStateClampsANonPositiveBudget makes the safe direction the default.
// A dive planner that believes it has unlimited air is exactly the planner that
// drowns, so a caller who forgot to configure a budget gets one second and
// therefore still gets a dive that ends.
func TestBreathStateClampsANonPositiveBudget(t *testing.T) {
	t.Parallel()

	state := movement.NewBreathState(0, 0)

	if state.Budget != 1 {
		t.Errorf("Budget = %d, want it clamped to 1", state.Budget)
	}
	spent := movement.NewBreathState(1, 0)
	if !spent.MustSurface() {
		t.Error("a misconfigured budget did not run out; the dive would never be broken")
	}
	if spent.ReserveReached() != true {
		t.Error("a misconfigured budget never reached its reserve; the climb would start too late")
	}
}

// TestPlanDepthHoldsAtTheSurfaceWithoutADive is the idle case. A bot floating
// with its head out and nothing to do should not start sinking.
func TestPlanDepthHoldsAtTheSurfaceWithoutADive(t *testing.T) {
	t.Parallel()

	plan := movement.PlanDepth(submerged(63, 63), movement.DiveIntent{}, movement.NewBreathState(0, 15))

	if plan.Action != movement.DepthHold {
		t.Errorf("Action = %q at the surface with no dive, want %q", plan.Action, movement.DepthHold)
	}
	if plan.TargetY != 63 {
		t.Errorf("TargetY = %d, want 63 (hold where we are)", plan.TargetY)
	}
	if plan.Reason == "" {
		t.Error("a depth plan with no reason cannot be debugged from a log")
	}
}

// TestPlanDepthDescendsToTheDiveTarget is the retrieval: the bot is asked for a
// depth and the plan says go down.
func TestPlanDepthDescendsToTheDiveTarget(t *testing.T) {
	t.Parallel()

	dive := movement.NewDiveIntent(60)
	plan := movement.PlanDepth(submerged(63, 62), dive, movement.NewBreathState(1, 15))

	if plan.Action != movement.DepthDescend {
		t.Errorf("Action = %q, want %q", plan.Action, movement.DepthDescend)
	}
	if plan.TargetY != 60 {
		t.Errorf("TargetY = %d, want the dive target 60", plan.TargetY)
	}
	if plan.Suspended {
		t.Error("Suspended = true with a full air bar; the dive should be running")
	}
}

// TestPlanDepthRisesBackToTheDiveTarget is the ascent half of the same dive:
// once at depth, the way back up is a normal plan, not an emergency.
func TestPlanDepthRisesBackToTheDiveTarget(t *testing.T) {
	t.Parallel()

	dive := movement.NewDiveIntent(63)
	plan := movement.PlanDepth(submerged(63, 61), dive, movement.NewBreathState(2, 15))

	if plan.Action != movement.DepthAscend {
		t.Errorf("Action = %q, want %q", plan.Action, movement.DepthAscend)
	}
	if plan.TargetY != 63 {
		t.Errorf("TargetY = %d, want 63", plan.TargetY)
	}
}

// TestPlanDepthHoldsAtTheDiveTarget is the "hold station at depth" behaviour a
// retrieval needs, so the bot does not oscillate around the item.
func TestPlanDepthHoldsAtTheDiveTarget(t *testing.T) {
	t.Parallel()

	dive := movement.NewDiveIntent(60)
	plan := movement.PlanDepth(submerged(63, 60), dive, movement.NewBreathState(3, 15))

	if plan.Action != movement.DepthHold {
		t.Errorf("Action = %q at the dive target, want %q", plan.Action, movement.DepthHold)
	}
	if plan.TargetY != 60 {
		t.Errorf("TargetY = %d, want 60", plan.TargetY)
	}
}

// TestPlanDepthSurfacesBeforeTheAirRunsOut is the acceptance clause for 2.3,
// stated as a number: the dive breaks while air is still left, not at zero.
func TestPlanDepthSurfacesBeforeTheAirRunsOut(t *testing.T) {
	t.Parallel()

	breath := movement.NewBreathState(int(movement.DefaultBreathBudget.Seconds())-1, int(movement.DefaultBreathBudget.Seconds()))
	plan := movement.PlanDepth(submerged(63, 60), movement.NewDiveIntent(60), breath)

	if plan.Action != movement.DepthAscend {
		t.Errorf("Action = %q one second from drowning, want %q", plan.Action, movement.DepthAscend)
	}
	if breath.SecondsLeft() <= 0 {
		t.Fatalf("test setup: SecondsLeft = %d, want air remaining", breath.SecondsLeft())
	}
	if plan.TargetY != 63 {
		t.Errorf("TargetY = %d, want the surface 63", plan.TargetY)
	}
}

// TestPlanDepthSuspendsButKeepsTheDiveTarget is the difference between pausing a
// dive and cancelling it. The bot breathes and comes back for the item; a
// cancelled dive sends it home empty-handed.
func TestPlanDepthSuspendsButKeepsTheDiveTarget(t *testing.T) {
	t.Parallel()

	plan := movement.PlanDepth(submerged(63, 60), movement.NewDiveIntent(60), movement.NewBreathState(11, 15))

	if plan.Action != movement.DepthAscend {
		t.Errorf("Action = %q, want %q for the breath reflex", plan.Action, movement.DepthAscend)
	}
	if !plan.Suspended {
		t.Error("Suspended = false: the dive was cancelled instead of parked")
	}
}

// TestPlanDepthClimbsBlindWhenNoSurfaceWasFound is the flooded-shaft case. The
// plan has to admit it is guessing, because a bot that quietly holds at the
// bottom of an unventilated shaft drowns on schedule.
func TestPlanDepthClimbsBlindWhenNoSurfaceWasFound(t *testing.T) {
	t.Parallel()

	sub := submerged(0, 40)
	sub.HasSurface = false
	sub.SurfaceY = 40 + movement.SurfaceSearchLimit

	plan := movement.PlanDepth(sub, movement.NewDiveIntent(30), movement.NewBreathState(11, 15))

	if plan.Action != movement.DepthAscend {
		t.Errorf("Action = %q, want a climb even without a known surface", plan.Action)
	}
	if !plan.Blind {
		t.Error("Blind = false: the planner is not admitting it cannot see air")
	}
}

// TestPlanDepthSurfacesAnySubmergedBodyWithoutADive covers the case the AGI
// reflex used to have to handle alone: a body that ended up under with nobody
// asking it to be there.
func TestPlanDepthSurfacesAnySubmergedBodyWithoutADive(t *testing.T) {
	t.Parallel()

	plan := movement.PlanDepth(submerged(63, 60), movement.DiveIntent{}, movement.NewBreathState(0, 15))

	if plan.Action != movement.DepthAscend {
		t.Errorf("Action = %q for an accidentally submerged body, want %q", plan.Action, movement.DepthAscend)
	}
	if plan.Suspended {
		t.Error("Suspended = true with no dive to suspend")
	}
}

// TestDiveIntentReadsTheBodyHeight is the predicate the plan is built on, and
// the direction convention (larger Y is higher) is the thing that silently
// flips a dive into an ascent if it is written wrong.
func TestDiveIntentReadsTheBodyHeight(t *testing.T) {
	t.Parallel()

	dive := movement.NewDiveIntent(60)

	if !dive.WantsDescendAt(62) {
		t.Error("feet at 62 with a target of 60 should want to descend")
	}
	if dive.WantsDescendAt(60) {
		t.Error("feet at the target should not want to descend")
	}
	if !dive.WantsAscendAt(58) {
		t.Error("feet at 58 with a target of 60 should want to ascend")
	}

	if (movement.DiveIntent{}).Active {
		t.Error("the zero DiveIntent claims to be an active dive")
	}
}

// TestDiveCycleNeverOutrunsTheAirBar is the end-to-end arithmetic of 2.3: hold a
// three-block dive for a full air bar and the plan has to break it with air in
// hand, every single time.
func TestDiveCycleNeverOutrunsTheAirBar(t *testing.T) {
	t.Parallel()

	const surfaceY, depthY int32 = 63, 60
	dive := movement.NewDiveIntent(depthY)

	feetY := surfaceY
	secondsUnder := 0
	budget := 15

	for second := 0; second < 200; second++ {
		sub := submerged(surfaceY, feetY)
		if sub.Submerged {
			secondsUnder++
		} else {
			secondsUnder = 0
		}

		plan := movement.PlanDepth(sub, dive, movement.NewBreathState(secondsUnder, budget))

		if secondsUnder >= budget {
			t.Fatalf("second %d: the plan let the body stay under for the whole air bar (%+v)", second, plan)
		}

		switch plan.Action {
		case movement.DepthAscend:
			feetY++
		case movement.DepthDescend:
			feetY--
		case movement.DepthHold:
		}
		if feetY > surfaceY {
			feetY = surfaceY
		}
		if feetY < depthY-2 {
			t.Fatalf("second %d: the dive drove the body to %d, past its own safety floor (%+v)", second, feetY, plan)
		}
	}
}
