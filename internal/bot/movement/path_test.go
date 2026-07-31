package movement

import "testing"

func TestEvaluateNavProgressReachesTolerance(t *testing.T) {
	t.Parallel()
	d := evaluateNavProgress(1.0, 5.0, 1.5, 0, "walk_to", true)
	if !d.done || !d.reached {
		t.Fatalf("within tolerance: got %+v, want done+reached", d)
	}
}

func TestEvaluateNavProgressKeepsWaitingWhileProgressing(t *testing.T) {
	t.Parallel()
	// Distance shrank well beyond the epsilon on an active path: keep going,
	// stall counter resets to zero.
	d := evaluateNavProgress(6.0, 9.0, 2.0, 4, "walk_to", true)
	if d.done {
		t.Fatalf("still progressing: got done=true, want keep waiting (%+v)", d)
	}
	if d.stalledPolls != 0 {
		t.Fatalf("stall counter = %d, want reset to 0 on progress", d.stalledPolls)
	}
}

func TestEvaluateNavProgressAccumulatesStallThenStops(t *testing.T) {
	t.Parallel()
	// No measurable progress (same distance) while path is still active: stall
	// counter increments and does not finish until it hits the limit.
	d := evaluateNavProgress(9.0, 9.0, 2.0, navStallPollsLimit-2, "walk_to", true)
	if d.done {
		t.Fatalf("below stall limit: got done=true, want keep waiting (%+v)", d)
	}
	if d.stalledPolls != navStallPollsLimit-1 {
		t.Fatalf("stall counter = %d, want %d", d.stalledPolls, navStallPollsLimit-1)
	}

	stuck := evaluateNavProgress(9.0, 9.0, 2.0, navStallPollsLimit-1, "walk_to", true)
	if !stuck.done || stuck.reached {
		t.Fatalf("at stall limit: got %+v, want done and not reached", stuck)
	}
}

func TestEvaluateNavProgressStopsWhenPathEndsOutsideTolerance(t *testing.T) {
	t.Parallel()
	// Path finished (walk_to but no path) while still outside tolerance.
	d := evaluateNavProgress(3.0, 3.2, 1.5, 0, "walk_to", false)
	if !d.done || d.reached {
		t.Fatalf("path ended outside tolerance: got %+v, want done and not reached", d)
	}
}

func TestEvaluateNavProgressStopsWhenIdle(t *testing.T) {
	t.Parallel()
	d := evaluateNavProgress(3.0, 3.2, 1.5, 0, "idle", true)
	if !d.done || d.reached {
		t.Fatalf("idle outside tolerance: got %+v, want done and not reached", d)
	}
}
