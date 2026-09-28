// Knowing when the inventory is lying to us, and what to do about it.
//
// The container session tracks what the server last told it. It does not notice
// when the bot's own view has drifted away from that, and nothing sweeps a
// cursor item back into the bag except a craft that already failed. Between a
// dropped-stack timeout, a rejected request and a container the server closed
// early, there is a window where the bot believes it is holding something it is
// not — or holding nothing while something is stuck to its mouse.
//
// That window is where the worst failures live, because they are silent. A bot
// that thinks it has a stack it does not have will craft with it, fail, and
// report the craft as the problem. The actual problem is three steps earlier.
//
// The checks here are pure over a snapshot of what is known, so the policy is
// testable without a server, a connection, or an item that has to be genuinely
// lost first.

package inventory

import "sort"

// View is what the bot believes about its own inventory at one moment.
//
// It is assembled from two sources that can disagree: what the bot last saw and
// what the server last confirmed. Disagreement between them is the entire
// subject of this file.
type View struct {
	// SlotCounts is the bot's own view, slot to stack size.
	SlotCounts map[uint32]int
	// ConfirmedSlotCounts is what the server last reported, when there is one.
	// Nil means the bot has never had a confirmation to compare against, which
	// is a normal state right after joining and not a fault.
	//
	// A non-nil map is treated as a COMPLETE snapshot, not a partial one. That
	// is the only reading under which the comparison means anything: if the
	// server has enumerated the inventory, then a slot it mentions that the bot
	// does not know about is an item the bot is about to act without — and
	// treating that as "a gap, not a disagreement" would be exactly the bug.
	ConfirmedSlotCounts map[uint32]int
	// CursorStack is what is sitting on the cursor, if anything. A non-empty
	// value here is not an error — it is what a player looks like mid-drag — but
	// it is not survivable indefinitely, and nothing else will clear it.
	CursorStack string
	// ContainerOpen is true while a container session is live.
	ContainerOpen bool
	// LastRequestFailed is true when the most recent stack request was rejected
	// or timed out.
	LastRequestFailed bool
}

// Problem is one thing that is wrong with the bot's inventory state.
type Problem int

const (
	// ProblemNone means nothing needs doing.
	ProblemNone Problem = iota
	// ProblemCursorStuck means an item is sitting on the cursor. Left there it
	// is invisible to every slot operation the bot performs, so the next thing
	// it tries to do either fails or puts the item somewhere unintended.
	ProblemCursorStuck
	// ProblemCountDrift means the bot's view of a stack disagrees with the
	// server's. The bot is about to act on a number that is not true.
	ProblemCountDrift
	// ProblemStaleContainer means a container session is open but the bot is
	// not the one driving it. Every transfer against it will be rejected.
	ProblemStaleContainer
)

// String names the problem for a log line.
func (p Problem) String() string {
	switch p {
	case ProblemCursorStuck:
		return "an item is stuck on the cursor"
	case ProblemCountDrift:
		return "the inventory does not match what the server said"
	case ProblemStaleContainer:
		return "a container is open that the bot is not driving"
	default:
		return "nothing wrong"
	}
}

// Recovery is what to do about a problem.
type Recovery int

const (
	// RecoverNone means leave it alone.
	RecoverNone Recovery = iota
	// RecoverCursor puts the cursor item back in the bag.
	RecoverCursor
	// RecoverResync asks the server what is actually held and believes the
	// answer.
	RecoverResync
	// RecoverCloseContainer backs out of a container the bot is not driving.
	RecoverCloseContainer
)

// String names the recovery for a log line.
func (r Recovery) String() string {
	switch r {
	case RecoverCursor:
		return "return the cursor item to the bag"
	case RecoverResync:
		return "re-read the inventory from the server"
	case RecoverCloseContainer:
		return "close the container"
	default:
		return "do nothing"
	}
}

// Diagnose names the single most urgent problem with a view.
//
// One at a time, in a fixed order, is deliberate. A bot that tries to fix three
// things at once has no way to tell which fix worked, and inventory operations
// interfere with each other: a resync issued while a cursor item is still
// attached reports a view the bot cannot act on, and a container closed while a
// transfer is in flight loses the transfer's result.
//
// The order is cheapest-and-most-blocking first. A cursor item makes every other
// operation unreliable, so it goes before anything else.
func Diagnose(v View) Problem {
	switch {
	case v.CursorStack != "":
		return ProblemCursorStuck
	case v.StaleContainer():
		return ProblemStaleContainer
	case v.Drifted():
		return ProblemCountDrift
	default:
		return ProblemNone
	}
}

// Plan says what to do about a view.
//
// It is separate from Diagnose so a caller can report the problem in words and
// act on it in a different place — the log and the wire do not have to happen
// at the same instant, and coupling them is how a log line ends up claiming a
// recovery that then does not happen.
func Plan(v View) (Problem, Recovery) {
	problem := Diagnose(v)
	switch problem {
	case ProblemCursorStuck:
		return problem, RecoverCursor
	case ProblemStaleContainer:
		return problem, RecoverCloseContainer
	case ProblemCountDrift:
		return problem, RecoverResync
	default:
		return problem, RecoverNone
	}
}

// StaleContainer reports whether a container is open that nothing is driving.
//
// A container left open after a request died is the state that makes every
// subsequent transfer fail with a rejection the bot cannot explain: the session
// it is writing to belongs to an exchange that already timed out.
func (v View) StaleContainer() bool {
	return v.ContainerOpen && v.LastRequestFailed
}

// Drifted reports whether the bot's own count for any slot disagrees with the
// server's.
//
// A slot the bot has never heard of counts as drift, because a non-nil
// confirmation is a complete snapshot: the server has told us what is held, and
// a slot in that list missing from the bot's own view is an item the bot is
// about to act without. The one case that is NOT drift is having no confirmation
// at all, which is a normal state right after joining rather than a fault.
func (v View) Drifted() bool {
	if v.ConfirmedSlotCounts == nil {
		return false
	}
	for slot, confirmed := range v.ConfirmedSlotCounts {
		believed, known := v.SlotCounts[slot]
		if !known {
			believed = 0
		}
		if believed != confirmed {
			return true
		}
	}
	return false
}

// DriftedSlots lists the slots where the two views disagree, lowest first.
//
// Sorted rather than in map order, for the same reason every other map walk in
// this codebase is sorted: an unordered list of problems is a log that reads
// differently every time, which makes it impossible to tell whether anything
// actually changed.
func (v View) DriftedSlots() []uint32 {
	if v.ConfirmedSlotCounts == nil {
		return nil
	}
	var out []uint32
	for slot, confirmed := range v.ConfirmedSlotCounts {
		believed := v.SlotCounts[slot]
		if believed != confirmed {
			out = append(out, slot)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MergeConfirmed folds a server-confirmed view into the bot's own, returning
// the corrected counts.
//
// The server is right about counts and the bot is right about nothing here:
// a count is a fact the server owns, and an argument about it is a bug on this
// side. Slots the server says are empty are removed rather than zeroed, so a
// stale belief about a slot that has since been emptied does not linger as a
// phantom entry.
func MergeConfirmed(believed, confirmed map[uint32]int) map[uint32]int {
	out := make(map[uint32]int, len(confirmed))
	for slot, count := range confirmed {
		if count <= 0 {
			continue
		}
		out[slot] = count
	}
	// Slots the server has never mentioned are kept: an absent key means "not
	// confirmed", which is not the same as "empty", and throwing the bot's
	// belief away for everything unconfirmed would empty a whole inventory that
	// the server simply has not enumerated yet.
	for slot, count := range believed {
		if _, confirmedAlready := confirmed[slot]; confirmedAlready {
			continue
		}
		if count > 0 {
			out[slot] = count
		}
	}
	return out
}
