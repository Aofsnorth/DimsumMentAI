// Dive and breath planning: how deep the body should be, and when to stop
// caring about that because the air bar is running out.
//
// This is the half of water movement that is pure on purpose. "The bot surfaces
// before it drowns" is a claim about arithmetic over a countdown, and arithmetic
// over a countdown is exactly the kind of thing that has to be testable without
// a server, a world, a body and fifteen seconds of real time. Every function
// here takes its inputs and returns a decision; the controller in swim.go is
// the only part that holds state.

package movement

import (
	"fmt"
	"time"
)

// Breath budget.
//
// Vanilla gives a player fifteen seconds of air, and the bot is a player on the
// wire as far as the server is concerned. The reserve is a third of the budget:
// the bot starts climbing with five seconds in hand, which is roughly two
// seconds of rise for a four-block shaft and leaves room for the body to be
// slower than the plan wanted. A bot that surfaces at zero is a bot that
// surfaces one tick after it needed to.
const (
	DefaultBreathBudget  = 15 * time.Second
	breathBudgetFloor    = 1
	breathReserveDivisor = 3
)

// BreathState is how much air is left, counted in whole seconds.
//
// Seconds rather than ticks because that is the unit the submersion clock and
// every log line already speak, and because at 20 ticks a second the two are
// the same decision to within a twentieth of a second.
type BreathState struct {
	// SecondsUnder is how long the head has been in the water.
	SecondsUnder int
	// Budget is the whole air bar, in seconds.
	Budget int
}

// NewBreathState builds a breath reading from an elapsed submersion time.
//
// A non-positive budget is clamped to one second rather than treated as
// "unlimited". A dive planner that believes it has infinite air is precisely
// the planner that drowns, and there is no caller in this codebase for which
// the honest answer is an unbounded budget.
func NewBreathState(secondsUnder, budgetSeconds int) BreathState {
	if budgetSeconds < breathBudgetFloor {
		budgetSeconds = breathBudgetFloor
	}
	if secondsUnder < 0 {
		secondsUnder = 0
	}
	return BreathState{SecondsUnder: secondsUnder, Budget: budgetSeconds}
}

// ReserveSeconds is how much air is kept back for the climb itself.
func (b BreathState) ReserveSeconds() int { return b.Budget / breathReserveDivisor }

// SecondsLeft is the air remaining, floored at zero so a spent bar never reads
// as a fresh one.
func (b BreathState) SecondsLeft() int {
	if b.SecondsUnder >= b.Budget {
		return 0
	}
	return b.Budget - b.SecondsUnder
}

// MustSurface reports that there is no air left at all.
func (b BreathState) MustSurface() bool { return b.SecondsLeft() <= 0 }

// ReserveReached reports that the climb has to start now.
func (b BreathState) ReserveReached() bool { return b.SecondsLeft() <= b.ReserveSeconds() }

// DepthAction is what the body should do about its depth this tick.
type DepthAction string

const (
	// DepthDescend pushes the body down toward the dive target.
	DepthDescend DepthAction = "descend"
	// DepthHold keeps the body where it is.
	DepthHold DepthAction = "hold"
	// DepthAscend brings the body up, toward the surface or the dive target.
	DepthAscend DepthAction = "ascend"
)

// DiveIntent is the caller's ask: reach a depth and stay there.
//
// It is a value, not a pointer, and it has no state of its own. A dive is a
// request; whether it is being honoured is the plan's answer, and keeping those
// two apart is what lets a breath reflex suspend a dive without cancelling it.
type DiveIntent struct {
	// Active is false for a bot nobody asked to go under.
	Active bool
	// TargetY is the feet cell the dive is trying to reach. Larger is higher.
	TargetY int32
}

// NewDiveIntent asks for a depth.
func NewDiveIntent(targetY int32) DiveIntent {
	return DiveIntent{Active: true, TargetY: targetY}
}

// WantsDescendAt reports whether a body at feetY is above the target and so
// needs to go down. The direction convention is the one that flips silently:
// larger Y is higher, so "above the target" is a larger Y.
func (d DiveIntent) WantsDescendAt(feetY int32) bool {
	return d.Active && feetY > d.TargetY
}

// WantsAscendAt is the mirror.
func (d DiveIntent) WantsAscendAt(feetY int32) bool {
	return d.Active && feetY < d.TargetY
}

// DepthPlan is the decision for one tick.
type DepthPlan struct {
	// Action is what the body does vertically.
	Action DepthAction
	// TargetY is the feet cell that action is heading for.
	TargetY int32
	// Reason is the sentence a log line needs to explain the bot to a human.
	Reason string
	// Suspended is true when a live dive was parked so the body could breathe.
	// The dive is still wanted; it is just not this tick's business.
	Suspended bool
	// Blind is true when no surface was found and the climb is a guess. It is
	// carried so the controller can say so out loud rather than climbing in
	// silence at the bottom of a flooded shaft.
	Blind bool
}

// PlanDepth decides how deep the body should be.
//
// The order of the rules is the whole design: breath first, then the dive, then
// the default. A reflex that can be outvoted by the thing it exists to prevent
// is not a reflex, and the reason a dive is suspended rather than cancelled is
// that the caller asked for an item three blocks down, not for a swim.
func PlanDepth(sub Submersion, dive DiveIntent, breath BreathState) DepthPlan {
	if breath.ReserveReached() {
		return DepthPlan{
			Action:    DepthAscend,
			TargetY:   sub.SurfaceY,
			Reason:    breathReason(breath),
			Suspended: dive.Active,
			Blind:     !sub.HasSurface,
		}
	}

	if !dive.Active {
		if sub.Submerged {
			// Nobody asked for this. The body is under and the air bar is
			// running, so the answer is the same one the AGI reflex gives,
			// arrived at here rather than there.
			return DepthPlan{
				Action:  DepthAscend,
				TargetY: sub.SurfaceY,
				Reason:  "submerged with no dive intent",
				Blind:   !sub.HasSurface,
			}
		}
		return DepthPlan{Action: DepthHold, TargetY: sub.FeetY, Reason: "at the surface"}
	}

	switch {
	case dive.WantsDescendAt(sub.FeetY):
		return DepthPlan{Action: DepthDescend, TargetY: dive.TargetY, Reason: "feet above the dive target"}
	case dive.WantsAscendAt(sub.FeetY):
		return DepthPlan{Action: DepthAscend, TargetY: dive.TargetY, Reason: "feet below the dive target"}
	default:
		return DepthPlan{Action: DepthHold, TargetY: dive.TargetY, Reason: "holding at the dive target"}
	}
}

// breathReason names the exact state the reflex fired on. "Surfacing" on its
// own is not a log line; "air spent" and "air reserve" are two different
// problems and the field only tells you which one when the line says so.
func breathReason(breath BreathState) string {
	if breath.MustSurface() {
		return "air spent: no seconds left"
	}
	return fmt.Sprintf("air reserve: %ds of %ds left, climbing", breath.SecondsLeft(), breath.Budget)
}
