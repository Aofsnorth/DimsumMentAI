package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"
)

func TestEvaluateNavProgressReachesTolerance(t *testing.T) {
	t.Parallel()
	d := movement.EvaluateNavProgress(1.0, 5.0, 1.5, 0, "walk_to", true)
	if !d.Done || !d.Reached {
		t.Fatalf("within tolerance: got %+v, want done+reached", d)
	}
}

func TestEvaluateNavProgressKeepsWaitingWhileProgressing(t *testing.T) {
	t.Parallel()
	// Distance shrank well beyond the epsilon on an active path: keep going,
	// stall counter resets to zero.
	d := movement.EvaluateNavProgress(6.0, 9.0, 2.0, 4, "walk_to", true)
	if d.Done {
		t.Fatalf("still progressing: got Done=true, want keep waiting (%+v)", d)
	}
	if d.StalledPolls != 0 {
		t.Fatalf("stall counter = %d, want reset to 0 on progress", d.StalledPolls)
	}
}

func TestEvaluateNavProgressAccumulatesStallThenStops(t *testing.T) {
	t.Parallel()
	// No measurable progress (same distance) while path is still active: stall
	// counter increments and does not finish until it hits the limit.
	d := movement.EvaluateNavProgress(9.0, 9.0, 2.0, movement.NavStallPollsLimit-2, "walk_to", true)
	if d.Done {
		t.Fatalf("below stall limit: got Done=true, want keep waiting (%+v)", d)
	}
	if d.StalledPolls != movement.NavStallPollsLimit-1 {
		t.Fatalf("stall counter = %d, want %d", d.StalledPolls, movement.NavStallPollsLimit-1)
	}

	stuck := movement.EvaluateNavProgress(9.0, 9.0, 2.0, movement.NavStallPollsLimit-1, "walk_to", true)
	if !stuck.Done || stuck.Reached {
		t.Fatalf("at stall limit: got %+v, want done and not reached", stuck)
	}
}

func TestEvaluateNavProgressStopsWhenPathEndsOutsideTolerance(t *testing.T) {
	t.Parallel()
	// Path finished (walk_to but no path) while still outside tolerance.
	d := movement.EvaluateNavProgress(3.0, 3.2, 1.5, 0, "walk_to", false)
	if !d.Done || d.Reached {
		t.Fatalf("path ended outside tolerance: got %+v, want done and not reached", d)
	}
}

func TestEvaluateNavProgressStopsWhenIdle(t *testing.T) {
	t.Parallel()
	d := movement.EvaluateNavProgress(3.0, 3.2, 1.5, 0, "idle", true)
	if !d.Done || d.Reached {
		t.Fatalf("idle outside tolerance: got %+v, want done and not reached", d)
	}
}
