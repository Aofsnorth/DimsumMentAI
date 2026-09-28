// The escalation: Jev decides, the big model is called with everything.
//
// Today the two models never speak to each other. The fast one answers numbers
// about the present; the slow one is told what to say and nothing about why.
// That is a gap, not a design: a bot asked "what do you want to do" while
// holding a plan it knows nothing about will answer as if it had no plan, and
// the answer will contradict what the body is doing in front of the viewer.
//
// So this file makes the fast model the one that opens the door, and makes sure
// that when it does the slow model is handed the whole picture.
//
// Two things have to be true for this to work, and both are easy to get wrong:
//
//   - Jev decides. The big model is never called first and never on a schedule.
//     A model consulted every thirty seconds is a model consulted for trivia,
//     and a bot that asks a large model what to do every tick is a bot that
//     spends its whole budget re-deriving what it already knows.
//
//   - When the big model IS called, it gets everything. The goal, the plan, the
//     mobs, the blocks, the inventory, the clock, and how much recording time is
//     left. A model handed a partial picture does not know it is missing one,
//     and will confidently fill the gap.

package agi

import (
	"strings"
)

// EscalateQuestion is the Jev question that decides whether the big model is
// worth calling.
//
// It is a noul, not a choice, because the answer is a probability that the
// program thresholds. Jev saying 0.9 does not mean "call it"; it means "90
// likely this is worth the seconds", and where to cut that is a decision that
// belongs in config rather than in a prompt.
const EscalateQuestion = "jev.needs_the_big_brain"

// EscalateInstructions is the wording, and it is deliberately specific about
// when NOT to escalate. A question that invites escalation on every tick gets
// escalation on every tick, and the cost is a multi-second round trip for
// something the fast model could have decided.
const EscalateInstructions = "Is this a moment where the bot needs to think something through " +
	"properly — working out what to do next, planning several steps ahead, or saying " +
	"something worth hearing? Answer no if nothing is happening, if the bot is " +
	"just idling or walking, and if the only reason to answer would be that it " +
	"has answered before. Repeating yourself is not a reason to think harder."

// escalateThreshold is where the probability is read as "call the big model".
//
// It sits high on purpose. The big model is the expensive tier and it is not
// needed to decide between wandering and looking at a tree; it is needed when
// the bot has to work something out. A low threshold here turns a three-hour
// recording into several hundred model calls, which is both slow and, on a
// long stream, audibly different — a bot that pauses to think every few seconds
// does not read as thoughtful, it reads as laggy.
const escalateThreshold = 0.75

// WantsBigBrain reports whether the judgement says to call the slow model.
func WantsBigBrain(j Judgement, threshold float64) bool {
	return j.Known && j.Escalate >= threshold
}

// escalationContext is everything the big model is told, rendered for a prompt.
type escalationContext struct {
	Episode string
	Goal    string
	Plan    string
	State   string
	Vocab   string
	Urgent  bool
}

// Render assembles the full context for the big model.
//
// Everything goes in, and the reason is the one from the top of this file: a
// model handed a partial picture does not know it is missing one. The cost of a
// longer prompt is a few hundred tokens, which the budget absorbs many times
// over, and the cost of omitting the plan is a bot that contradicts itself on
// camera.
func buildEscalationContext(snap Snapshot, plan Plan) escalationContext {
	ctx := escalationContext{
		State:  describeState(snap),
		Goal:   snap.GoalSummary,
		Urgent: snap.HP > 0 && snap.Hunger > 0,
	}
	if snap.Vocabulary != nil {
		ctx.Vocab = snap.Vocabulary.Describe()
	}
	if plan.Objective != "" {
		ctx.Plan = renderPlan(plan)
	}
	if ep := snap.EpisodeText; ep != "" {
		ctx.Episode = ep
	}
	return ctx
}

// Prompt renders the context as the message the big model is given.
//
// It is deliberately not a task list. The big model is being consulted because
// something needs thinking about, and handing it a menu of things to do is how
// you get a menu back instead of a thought. What it gets is a situation and a
// question.
func (c escalationContext) Prompt() string {
	var sb strings.Builder

	sb.WriteString("The fast model decided this is a moment to actually think.\n\n")

	if c.Episode != "" {
		sb.WriteString("WHAT THIS RECORDING IS FOR:\n")
		sb.WriteString(c.Episode)
		sb.WriteString("\n\n")
	}
	if c.Plan != "" {
		sb.WriteString("WHAT THE BOT IS IN THE MIDDLE OF:\n")
		sb.WriteString(c.Plan)
		sb.WriteString("\n\n")
	}

	sb.WriteString("SITUATION:\n")
	sb.WriteString(c.State)

	if c.Vocab != "" {
		sb.WriteString("\nWHAT THIS WORLD CONTAINS:\n")
		sb.WriteString(c.Vocab)
		sb.WriteString("\n")
	}

	sb.WriteString("\nThink it through, then say what you would do. ")
	sb.WriteString("If there is nothing worth doing, say so — doing nothing is a real answer.\n")
	return sb.String()
}
