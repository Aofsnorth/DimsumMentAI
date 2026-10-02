package config

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TickInterval is how long the AGI brain waits between ticks, and it accepts
// either one number or a range.
//
//	tick_interval_sec: 30      # exactly every 30s
//	tick_interval_sec: 1-2s    # somewhere in [1s, 2s], redrawn every tick
//
// The range exists because a fixed period is the one thing about a recorded bot
// that never looks like a person. A player does not decide "in twelve seconds I
// will do something" on a stopwatch; the gap between one action and the next is
// whatever the last action turned out to need. A brain on a fixed ticker
// therefore idles in visible lockstep — and it is the *idle* that reads, because
// a viewer learns the period within a minute and then waits for it.
//
// Both ends are inclusive of the range and both are drawn per tick, not once at
// startup: a range that produced the same average every time would still be a
// metronome in the same place.
//
// It is a type rather than two int fields because a pair of seconds and a range
// of seconds are the same setting, and asking the operator to remember which one
// they are using is how a config ends up with a jitter field left at zero.
type TickInterval struct {
	Min time.Duration
	Max time.Duration
}

// DefaultTickInterval is what an unset or unusable tick_interval_sec means.
//
// It is not a small number on purpose. A brain that wakes far too often does
// not think more, it spends its time waiting on the model and looks like a bot
// lagging; the reflex layer is what actually keeps the bot alive, and it has its
// own triggers that do not need the brain at all.
const DefaultTickInterval = 30 * time.Second

// TickIntervalFloor is the shortest gap the brain will ever run at.
//
// A zero or negative interval is not a fast brain, it is a busy loop: the loop
// would re-ask the model as fast as the network answers and produce no visible
// behaviour at all. Clamping is the only safe reading of a config typo, because
// the alternative is a bot that appears to hang.
const TickIntervalFloor = 500 * time.Millisecond

// NewTickInterval builds a fixed interval, floored.
func NewTickInterval(d time.Duration) TickInterval {
	return normaliseTickInterval(TickInterval{Min: d, Max: d})
}

// TickIntervalRange builds an interval drawn from [min, max], floored.
func TickIntervalRange(minD, maxD time.Duration) TickInterval {
	if maxD < minD {
		minD, maxD = maxD, minD
	}
	return normaliseTickInterval(TickInterval{Min: minD, Max: maxD})
}

func normaliseTickInterval(i TickInterval) TickInterval {
	if i.Min < TickIntervalFloor {
		i.Min = TickIntervalFloor
	}
	if i.Max < TickIntervalFloor {
		i.Max = TickIntervalFloor
	}
	if i.Max < i.Min {
		i.Max = i.Min
	}
	return i
}

// Draw returns one wait drawn from the range. A fixed interval always returns
// itself.
func (i TickInterval) Draw() time.Duration {
	i = normaliseTickInterval(i)
	if i.Max <= i.Min {
		return i.Min
	}
	return i.Min + time.Duration(rand.Int64N(int64(i.Max-i.Min)))
}

// String renders the range the way it is written in the config, so a log line
// and a config file can be compared by eye.
func (i TickInterval) String() string {
	i = normaliseTickInterval(i)
	if i.Max <= i.Min {
		return i.Min.String()
	}
	return fmt.Sprintf("%s-%s", i.Min, i.Max)
}

// UnmarshalYAML accepts a number of seconds, or a range.
//
// The number form is the one every existing config already uses and is decoded
// as a plain int, so nothing has to be rewritten. The range form borrows the
// episode brief's duration notation ("24m", "90s", "1h30m") rather than
// inventing a second one, because an operator who has already learned how to
// type a duration should not have to learn it again.
func (i *TickInterval) UnmarshalYAML(node *yaml.Node) error {
	raw := strings.TrimSpace(node.Value)
	if raw == "" {
		*i = NewTickInterval(DefaultTickInterval)
		return nil
	}

	if lo, hi, isRange := strings.Cut(raw, "-"); isRange {
		minD, err := parseTickBound(lo)
		if err != nil {
			return fmt.Errorf("tick_interval_sec: lower bound: %w", err)
		}
		maxD, err := parseTickBound(hi)
		if err != nil {
			return fmt.Errorf("tick_interval_sec: upper bound: %w", err)
		}
		*i = TickIntervalRange(minD, maxD)
		return nil
	}

	d, err := parseTickBound(raw)
	if err != nil {
		return fmt.Errorf("tick_interval_sec: %w", err)
	}
	*i = NewTickInterval(d)
	return nil
}

// parseTickBound reads one end of a range. A bare number is seconds, which is
// what the field has always meant; anything with a unit is a Go duration.
func parseTickBound(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty bound")
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		if seconds <= 0 {
			return 0, fmt.Errorf("%q is not a positive interval", raw)
		}
		return time.Duration(seconds * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%q is neither a number of seconds nor a duration (try 2, 2s or 1m30s)", raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q is not a positive interval", raw)
	}
	return d, nil
}
