package agi

import (
	"testing"
	"time"
)

// TestTickIntervalDrawStaysInsideItsRange is the loop-side guard. The runner
// redraws its wait on every pass, so a draw outside the configured range is a
// brain that either vanishes or does not sleep at all.
func TestTickIntervalDrawStaysInsideItsRange(t *testing.T) {
	t.Parallel()

	got := TimeRange{Min: time.Second, Max: 2 * time.Second}
	seen := map[time.Duration]bool{}
	for i := 0; i < 300; i++ {
		d := got.Draw()
		if d < got.Min || d > got.Max {
			t.Fatalf("draw %v outside [%v, %v]", d, got.Min, got.Max)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Fatalf("300 draws produced %d distinct waits, want the gap to actually vary", len(seen))
	}
}

// TestTickIntervalFallsBackWhenUnset is the one that matters most in practice.
// The loop used to divide the configured interval by itself to jitter the first
// tick, so a Runner with no interval — or a config that never set one — took
// rand.Intn(0) and panicked the moment the bot joined. The brain now draws from
// a floored range instead, so a missing setting is slow rather than fatal.
func TestTickIntervalFallsBackWhenUnset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   TimeRange
		want time.Duration
	}{
		{name: "zero value", in: TimeRange{}, want: 500 * time.Millisecond},
		{name: "negative", in: TimeRange{Min: -time.Second, Max: -time.Second}, want: 500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.in.Draw(); got != tt.want {
				t.Fatalf("Draw() = %v, want %v", got, tt.want)
			}
		})
	}

	// Reversed bounds are a typo, not a request for a 1s brain: the setting has
	// to keep the same meaning on both sides of the config boundary, so they are
	// swapped and both ends of the resulting range are honoured.
	inverted := TimeRange{Min: 5 * time.Second, Max: time.Second}
	for i := 0; i < 100; i++ {
		if d := inverted.Draw(); d < time.Second || d > 5*time.Second {
			t.Fatalf("draw %v outside the swapped [1s, 5s]", d)
		}
	}
}

// TestTickIntervalFloorKeepsAZeroGapFromBecomingABusyLoop pins the reason for
// the floor in operational terms: whatever the operator asked for, the gap never
// collapses to nothing, so the loop cannot re-ask the model as fast as the
// network replies and look hung while it does it.
func TestTickIntervalFloorKeepsAZeroGapFromBecomingABusyLoop(t *testing.T) {
	t.Parallel()

	got := TimeRange{Min: 0, Max: 10 * time.Millisecond}
	for i := 0; i < 100; i++ {
		if d := got.Draw(); d < 500*time.Millisecond {
			t.Fatalf("draw %v is under the half-second floor", d)
		}
	}
}

// TestTickIntervalStringIsReadableInTheLog keeps the startup line comparable to
// the config file by eye, which is the whole reason the range is logged at all.
func TestTickIntervalStringIsReadableInTheLog(t *testing.T) {
	t.Parallel()

	if got := (TimeRange{Min: time.Second, Max: 2 * time.Second}).String(); got != "1s-2s" {
		t.Errorf("range printed %q, want %q", got, "1s-2s")
	}
	if got := (TimeRange{Min: 30 * time.Second, Max: 30 * time.Second}).String(); got != "30s" {
		t.Errorf("fixed interval printed %q, want %q", got, "30s")
	}
}
