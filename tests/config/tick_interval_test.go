package config_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/config"
	"gopkg.in/yaml.v3"
)

func decodeTick(t *testing.T, raw string) (config.TickInterval, error) {
	t.Helper()
	var cfg struct {
		AGI struct {
			TickInterval config.TickInterval `yaml:"tick_interval_sec"`
		} `yaml:"agi"`
	}
	err := yaml.Unmarshal([]byte("agi:\n  tick_interval_sec: "+raw+"\n"), &cfg)
	return cfg.AGI.TickInterval, err
}

// TestTickIntervalReadsTheOldNumberForm is the compatibility guard. Every config
// written before this existed says `tick_interval_sec: 30`, and it has to keep
// meaning exactly 30 seconds.
func TestTickIntervalReadsTheOldNumberForm(t *testing.T) {
	t.Parallel()

	got, err := decodeTick(t, "30")
	if err != nil {
		t.Fatalf("a plain number was rejected: %v", err)
	}
	if got.Min != 30*time.Second || got.Max != 30*time.Second {
		t.Fatalf("got %v, want a fixed 30s", got)
	}
	if got.Draw() != 30*time.Second {
		t.Fatalf("a fixed interval drew %v, want 30s", got.Draw())
	}
}

// TestTickIntervalReadsARange is the feature: an operator sets "1-2s" and every
// gap between ticks is a fresh draw inside that range.
func TestTickIntervalReadsARange(t *testing.T) {
	t.Parallel()

	got, err := decodeTick(t, "1-2s")
	if err != nil {
		t.Fatalf("a range was rejected: %v", err)
	}
	if got.Min != time.Second || got.Max != 2*time.Second {
		t.Fatalf("got %v, want [1s, 2s]", got)
	}

	seen := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		d := got.Draw()
		if d < got.Min || d > got.Max {
			t.Fatalf("draw %v outside [%v, %v]", d, got.Min, got.Max)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Fatalf("200 draws produced %d distinct values, want a real range rather than one repeated number", len(seen))
	}
}

// TestTickIntervalRangeUsesBothEnds checks the draw actually covers the range
// instead of clustering at the floor. A range that always returned its minimum
// would parse fine, pass a bounds check, and behave exactly like the fixed
// interval it was meant to replace.
func TestTickIntervalRangeUsesBothEnds(t *testing.T) {
	t.Parallel()

	got, err := decodeTick(t, "1-2s")
	if err != nil {
		t.Fatal(err)
	}

	low, high := 0, 0
	for i := 0; i < 400; i++ {
		switch d := got.Draw(); {
		case d <= 1500*time.Millisecond:
			low++
		case d > 1500*time.Millisecond:
			high++
		}
	}
	if low == 0 || high == 0 {
		t.Fatalf("draws only reached one end of the range: %d low, %d high", low, high)
	}
}

// TestTickIntervalAcceptsTheProjectsDurationNotation borrows the episode
// brief's notation so an operator only has to learn one way of writing a
// duration, and supports fractional seconds because that is the range people
// actually want for a brain tick.
//
// The two range cases pin the one ambiguity in the notation: a bare number is
// SECONDS, so "1-2" is one to two seconds and "1m-2m" is one to two minutes.
// "1-1m30s" is therefore 1s..90s, not 1m..90s — writing the unit on both sides
// is the only way to be sure, and the alternative is a range nobody expects.
func TestTickIntervalAcceptsTheProjectsDurationNotation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw     string
		wantMin time.Duration
		wantMax time.Duration
		clamped bool
	}{
		{raw: "2", wantMin: 2 * time.Second, wantMax: 2 * time.Second},
		{raw: "0.5s", wantMin: 500 * time.Millisecond, wantMax: 500 * time.Millisecond},
		{raw: "250ms", wantMin: config.TickIntervalFloor, wantMax: config.TickIntervalFloor, clamped: true},
		{raw: "1m", wantMin: time.Minute, wantMax: time.Minute},
		{raw: "1m30s", wantMin: 90 * time.Second, wantMax: 90 * time.Second},
		{raw: "1-2", wantMin: time.Second, wantMax: 2 * time.Second},
		{raw: "1m-2m", wantMin: time.Minute, wantMax: 2 * time.Minute},
		{raw: "1-1m30s", wantMin: time.Second, wantMax: 90 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()
			got, err := decodeTick(t, tt.raw)
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if got.Min != tt.wantMin || got.Max != tt.wantMax {
				t.Fatalf("got %v, want [%v, %v]", got, tt.wantMin, tt.wantMax)
			}
			if tt.clamped && got.Draw() < config.TickIntervalFloor {
				t.Fatalf("a clamped interval drew under the floor")
			}
		})
	}
}

// TestTickIntervalRejectsNonsense pins the error path. A typo in this field used
// to be a startup panic rather than a message, because the loop divided by it.
func TestTickIntervalRejectsNonsense(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"banana", "0", "-4", "1-", "-", "0s"} {
		if _, err := decodeTick(t, raw); err == nil {
			t.Errorf("%q was accepted as a tick interval", raw)
		}
	}
}

// TestTickIntervalNeverRunsFasterThanTheFloor is the safety guard on the whole
// feature. An operator who asks for a sub-second brain does not get a faster
// brain, they get a bot that re-asks the model as fast as the network answers
// and looks hung while doing it.
func TestTickIntervalNeverRunsFasterThanTheFloor(t *testing.T) {
	t.Parallel()

	got, err := decodeTick(t, "1ms-4ms")
	if err != nil {
		t.Fatalf("a very short range was rejected outright, want it clamped: %v", err)
	}
	for i := 0; i < 50; i++ {
		if d := got.Draw(); d < config.TickIntervalFloor {
			t.Fatalf("draw %v is under the %v floor", d, config.TickIntervalFloor)
		}
	}
}

// TestTickIntervalSwapsReversedBounds keeps "5-1s" from being a config that
// loads and then draws nothing sensible.
func TestTickIntervalSwapsReversedBounds(t *testing.T) {
	t.Parallel()

	got := config.TickIntervalRange(5*time.Second, time.Second)
	if got.Min != time.Second || got.Max != 5*time.Second {
		t.Fatalf("got %v, want the bounds swapped to [1s, 5s]", got)
	}
}

// TestTickIntervalStringMatchesTheConfig pins what the startup log prints, so a
// log line and a config file can be compared by eye.
func TestTickIntervalStringMatchesTheConfig(t *testing.T) {
	t.Parallel()

	if got := config.NewTickInterval(30 * time.Second).String(); got != "30s" {
		t.Errorf("fixed interval printed %q, want %q", got, "30s")
	}
	if got := (config.TickInterval{Min: time.Second, Max: 2 * time.Second}).String(); got != "1s-2s" {
		t.Errorf("range printed %q, want %q", got, "1s-2s")
	}
}
