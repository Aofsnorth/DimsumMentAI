package agi_test

import (
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/agi"
)

// The affordance question was sent and its answer thrown away.
//
// The bot picked a verb every tick, nothing ran, and it stood still for seven
// minutes while the watchdog fired two hundred and six times. A run before it,
// with the same build minus the affordance question, walked to sixteen distinct
// positions. Offering a choice and not reading it is worse than not offering it,
// and no unit test caught it because every test proved the question was
// BUILT — not that its answer was USED.

// TestAnOfferedVerbIsSpent is the missing check. A picked verb has to reach
// doActivity, or the offer is a decoration the model can see and the body cannot.
func TestAnOfferedVerbIsSpent(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	b.Gatherer = nil
	r := agi.NewRunnerForTest(b, agi.Config{})

	// "place" is a registry action in the catalogue, so it survives the
	// catalogue-and-gate check and reaches the dispatch.
	r.RememberAffordanceForTest("place")
	verb, ok := r.TakeAffordanceForTest()
	if !ok {
		t.Fatal("a picked verb was discarded: the model chose it and nothing ran")
	}
	if verb != "place" {
		t.Errorf("verb = %q, want place", verb)
	}
}

// TestAVerbIsSpentOnlyOnce stops a stale answer being replayed on a later tick.
// An affordance that lingers is a bot acting on a decision the model withdrew.
func TestAVerbIsSpentOnlyOnce(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	r.RememberAffordanceForTest("place")
	if _, ok := r.TakeAffordanceForTest(); !ok {
		t.Fatal("the first spend did not produce a verb")
	}
	if _, ok := r.TakeAffordanceForTest(); ok {
		t.Error("the verb was spent twice: a stale decision replayed on a later tick")
	}
}

// TestAnInventedVerbIsDropped closes the hole the gate exists for. The model
// answers a question; an answer that names something the catalogue does not
// contain is the model having invented it, and executing an invented label is
// precisely the unverified world-change the affordance layer refuses.
func TestAnInventedVerbIsDropped(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	for _, invented := range []string{"detonate", "teleport", "launch_rocket", ""} {
		r.RememberAffordanceForTest(invented)
		if verb, ok := r.TakeAffordanceForTest(); ok {
			t.Errorf("invented verb %q was dispatched as %q", invented, verb)
		}
	}
}

// TestAnActivityVerbIsNotDispatchedAsALabel keeps the two vocabularies apart.
// "wander" is something the brain does; it is also a registry label. Dispatching
// it would run a different code path than the one the catalogue describes, and
// the model would be told it had wandered by a handler that walks somewhere
// else.
func TestAnActivityVerbIsNotDispatchedAsALabel(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	r.RememberAffordanceForTest("wander")
	if verb, ok := r.TakeAffordanceForTest(); ok {
		t.Errorf("an Activity verb was dispatched as a registry label: %q", verb)
	}
}

// Stage 3 end to end: the argument has to survive the whole trip.
//
// Every test above proves the verb arrives. None of them prove the parameter
// does, and a parameter that is dropped on the way is the worst kind of bug
// here: the verb still dispatches, the model still believes it chose what to
// take, and the handler guesses — which is exactly the state Stage 3 was built
// to end.

// TestTheArgumentTravelsWithTheVerb is the missing link.
//
// It observes the dispatch rather than the helper. The first version of this
// test called takeAffordanceParam directly and passed even after the call site
// was changed to pass an empty string — it proved the helper worked, not that
// the handler was given anything. A test has to watch the thing it is talking
// about.
func TestTheArgumentTravelsWithTheVerb(t *testing.T) {
	// Not parallel: the dispatch seam is a package-level var, so two tests
	// swapping it at once clobber each other and the failure looks like a
	// dropped argument rather than a data race. Serial is the honest choice.
	var got []agi.DispatchedVerb
	r := agi.NewRunnerForTest(&bot.Bot{Logger: slog.Default()}, agi.Config{})
	defer r.SetExecuteForTest(&got)()

	r.RememberAffordanceForTest("take:oak_log")
	r.DoActivityForTest("rest")

	if len(got) != 1 {
		t.Fatalf("dispatched %d verbs, want exactly 1: the model chose a verb and "+
			"what ran was not it", len(got))
	}
	if got[0].Param != "oak_log" {
		t.Errorf("handler received argument %q, want oak_log: the model chose what "+
			"to take and the handler would have had to guess", got[0].Param)
	}
	if got[0].Verb != "take:oak_log" {
		t.Errorf("dispatched verb %q, want take:oak_log", got[0].Verb)
	}
}

// TestAParameterisedVerbIsStillGated. Adding an argument must not make the
// catalogue any more permissive: the gate keys on the verb.
func TestAParameterisedVerbIsStillGated(t *testing.T) {
	t.Parallel()

	r := agi.NewRunnerForTest(&bot.Bot{Logger: slog.Default()}, agi.Config{})

	r.RememberAffordanceForTest("detonate:creeper")
	if verb, ok := r.TakeAffordanceForTest(); ok {
		t.Errorf("an invented verb with a plausible argument was dispatched as %q", verb)
	}
}

// TestABareVerbSendsNoArgument keeps the old contract intact for every verb that
// was never parameterised.
func TestABareVerbSendsNoArgument(t *testing.T) {
	// Serial for the same reason as TestTheArgumentTravelsWithTheVerb.
	var got []agi.DispatchedVerb
	r := agi.NewRunnerForTest(&bot.Bot{Logger: slog.Default()}, agi.Config{})
	defer r.SetExecuteForTest(&got)()

	r.RememberAffordanceForTest("place")
	r.DoActivityForTest("rest")

	if len(got) != 1 {
		t.Fatalf("dispatched %d verbs, want 1", len(got))
	}
	if got[0].Param != "" {
		t.Errorf("a bare verb sent the argument %q to the handler", got[0].Param)
	}
}
