// Doing nothing, on purpose, in a way that does not look like a crash.
//
// There are two kinds and the difference between them is the whole point:
//
//   - idle_alive keeps the small idle-nudge steps and the drifting gaze. The bot
//     is not doing anything, but its head moves and it shifts its weight, and a
//     viewer reads that as a person thinking.
//
//   - idle_still is genuinely motionless.
//
// A long recording needs both, and needs them in the right proportion. Forty
// seconds of motionless is a player who has walked away; two minutes of it on a
// stream reads as a crash. But a bot that is never motionless is a machine that
// cannot rest, and watching one for three hours is exhausting in a way that is
// hard to name — it is the difference between a companion and a screensaver.
//
// So the choice is made here, by a rule, rather than by whoever is driving. An
// AI asked "should I idle?" will pick one every time, and the recording gets a
// personality nobody chose.

package agi

import (
	"log/slog"
	"time"

	"bedrock-ai/internal/evidence"
)

// IdleMode is which kind of nothing to do.
type IdleMode int

const (
	// IdleBusy is not idle. It is the state where something else got chosen.
	IdleBusy IdleMode = iota
	// IdleAlive keeps the gaze and the weight shifts.
	IdleAlive
	// IdleStill is motionless.
	IdleStill
)

// String names the mode for a log line and the evidence record.
func (m IdleMode) String() string {
	switch m {
	case IdleAlive:
		return "idle_alive"
	case IdleStill:
		return "idle_still"
	default:
		return "busy"
	}
}

// idleWindows is the shape of a rest period, in seconds.
//
// The bounds are deliberately short. Long enough that a viewer reads it as
// "the bot is thinking" rather than "the bot froze", short enough that a bot
// which idles repeatedly returns to doing something every few seconds and the
// recording keeps moving.
const (
	minIdleSec = 6
	maxIdleSec = 40
	// A motionless rest gets a longer window than a living one. It costs the
	// viewer more to watch and less to look at, so the bot may sit still for
	// longer than it may fidget.
	minStillIdleSec = 10
	maxStillIdleSec = 55
)

// ChooseIdle decides what kind of nothing to do, for a given seed.
//
// It is a pure function of the seed so the whole decision is testable and
// reproducible: a run that picks "still" twice in a row on a 30% chance is a
// bug in the caller, not in the world.
//
// stillBias is the share of idles that are fully motionless. It belongs in
// config because it is a taste knob and a number in the code is a number
// somebody has to recompile to change.
func ChooseIdle(seed int, stillBias float64) IdleMode {
	if stillBias <= 0 {
		return IdleAlive
	}
	if stillBias >= 1 {
		return IdleStill
	}
	// A cheap, well-spread hash so consecutive seeds do not alternate.
	spread := (seed*1103515245 + 12345) & 0x7fffffff
	if float64(spread%1000)/1000.0 < stillBias {
		return IdleStill
	}
	return IdleAlive
}

// idlePick is the decision and how long it lasts, kept together so the caller
// cannot apply a duration from one decision to a mode from another.
type idlePick struct {
	Mode     IdleMode
	Duration time.Duration
}

// idlePickFor turns a seed and a mode into a concrete rest period.
//
// It is pure so the whole policy is testable without a clock and without a
// runner. The runner wrapper exists only to supply the seed and the config.
func idlePickFor(seed int, mode IdleMode) idlePick {
	// A motionless idle is allowed to run a little longer than a live one,
	// because it costs the viewer more to watch and less to look at.
	lo, hi := minIdleSec, maxIdleSec
	if mode == IdleStill {
		lo, hi = minStillIdleSec, maxStillIdleSec
	}
	span := hi - lo
	if span < 1 {
		span = 1
	}
	// A second, differently-mixed hash so a run of identical seeds does not
	// produce a run of identical durations.
	seconds := lo + (seed*2654435761)%span
	return idlePick{Mode: mode, Duration: time.Duration(seconds) * time.Second}
}

// planIdle decides a rest period from the clock and the runner's config.
func (r *Runner) planIdle(now time.Time) idlePick {
	r.mu.Lock()
	seed := r.seed
	r.seed++
	r.mu.Unlock()

	return idlePickFor(seed, ChooseIdle(seed, r.cfg.IdleStillBias))
}

// shouldIdle reports whether a rest period opened earlier is still running.
//
// The window is kept on the runner rather than recomputed each tick, because a
// duration that is re-derived from the clock every tick is a duration that can
// never end: the clock has moved, so "started + 30s" is always 30s away, and
// the bot idles forever.
func (r *Runner) shouldIdle(snap Snapshot) bool {
	now := nowish(snap)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idleUntil.IsZero() {
		return false
	}
	if now.Before(r.idleUntil) {
		return true
	}
	r.idleUntil = time.Time{}
	return false
}

// idle opens or continues a rest period.
//
// An idle already in progress is simply carried, not restarted. Restarting it
// every tick would stretch a rest period by thirty seconds each time, which
// means it never ends — the same trap as a deadline read fresh from config.
func (r *Runner) idle(snap Snapshot) {
	now := nowish(snap)

	// Read the existing window under the lock, then release it. planIdle takes
	// the lock itself, and sync.Mutex is not reentrant — holding it across that
	// call is a self-deadlock, which is the same trap that wedged Observe.
	r.mu.Lock()
	active := !r.idleUntil.IsZero() && now.Before(r.idleUntil)
	mode := r.idleMode
	r.mu.Unlock()

	if active {
		// Keep the body alive during an idle_alive: the gaze and the weight
		// shifts are what stop a resting player looking like a crash.
		if mode == IdleAlive && r.b != nil {
			r.b.StopMovement()
		}
		return
	}

	pick := r.planIdle(now)

	r.mu.Lock()
	r.idleUntil = now.Add(pick.Duration)
	r.idleMode = pick.Mode
	r.mu.Unlock()

	r.log().Info("AGI: idling",
		slog.String("mode", pick.Mode.String()),
		slog.Duration("for", pick.Duration),
	)
	r.note(evidence.KindIdle, pick.Mode.String(), map[string]any{
		"mode":     pick.Mode.String(),
		"duration": pick.Duration.String(),
	})

	if pick.Mode == IdleStill {
		// A genuine stop. No nudge, no drift — a motionless bot on purpose.
		if r.b != nil {
			r.b.StopMovement()
		}
	}
}

// idleProgress is how much of the current rest period is left, for the state
// text. Zero when not idling.
func (r *Runner) idleProgress(now time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idleUntil.IsZero() || !now.Before(r.idleUntil) {
		return 0
	}
	return r.idleUntil.Sub(now)
}
