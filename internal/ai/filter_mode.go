package ai

import "sync/atomic"

// Whether a contradicting narration is replaced, or only counted.
//
// This exists because the filter was switched on before it had ever been
// measured. The marker lists are a few dozen words of Indonesian and English
// chosen by reading the prompt corpus, and no real model output has ever been
// run past them. Enabling the replacement cold means betting the bot's ordinary
// conversation on that guess — and the failure mode is not a cosmetic one: a
// false positive swaps an honest "8 logs!" for a fallback that says something
// closer to the opposite, so the bot starts contradicting results that really
// happened. A filter that cannot yet be shown to be right should not be allowed
// to change what a player sees.
//
// So the default is shadow. Every narration is checked and every contradiction
// is counted and logged, but the model still says what it said. The counters
// below are what turns "we think this filter is right" into a number, and
// enforcing is then a one-line decision made with evidence rather than with
// hope.

// FilterMode says what to do with a narration that contradicts the server.
type FilterMode int

const (
	// ModeShadow checks and counts, and sends the model's own text. The default.
	ModeShadow FilterMode = iota
	// ModeEnforce replaces a contradicting narration with the server's own
	// wording. Only worth turning on once the shadow counters show the filter
	// does not fire on honest replies.
	ModeEnforce
)

// String names the mode for a log line or a config dump.
func (m FilterMode) String() string {
	if m == ModeEnforce {
		return "enforce"
	}
	return "shadow"
}

// filterMode holds the current setting. Atomic rather than mutex-guarded: it is
// read on every status reply and written only when someone changes a setting.
var filterMode atomic.Int32

// NarrationCounters is the measurement the shadow mode exists to produce.
//
// The split that matters is not the total but the false-positive direction: a
// narration flagged while the action SUCCEEDED is the filter arguing with a real
// result, and that is the number that has to be near zero before enforcing.
type NarrationCounters struct {
	// Checked is how many narrations were compared against a verdict.
	Checked uint64
	// Contradicted is how many did not match, in either direction.
	Contradicted uint64
	// ContradictedFailure is a success claimed over a refused action. This is
	// the lie the filter exists to stop.
	ContradictedFailure uint64
	// ContradictedSuccess is a refusal claimed over a confirmed action. This is
	// the filter being wrong, and it is the one that would make enforcement
	// harmful.
	ContradictedSuccess uint64
}

// SetFilterMode chooses whether contradictions are replaced or only counted.
// It returns the mode that was in force, so a caller can restore it.
func SetFilterMode(m FilterMode) FilterMode {
	return FilterMode(filterMode.Swap(int32(m)))
}

// FilterMode returns the current setting.
func CurrentFilterMode() FilterMode {
	return FilterMode(filterMode.Load())
}

var (
	counterChecked          atomic.Uint64
	counterContradicted     atomic.Uint64
	counterContradictedFail atomic.Uint64
	counterContradictedOK   atomic.Uint64
)

// RecordNarrationVerdict counts one comparison and returns whether it matched.
//
// It is exported so the caller reports what it actually sent, which is the only
// way a shadow-mode measurement is worth anything: counting a contradiction and
// then enforcing anyway would be a lie in the other direction.
func RecordNarrationVerdict(checked, matched bool, truth Outcome) bool {
	if !checked {
		return true
	}
	counterChecked.Add(1)
	if matched {
		return true
	}
	counterContradicted.Add(1)
	if truth.Success {
		counterContradictedOK.Add(1)
	} else {
		counterContradictedFail.Add(1)
	}
	return false
}

// NarrationStats returns a snapshot of the counters.
func NarrationStats() NarrationCounters {
	return NarrationCounters{
		Checked:             counterChecked.Load(),
		Contradicted:        counterContradicted.Load(),
		ContradictedFailure: counterContradictedFail.Load(),
		ContradictedSuccess: counterContradictedOK.Load(),
	}
}

// ResetNarrationStats clears the counters, for a measurement window that starts
// at a known point.
func ResetNarrationStats() {
	counterChecked.Store(0)
	counterContradicted.Store(0)
	counterContradictedFail.Store(0)
	counterContradictedOK.Store(0)
}
