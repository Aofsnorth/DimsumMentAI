package agi

import (
	"strings"
	"time"

	"bedrock-ai/internal/jev"
)

// GazeHint is what Jev wants the head to settle on while the bot is standing
// still: a player, a creature, a block, or nothing in particular.
//
// It is a hint, on the same terms as LocomotionHint, and for the same reason.
// The bot could already look at things — the idle gaze settles on players,
// creatures and blocks on its own — but the choice was a dice roll the model
// had no say in. This hands over that one decision and nothing else.
//
// The boundaries are what matter. A model that could turn the body would be a
// model with the keys to the physics: walking, collision, the path step sizes
// and the jump all stay exactly where they are. A gaze hint only ever changes
// which of the movement layer's OWN existing look strategies runs next. If the
// model asks for a player and there is nobody nearby, the request is not
// honoured by the bot staring at a wall — the existing cascade runs instead,
// because a bot that picks something to look at and then cannot find it looks
// broken in a way no amount of extra realism elsewhere repairs.
type GazeHint string

const (
	// GazeAuto leaves the gaze to the movement rules, which is what happened
	// before this existed. Missing, empty or unrecognised answers all land here,
	// so a model that never heard of the question changes nothing at all.
	GazeAuto GazeHint = ""
	// GazeAtPerson settles on the nearest player.
	GazeAtPerson GazeHint = "person"
	// GazeAtMob settles on the nearest creature.
	GazeAtMob GazeHint = "mob"
	// GazeAtBlock settles on a nearby block.
	GazeAtBlock GazeHint = "block"
	// GazeWanderly looks at nothing in particular, which is what a person does
	// when nothing in view is worth watching.
	GazeWanderly GazeHint = "around"
)

// gazeHold is how long one gaze instruction stays in force.
//
// It exists because the brain decides on a slower clock than the body moves. A
// hint with no expiry would have the bot watching a player who walked away
// twenty minutes ago, because some tick long ago decided that and nothing ever
// cleared it. The movement layer re-checks the target is still there on every
// fixation; this bounds the window the instruction itself lives in.
const gazeHold = 25 * time.Second

// parseGaze turns a raw Jev answer into a hint. Anything unrecognised is auto
// rather than an error, exactly as with locomotion: the question is optional,
// and a model answering "" or something invented must not break the tick.
func parseGaze(raw string) GazeHint {
	switch GazeHint(strings.ToLower(strings.TrimSpace(raw))) {
	case GazeAtPerson:
		return GazeAtPerson
	case GazeAtMob:
		return GazeAtMob
	case GazeAtBlock:
		return GazeAtBlock
	case GazeWanderly:
		return GazeWanderly
	default:
		return GazeAuto
	}
}

// applyGaze installs Jev's attention for the next little while.
//
// A gaze that is over — or never given — is cleared rather than left to expire
// on its own, so a model that stops caring about a player stops looking at
// them on the very next decision instead of up to gazeHold later.
func (r *Runner) applyGaze(hint GazeHint) {
	if r == nil || r.b == nil {
		return
	}
	if hint == GazeAuto {
		r.b.ClearGazeHint()
		return
	}
	r.b.SetGazeHint(string(hint), time.Now().Add(gazeHold))
}

// gazeAppliesTo reports whether an activity is one where the bot's head is free
// and a gaze instruction could be honoured.
//
// Wandering and exploring are excluded on purpose even though they are the
// activities most likely to be described as "looking around". While the body is
// travelling, the head is committed to the direction of travel: that is what
// makes a walking bot read as a walking bot rather than a walking statue. The
// hint is still recorded for those ticks and takes effect the moment the bot
// stops, which is when the question of what to look at actually arises.
func gazeAppliesTo(activity string) bool {
	switch activity {
	case jev.ActivityRest, jev.ActivitySleep, jev.ActivityWander, jev.ActivityExplore:
		return true
	default:
		return false
	}
}
