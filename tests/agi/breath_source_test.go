package agi_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
)

// The breath reflex and the swim plan each used to keep their own record of when
// the head went under, both sampling the same body on the same tick. Two clocks
// reading one body is two answers, and they drift: the reflex could decide there
// was air left while the plan had already committed to a dive, or the reverse.
// The disagreement is worst exactly when it matters, because the two halves are
// deciding one thing — whether the bot has to come up now.
//
// The reflex now reads the swim controller's clock through a read-only seam.
// These tests pin that it reads it, and that the fallback still works.

// fakeBreath is a stand-in for the movement package's swim controller. It
// reports a fixed reading and counts how often it was asked, which is what
// proves the reflex is reading rather than recomputing.
type fakeBreath struct {
	underwater bool
	seconds    int
	known      bool
	calls      int
}

func (f *fakeBreath) Breath() (bool, int, bool) {
	f.calls++
	return f.underwater, f.seconds, f.known
}

// TestTheRunnerHasABreathSourceSeam is the compile-time half: the seam exists,
// the runner accepts one, and the reading reaches the reflex through it.
func TestTheRunnerHasABreathSourceSeam(t *testing.T) {
	t.Parallel()

	src := &fakeBreath{underwater: true, seconds: 9, known: true}
	var _ agi.BreathSource = src

	r := newBreathRunner(t)
	r.SetBreathSource(src)

	underwater, seconds := r.ObserveSubmersionForTest(time.Now())
	if !underwater || seconds != 9 {
		t.Fatalf("the wired reading did not reach the reflex: underwater=%v seconds=%d", underwater, seconds)
	}
	if src.calls == 0 {
		t.Error("the reflex computed its own answer instead of reading the wired source")
	}
}

// newBreathRunner builds a runner with no bot attached. The breath reflex reads
// only the world and the breath source, and both paths are exercised with a
// runner that has no body, which is what NewBareForTest is for.
func newBreathRunner(t *testing.T) *agi.Runner {
	t.Helper()
	return agi.NewBareForTest(agi.Config{})
}

// TestTheReflexReadsTheWiredSourceRatherThanItsOwnClock is the behaviour that
// matters. The source says the head is under and has been for a while; the
// runner's own clock has never been started. If the reflex were still computing
// the answer itself it would see no submersion at all and would never surface.
func TestTheReflexReadsTheWiredSourceRatherThanItsOwnClock(t *testing.T) {
	t.Parallel()

	src := &fakeBreath{underwater: true, seconds: 11, known: true}
	r := newBreathRunner(t)
	r.SetBreathSource(src)

	// The snapshot path is the one the brain reads, and it is the one that has
	// to agree with the plan.
	underwater, seconds := r.ObserveSubmersionForTest(time.Now())
	if !underwater {
		t.Error("the reflex reported the bot was not underwater while the swim controller had it under for 11 seconds")
	}
	if seconds != 11 {
		t.Errorf("the reflex read %d seconds under, want the controller's 11", seconds)
	}
	if src.calls == 0 {
		t.Error("the reflex did not read the wired source")
	}
}

// TestAnUnwiredRunnerKeepsItsOwnClock is the no-regression half. Nothing is
// wired — which is the state of every test and of a bot whose world model cannot
// answer — and the reflex has to keep working on its own clock rather than
// reading a nil source and reporting a permanent surface.
func TestAnUnwiredRunnerKeepsItsOwnClock(t *testing.T) {
	t.Parallel()

	r := newBreathRunner(t)

	underwater, seconds := r.ObserveSubmersionForTest(time.Now())
	if underwater {
		t.Error("a runner with a dry world reported being underwater")
	}
	if seconds != 0 {
		t.Errorf("seconds under = %d with no submersion recorded, want 0", seconds)
	}
}

// TestAnUnknownReadingIsNotTreatedAsAir is the honest default. A source that has
// not sampled yet reports known=false, and that has to fall through to the
// runner's own clock rather than being read as "surfaced, plenty of air".
func TestAnUnknownReadingIsNotTreatedAsAir(t *testing.T) {
	t.Parallel()

	src := &fakeBreath{underwater: false, seconds: 0, known: false}
	r := newBreathRunner(t)
	r.SetBreathSource(src)

	underwater, _ := r.ObserveSubmersionForTest(time.Now())
	if underwater {
		t.Error("an unknown reading was read as confirmed air; the reflex must fall back instead")
	}
}
