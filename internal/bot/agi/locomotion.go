package agi

import (
	"log/slog"
	"strings"
)

// LocomotionHint is how Jev wants the body to move while it carries out this
// tick's activity: a travel style, not a destination. The movement tick owns
// the physics; this only picks the gait.
//
// It is a hint on purpose. A model that could force a sprint into a wall would
// be a model with the keys to the physics, and collision, hunger drain and the
// path step sizes all stay exactly where they are. Sprint only changes how fast
// the walk runs and which input flags go out — never where the bot is allowed
// to go.
type LocomotionHint string

const (
	// LocomotionAuto leaves the gait to the movement rules: sprint on long
	// straight path stretches, walk everywhere else. Missing, empty or unknown
	// answers all land here, so a model that never heard of this question
	// changes nothing.
	LocomotionAuto LocomotionHint = ""
	// LocomotionWalk keeps both feet on the ground at walking pace. The
	// deliberate choice around other players, on ledges, and anywhere running
	// would read as panic.
	LocomotionWalk LocomotionHint = "walk"
	// LocomotionSprint runs. For covering ground when there is somewhere to be
	// and nobody to bump into.
	LocomotionSprint LocomotionHint = "sprint"
	// LocomotionSprintJump runs and jumps — the bunny-hop a player does
	// downhill or across flat open ground. Never chosen near a ledge the bot
	// has not measured: a sprint-jump off a cliff is still off a cliff.
	LocomotionSprintJump LocomotionHint = "sprint_jump"
)

// DropOK is Jev's authority for one deliberate leap off a ledge.
//
// It is separate from the gait because it is a different question. "Sprint" is
// how fast to travel; "drop" is whether to leave the ground at all, and the body
// already refuses cliffs on its own. A player who sees a five-block drop with
// the far side reachable leaps it, and a bot that cannot express that walks a
// staircase in both directions and calls it careful.
type DropOK bool

// applyDrop spends or withholds Jev's authority for a deliberate leap.
//
// The request is refused when there is no cliff, so a model that volunteers a
// leap every tick cannot turn the gate into an open door — and a request that
// was never granted has to be spent deliberately, not on the next convenient
// ledge half a minute later.
func (r *Runner) applyDrop(want bool) {
	if r == nil || r.b == nil || !want {
		return
	}
	if !r.b.RequestDrop() {
		r.b.Logger.Debug("AGI: drop refused, nothing to leap to",
			slog.Bool("willing", true))
	}
}

// applyLocomotion installs Jev's travel style for the trip that starts now.
// A walk or rest decision carries no gait, so a stale sprint from an earlier
// trip must not leak into it: only the two running gaits are stored, and
// anything else clears the latch back to the movement rules' own judgement.
func (r *Runner) applyLocomotion(hint LocomotionHint) {
	if r == nil || r.b == nil {
		return
	}
	switch hint {
	case LocomotionSprint, LocomotionSprintJump:
		r.b.SetSprintHint(hint == LocomotionSprintJump)
	default:
		r.b.ClearSprintHint()
	}
}

// parseLocomotion turns a raw Jev answer into a hint. Anything it does not
// recognise is auto rather than an error: the question is optional, and an
// older or narrower model answering "" must not break the tick.
func parseLocomotion(raw string) LocomotionHint {
	switch LocomotionHint(strings.ToLower(strings.TrimSpace(raw))) {
	case LocomotionWalk:
		return LocomotionWalk
	case LocomotionSprint:
		return LocomotionSprint
	case LocomotionSprintJump:
		return LocomotionSprintJump
	default:
		return LocomotionAuto
	}
}

// rememberAffordance holds the verb the model chose until doActivity spends it.
//
// It is deliberately not applied immediately: doActivity runs on the same tick
// and is the only place that knows who the message is for and whether the body
// is free to act.
func (r *Runner) rememberAffordance(picked string) {
	if r == nil || picked == "" {
		return
	}
	r.mu.Lock()
	r.pendingAffordance = strings.ToLower(strings.TrimSpace(picked))
	r.mu.Unlock()
}
