// Keeping the bot alive across a recording nobody is watching.
//
// A three-hour stream is not a session anybody is present for. Somewhere in
// three hours the bot will die, wedge itself against a wall, or lose the
// connection, and the difference between a recording and a crash is entirely
// whether anything was there to notice.
//
// The decisions are kept pure and separate from the reconnect machinery, for
// the same reason the survival manager's are: a judgement that can only be
// exercised by actually dying is a judgement that never gets exercised. Every
// case below has a test, and none of them need a server.

package agi

import (
	"fmt"
	"time"
)

// Health is what the watchdog can see about the bot right now.
type Health struct {
	// HP is the bot's health, 0 when dead.
	HP int
	// Disconnected is true when the connection is gone.
	Disconnected bool
	// LastMoved is when the bot last changed position. The zero value means it
	// has never moved, which is also "stuck" and is treated as such.
	LastMoved time.Time
	// Now is the current time.
	Now time.Time
	// PositionChanged is the strongest signal available: whether the bot's
	// coordinates differ from the last reading. It beats a clock because a
	// server that applies a freeze still answers ticks, and time alone would
	// call a motionless bot healthy.
	PositionChanged bool
	// WantsToMove reports that the body has an outstanding reason to be in
	// motion: it is walking somewhere, following someone, part-way along a
	// path, or out exploring.
	//
	// It is the question the stuck judgement actually turns on. Stillness means
	// nothing on its own — a bot that decided to rest, or that is standing in
	// front of a tree swinging an axe, is exactly as motionless as one wedged
	// against terrain, and only one of the three is broken.
	WantsToMove bool
	// Exploring is true when the bot is out wandering on purpose.
	//
	// An exemption rather than an intent, and a stronger one than WantsToMove:
	// a wanderer pauses to look at things constantly, and a watchdog that fires
	// on those twitches the bot out of its own journey every thirty seconds.
	Exploring bool
}

// Fault is what is wrong.
type Fault int

const (
	// FaultNone is the ordinary case.
	FaultNone Fault = iota
	// FaultDead means respawn, and nothing else. Everything else is secondary
	// to being alive.
	FaultDead
	// FaultDisconnected means reconnect; the session is over but the recording
	// is not, and a bot that gives up on a blip ends a three-hour stream in
	// minute five.
	FaultDisconnected
	// FaultStuck means the bot is alive and going nowhere, which is what a
	// wedge against terrain looks like from the outside.
	FaultStuck
)

// String names the fault for a log line.
func (f Fault) String() string {
	switch f {
	case FaultDead:
		return "dead"
	case FaultDisconnected:
		return "disconnected"
	case FaultStuck:
		return "stuck"
	default:
		return "fine"
	}
}

// Action is what to do about a fault.
type Action int

const (
	// ActNone is "carry on".
	ActNone Action = iota
	// ActRespawn puts the bot back in the world.
	ActRespawn
	// ActReconnect dials the server again.
	ActReconnect
	// ActUnstick tries to get the bot out of whatever it is wedged against.
	ActUnstick
)

// String names the action for a log line.
func (a Action) String() string {
	switch a {
	case ActRespawn:
		return "respawn"
	case ActReconnect:
		return "reconnect"
	case ActUnstick:
		return "unstick"
	default:
		return "carry on"
	}
}

// stuckAfter is how long the bot can go without changing position before it is
// called stuck.
//
// It is long enough that a deliberate pause, an idle, or a chat turn does not
// trip it, and short enough that a wedge is noticed inside one tick of the
// recording looking wrong. Thirty seconds is roughly the tick interval, so a
// stuck bot is caught on the very next decision rather than a minute later.
const stuckAfter = 30 * time.Second

// StuckNeedsRepeats is how many consecutive readings must agree before the
// watchdog acts.
//
// One reading is not enough: a single tick can be a server hiccup, and a bot
// that panics at the first one spends a three-hour stream reconnecting to a
// problem it did not have. Two is the smallest number that is actually evidence.
const StuckNeedsRepeats = 2

// Diagnose names the fault, and only one.
//
// The order is deliberate. A dead bot is also a motionless bot and a
// disconnected bot is also a motionless one, so checking for stillness first
// would unstick a corpse. And a disconnect is more urgent than being wedged,
// because nothing else can be tried until the connection is back.
func Diagnose(h Health) Fault {
	if h.Disconnected {
		return FaultDisconnected
	}
	if h.HP <= 0 {
		return FaultDead
	}
	if IsStuck(h) {
		return FaultStuck
	}
	return FaultNone
}

// IsStuck reports whether the bot has been motionless too long to be deliberate.
//
// The exemption is the whole judgement. Stillness is only a fault when the body
// was trying to move and did not: a bot resting, chatting, or standing still to
// break a block is supposed to leave its position alone, and calling that stuck
// produces a bot that interrupts itself every thirty seconds. It also produces
// the stutter this was found by — a live run had the watchdog fire ActUnstick
// sixteen times in a row on a resting bot, each one clearing the world model and
// none of them moving the body, which is visible as a repeated hop that goes
// nowhere.
//
// A body that has never moved is different and stays stuck regardless: that is a
// bot that has not started, and no resting bot reaches it, because the first
// reading stamps LastMoved.
func IsStuck(h Health) bool {
	if h.Exploring || h.HP <= 0 || h.Disconnected {
		return false
	}
	if h.PositionChanged {
		return false
	}
	if h.LastMoved.IsZero() {
		// Never moved at all. That is a bot that has not started, and it is
		// stuck by any reasonable reading.
		return true
	}
	if !h.WantsToMove {
		// Stillness with no reason to be moving is rest, not a wedge.
		return false
	}
	return h.Now.Sub(h.LastMoved) > stuckAfter
}

// Decide returns the fault and the action together, counting repeats for the
// stuck case so the caller can just obey it.
//
// repeats is how many consecutive ticks the same fault has been observed, and
// it is only trusted for stuck: death and disconnection are unambiguous and
// acting on the first sighting is right, because waiting to confirm a dead bot
// means lying on the respawn screen for another tick.
func Decide(h Health, repeats int) (Fault, Action) {
	fault := Diagnose(h)
	switch fault {
	case FaultDisconnected:
		return fault, ActReconnect
	case FaultDead:
		return fault, ActRespawn
	case FaultStuck:
		if repeats < StuckNeedsRepeats {
			// Not yet. The one thing the watchdog must not do is thrash.
			return fault, ActNone
		}
		return fault, ActUnstick
	default:
		return FaultNone, ActNone
	}
}

// WatchReport is the one-line summary of a watchdog pass, for the log.
func WatchReport(fault Fault, action Action, repeats int) string {
	if fault == FaultNone {
		return ""
	}
	return fmt.Sprintf("watchdog: %s -> %s (seen %d time(s) in a row)", fault, action, repeats)
}
