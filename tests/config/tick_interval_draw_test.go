package config_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/config"
)

// TestTickIntervalDrawNeverReachesTheUpperBound is the regression guard for the
// inclusive-range bug.
//
// Draw documents both ends as inclusive ("somewhere in [1s, 2s]"), and the
// previous implementation used rand.Int64N(max-min), which can only ever return
// 0..max-min-1. A "1-2s" range therefore never produced 2s: the top nanosecond
// was unreachable and any test or operator expectation built on the documented
// range was wrong by one tick.
func TestTickIntervalDrawNeverReachesTheUpperBound(t *testing.T) {
	t.Parallel()

	const draws = 20000
	// A 2ns-wide range keeps the draw's outcome set small enough that "did the
	// upper bound ever come up" is a meaningful question in 20,000 draws. With a
	// 20ms range, hitting the single exact nanosecond of Max is a 1-in-20-million
	// event and the test would be vacuous.
	interval := config.TickIntervalRange(600*time.Millisecond, 600*time.Millisecond+2)

	sawMax := false
	for i := 0; i < draws; i++ {
		d := interval.Draw()
		if d < interval.Min || d > interval.Max {
			t.Fatalf("draw %v outside [%v, %v]", d, interval.Min, interval.Max)
		}
		if d == interval.Max {
			sawMax = true
		}
	}
	if !sawMax {
		t.Errorf("no draw in %d attempts reached Max=%v; the upper bound is exclusive, "+
			"which contradicts the documented inclusive range", draws, interval.Max)
	}
}

// TestTickIntervalDrawStaysWithinASingleNanosecondRange keeps the inclusive fix
// from dividing by zero or overshooting when both ends are equal.
func TestTickIntervalDrawStaysWithinASingleNanosecondRange(t *testing.T) {
	t.Parallel()

	interval := config.TickInterval{Min: time.Second, Max: time.Second}
	for i := 0; i < 100; i++ {
		if got := interval.Draw(); got != time.Second {
			t.Fatalf("a fixed interval drew %v, want exactly 1s", got)
		}
	}
}
