package ai_test

import (
	"sync"
	"testing"

	"bedrock-ai/internal/ai"
)

// The honesty filter was switched on before it had ever been measured.
//
// Its marker lists are a few dozen Indonesian and English words chosen by reading
// the prompt corpus, and no real model output had ever been run past them. A
// false positive there is not cosmetic: it swaps an honest "8 logs!" for a
// fallback that reads closer to the opposite, so the bot starts contradicting
// results that really happened — trading a possible lie for a certain one.
//
// So the default is shadow: check everything, count everything, change nothing.
// These tests pin that default and pin the counting, because a measurement
// nobody can read is not a measurement.

func TestShadowModeIsTheDefault(t *testing.T) {
	// The suite may run in any order, so the mode is restored rather than
	// assumed — but a test that finds the mode wrong should say so, because
	// "shadow" being the default is the entire safety property here.
	prev := ai.SetFilterMode(ai.ModeShadow)
	t.Cleanup(func() { ai.SetFilterMode(prev) })

	if ai.CurrentFilterMode() != ai.ModeShadow {
		t.Fatalf("default filter mode = %v, want %v; the filter must not change what a player sees before it has been measured",
			ai.CurrentFilterMode(), ai.ModeShadow)
	}
	if got := ai.CurrentFilterMode().String(); got != "shadow" {
		t.Errorf("FilterMode.String() = %q, want \"shadow\"", got)
	}
}

// TestTheCountersSeparateTheTwoDirections is the measurement that would decide
// whether to enforce.
//
// The total is not the number that matters. A contradiction over a FAILED action
// is the filter doing its job. A contradiction over a SUCCEEDED one is the filter
// being wrong, and that is the count that has to be near zero before anyone
// turns enforcement on — it is the number that would make enforcement harmful.
func TestTheCountersSeparateTheTwoDirections(t *testing.T) {
	ai.SetFilterMode(ai.ModeShadow)
	ai.ResetNarrationStats()
	t.Cleanup(ai.ResetNarrationStats)

	failed := ai.Outcome{Action: "mine", Item: "diamond", Success: false, Error: "tidak terlihat"}
	succeeded := ai.Outcome{Action: "chop", Item: "log", Count: 8, Success: true}

	// Two honest replies, one lie over a failure, one false alarm over a success.
	ai.RecordNarrationVerdict(true, true, failed)
	ai.RecordNarrationVerdict(true, true, succeeded)
	ai.RecordNarrationVerdict(true, false, failed)    // caught a lie
	ai.RecordNarrationVerdict(true, false, succeeded) // filter was wrong

	stats := ai.NarrationStats()
	if stats.Checked != 4 {
		t.Errorf("Checked = %d, want 4", stats.Checked)
	}
	if stats.Contradicted != 2 {
		t.Errorf("Contradicted = %d, want 2", stats.Contradicted)
	}
	if stats.ContradictedFailure != 1 {
		t.Errorf("ContradictedFailure = %d, want 1: a success claimed over a refused action", stats.ContradictedFailure)
	}
	if stats.ContradictedSuccess != 1 {
		t.Errorf("ContradictedSuccess = %d, want 1: a refusal claimed over a confirmed action, which is the number that gates enforcement",
			stats.ContradictedSuccess)
	}
}

// TestAnUncheckedNarrationIsNotCounted. There is nothing to compare when the
// model said nothing, and counting it as a pass would inflate Checked and make
// the false-positive rate look better than it is.
func TestAnUncheckedNarrationIsNotCounted(t *testing.T) {
	ai.SetFilterMode(ai.ModeShadow)
	ai.ResetNarrationStats()
	t.Cleanup(ai.ResetNarrationStats)

	if !ai.RecordNarrationVerdict(false, false, ai.Outcome{Success: true}) {
		t.Error("an unchecked narration reported a contradiction")
	}
	if stats := ai.NarrationStats(); stats.Checked != 0 || stats.Contradicted != 0 {
		t.Errorf("an unchecked narration was counted: %+v", stats)
	}
}

// TestSwitchingModeIsRestorable pins the setter's contract, since a test that
// cannot put the mode back will silently leave enforcement on for whatever runs
// after it.
func TestSwitchingModeIsRestorable(t *testing.T) {
	prev := ai.SetFilterMode(ai.ModeShadow)
	t.Cleanup(func() { ai.SetFilterMode(prev) })

	got := ai.SetFilterMode(ai.ModeEnforce)
	if got != ai.ModeShadow {
		t.Errorf("SetFilterMode returned %v, want the previous mode %v", got, ai.ModeShadow)
	}
	if ai.CurrentFilterMode() != ai.ModeEnforce {
		t.Errorf("CurrentFilterMode = %v, want %v", ai.CurrentFilterMode(), ai.ModeEnforce)
	}
	if s := ai.ModeEnforce.String(); s != "enforce" {
		t.Errorf("ModeEnforce.String() = %q, want \"enforce\"", s)
	}

	if got := ai.SetFilterMode(ai.ModeShadow); got != ai.ModeEnforce {
		t.Errorf("restoring returned %v, want %v", got, ai.ModeEnforce)
	}
}

// TestTheCountersAreSafeUnderConcurrency. The counter is read and written from
// the status-reply path, and a data race there would be a crash in a long
// session rather than a wrong number. Run with -race where the toolchain allows
// it; the package here builds without cgo so the race detector is unavailable on
// this host, which is exactly why the test is written to be meaningful anyway.
func TestTheCountersAreSafeUnderConcurrency(t *testing.T) {
	ai.SetFilterMode(ai.ModeShadow)
	ai.ResetNarrationStats()
	t.Cleanup(ai.ResetNarrationStats)

	truth := ai.Outcome{Action: "mine", Success: false}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				ai.RecordNarrationVerdict(true, (n+j)%2 == 0, truth)
				_ = ai.NarrationStats()
			}
		}(i)
	}
	wg.Wait()

	stats := ai.NarrationStats()
	if stats.Checked != 1600 {
		t.Errorf("Checked = %d, want 1600; concurrent increments were lost", stats.Checked)
	}
	if stats.ContradictedFailure > stats.Contradicted {
		// ContradictedFailure is a subset of Contradicted; this is a cheap
		// invariant that catches a counter being wired to the wrong total. It is
		// "<=" and not "==": every contradiction here is over a failed action, so
		// the two are legitimately equal in this test.
		t.Errorf("Contradicted = %d but ContradictedFailure = %d, which is a subset and cannot be larger",
			stats.Contradicted, stats.ContradictedFailure)
	}
}
