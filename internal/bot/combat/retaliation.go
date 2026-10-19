// Proportionate self-defence: how hard the bot answers being hit.
//
// The bot used to answer a single bare-handed punch with a full lethal duel —
// best weapon out, strafing the arena, chasing the hitter for up to 32 blocks,
// and not stopping until one of them died. That is not what a person does. A
// player poked once turns around, says something, and gets on with their day;
// only somebody who keeps swinging earns a fight.
//
// So the response is a ladder rather than a switch:
//
//	1 hit   -> warn   (react, no swing)
//	2-3     -> defend (swing back from where the bot stands, no chase, no
//	                    weapon draw, a few swings at most)
//	4+      -> duel   (a real fight, the existing doctrine)
//
// The ladder is per attacker and it decays: hits that stop for a while start
// over, because a scuffle twenty seconds ago is not evidence about now.

package combat

import (
	"sync"
	"time"
)

// RetaliationLevel is how hard the bot answers the current attacker.
type RetaliationLevel int

const (
	// RetaliationNone means nothing was hit and nothing is owed.
	RetaliationNone RetaliationLevel = iota
	// RetaliationWarn is the answer to a single hit: turn, look, say
	// something — explicitly not a swing.
	RetaliationWarn
	// RetaliationDefend is the answer to a short burst of hits: hit back,
	// but from where the bot is standing, with what it is already holding,
	// for a few swings at most. No chase, no weapon swap, no duel.
	RetaliationDefend
	// RetaliationDuel is the answer to a sustained attack: the ordinary duel
	// doctrine, because at that point the other player is actually fighting.
	RetaliationDuel
)

// String makes the level readable in a log line, which is where the
// distinction between "warned" and "answered" has to be auditable.
func (l RetaliationLevel) String() string {
	switch l {
	case RetaliationNone:
		return "none"
	case RetaliationWarn:
		return "warn"
	case RetaliationDefend:
		return "defend"
	case RetaliationDuel:
		return "duel"
	default:
		return "unknown"
	}
}

const (
	// retaliationDuelHits is how many hits inside one scuffle turn a response
	// into an actual duel. Four is the point where "that was an accident"
	// stops being a plausible reading of somebody's input.
	retaliationDuelHits = 4
	// retaliationQuietGap is how long the ladder remembers. A hit arriving
	// after this much quiet starts again at one: two unrelated pokes half a
	// minute apart are two pokes, not the start of a fight.
	retaliationQuietGap = 15 * time.Second
)

// ResponseForHits maps a hit count inside one scuffle to a response. It is a
// pure function so the ladder can be tested without a world.
func ResponseForHits(hits int) RetaliationLevel {
	switch {
	case hits <= 0:
		return RetaliationNone
	case hits == 1:
		return RetaliationWarn
	case hits < retaliationDuelHits:
		return RetaliationDefend
	default:
		return RetaliationDuel
	}
}

// HitTracker counts consecutive hits from one attacker and decides what they
// have earned. Zero value is ready to use.
type HitTracker struct {
	mu       sync.Mutex
	attacker string
	hits     int
	last     time.Time
}

// Record counts a hit and reports the response it earns, the total in this
// scuffle, and whether this hit started the scuffle. A different attacker, or
// one arriving after the quiet gap, starts over.
func (t *HitTracker) Record(attacker string, now time.Time) (RetaliationLevel, int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	fresh := attacker != t.attacker || t.last.IsZero() || now.Sub(t.last) > retaliationQuietGap
	if fresh {
		t.attacker = attacker
		t.hits = 0
	}
	t.hits++
	t.last = now
	return ResponseForHits(t.hits), t.hits, fresh
}

// Hits reports the count for the current attacker, for logging and tests.
func (t *HitTracker) Hits() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hits
}

// Attacker reports who the ladder is counting for.
func (t *HitTracker) Attacker() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.attacker
}

// Reset forgets the scuffle, which a successful disengage or an explicit
// player instruction does: the count must not survive a truce.
func (t *HitTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.attacker = ""
	t.hits = 0
	t.last = time.Time{}
}

// Defensive-answer limits. These are deliberately tighter than the duel's:
// defending is a place you stand, not a fight you run.
const (
	// defendDuration is how long one defensive answer lasts after the last
	// hit. The attacker who stops hitting is answered with the bot going
	// back to its business.
	defendDuration = 6 * time.Second
	// defendLeash is how close the attacker has to stay for the answer to
	// continue. Past it the bot does not follow: chasing is the aggressive
	// half of a fight, and a defender does not chase.
	defendLeash float32 = 4.5
	// defendMaxSwings caps the answer. A defender hits back enough to make
	// the point, not enough to farm the attacker's health bar.
	defendMaxSwings = 3
)
