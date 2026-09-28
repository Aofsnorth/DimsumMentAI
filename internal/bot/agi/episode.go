// An episode: a recorded session with a hard wall-clock deadline.
//
// The brief is the whole point. Somebody drops a bot into a world, types
// "/episode 1 24m build a house", and walks away. Two things follow from that,
// and both are the reason this is a type rather than a config field:
//
//   - The deadline is absolute. It was set once, by a person, at a moment in
//     time that has already passed by the time anyone looks at it later. It
//     cannot be extended, re-read, or renewed: at 24 minutes the episode is
//     over, whatever the objective says. A bot that decides it needs "a little
//     longer" is a bot that will decide that every time, and the recording ends
//     whenever the bot feels finished rather than when the person who is
//     recording said it would.
//
//   - The objective is the bot's to interpret, not the operator's to enforce.
//     "build a house" is a goal, not a task list. What makes that survivable is
//     that the bot can always ask how much time is left, and both the planner
//     and the tick loop ask, so running out of time is something the bot
//     notices rather than something that happens to it.
//
// Everything here is a value type and a pure function, because the alternative
// is a bot that silently overruns a recording and nobody notices for hours.

package agi

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Episode is one recorded session.
type Episode struct {
	// Number is what the operator called it. It goes in the evidence log and
	// in what the bot says, so an episode 7 is distinguishable from an episode
	// 70 in a folder of recordings.
	Number int
	// Title is the operator's name for it, if they gave one. Empty is fine.
	Title string
	// Objective is what the bot was told to do, in the operator's words.
	//
	// It is never rewritten. A bot that restates its objective in its own words
	// drifts: by minute twenty it is chasing a slightly different goal than the
	// one that was agreed, and nobody watching can tell.
	Objective string
	// StartedAt and EndsAt are absolute. Both are already in the past by the
	// time a late reader looks at them, which is the point: a deadline that can
	// be re-read as "now plus 24 minutes" is not a deadline.
	StartedAt time.Time
	EndsAt    time.Time
}

// Remaining is how much time is left, never negative.
//
// A negative duration is a bug waiting to happen: every caller that adds it to
// something, or compares against it, gets a different answer than the operator
// agreed to. Zero means over.
func (e Episode) Remaining(now time.Time) time.Duration {
	left := e.EndsAt.Sub(now)
	if left < 0 {
		return 0
	}
	return left
}

// Over reports whether the episode has run out of time.
//
// At the deadline instant itself it is over: "make sure it does not run past
// 24 minutes" means at 24:00 the recording is done, not that 24:00 is the last
// frame still inside the budget.
//
// An episode with no objective is never over. There is nothing to be over, and
// a caller that sees Over() true for "no episode" would conclude a recording
// ended when none ever started.
func (e Episode) Over(now time.Time) bool {
	if e.Objective == "" {
		return false
	}
	return !now.Before(e.EndsAt)
}

// Elapsed is how much of the budget has been spent, clamped to the budget.
func (e Episode) Elapsed(now time.Time) time.Duration {
	total := e.EndsAt.Sub(e.StartedAt)
	if total <= 0 {
		return 0
	}
	spent := now.Sub(e.StartedAt)
	if spent < 0 {
		return 0
	}
	if spent > total {
		return total
	}
	return spent
}

// FractionSpent is 0 at the start and 1 at the deadline.
//
// It exists so the planner can be told "you are two thirds through" without
// anyone having to do arithmetic on a duration, and — more importantly — so the
// bot can be told the difference between "plenty of time" and "not much" before
// it commits to something long.
func (e Episode) FractionSpent(now time.Time) float64 {
	total := e.EndsAt.Sub(e.StartedAt)
	if total <= 0 {
		return 1
	}
	return float64(e.Elapsed(now)) / float64(total)
}

// Running reports whether the episode is live at this moment.
//
// A zero Episode is the "no episode" state and is never running. That is a
// supported state, not an error: the bot is expected to run for hours before
// anybody hands it a brief, and it should be playing the whole time.
func (e Episode) Running(now time.Time) bool {
	return e.Objective != "" && now.Before(e.EndsAt)
}

// Urgency is how badly the bot needs to start wrapping up, from 0 to 1.
//
// The thresholds are not evenly spread. At the halfway point there is nothing
// to say — a bot that starts rushing at 50% panics over work it had ample time
// for, and the panic itself wastes the time. The band that matters is the last
// third, and inside it the number climbs steeply, because the last third is
// where an unfinished objective is actually lost.
func (e Episode) Urgency(now time.Time) float64 {
	if e.Objective == "" {
		return 0
	}
	spent := e.FractionSpent(now)
	switch {
	case spent < 0.4:
		return 0
	case spent >= 1:
		return 1
	default:
		// 0.4 -> 0, 1.0 -> 1, so the bot starts noticing a quarter of the way
		// through the final stretch.
		return (spent - 0.4) / 0.6
	}
}

// WrappingUp reports whether the objective should be abandoned in favour of
// finishing tidily.
//
// This is the "shoot" moment. A bot with four minutes left on a twenty-four
// minute episode that is halfway through building a house has one honest move:
// stop, say what it managed, and be somewhere safe. Half a house plus an
// overrun is worse than a small finished thing plus a clean ending, because the
// overrun is the part a viewer sees.
func (e Episode) WrappingUp(now time.Time) bool {
	return e.Objective != "" && e.Urgency(now) >= 0.75
}

// ParseEpisode reads a brief from a chat line.
//
// The shape is "/episode <number> <duration> <objective...>", with the duration
// in Go's own notation so "24m", "90s" and "1h30m" all work and nobody has to
// learn a second one. The objective is everything after the duration, kept
// verbatim including its spaces, because it is a sentence and truncating it to
// fit a field would be quietly editing what the operator asked for.
//
// A leading slash is optional. A player who types "episode 1 24m ..." into chat
// plainly means the same thing, and making them remember a slash is the kind of
// friction that gets a feature described and then never used.
func ParseEpisode(line string, now time.Time) (Episode, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return Episode{}, false
	}

	head := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if head != "episode" && head != "ep" {
		return Episode{}, false
	}
	fields = fields[1:]
	if len(fields) < 2 {
		return Episode{}, false
	}

	number := 0
	if n, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(fields[0]), "episode")); err == nil {
		number = n
		fields = fields[1:]
	}
	if len(fields) < 2 {
		return Episode{}, false
	}

	// The duration may carry its unit in front of it ("24m" or "m24"), because
	// people type both and neither is worth rejecting a recording over.
	dur, used := splitDuration(fields)
	if dur <= 0 || used >= len(fields) {
		return Episode{}, false
	}
	objective := strings.Join(fields[used:], " ")
	if strings.TrimSpace(objective) == "" {
		return Episode{}, false
	}

	return Episode{
		Number:    number,
		Objective: objective,
		StartedAt: now,
		EndsAt:    now.Add(dur),
	}, true
}

// splitDuration pulls a leading Go duration out of a token list.
//
// It returns the duration and how many tokens it consumed, so the caller can
// tell where the objective begins. A bare number is treated as minutes, because
// "/episode 1 24 build a house" is overwhelmingly what someone means, and
// rejecting it would make them go read the help text first.
func splitDuration(fields []string) (time.Duration, int) {
	first := fields[0]

	// "m24" — the unit in front. People type both orders.
	if digits, ok := strings.CutPrefix(first, "m"); ok && digits != "" {
		if _, err := strconv.Atoi(digits); err == nil {
			return minutesFor(digits), 1
		}
	}

	if d, err := time.ParseDuration(first); err == nil && d > 0 {
		// "1h 30m" is two tokens, and the first one parses perfectly well on its
		// own — so without this the "30m" would be left in the objective and the
		// brief would read "30m build and dig". Only a second token that is
		// itself a pure duration is absorbed, because a sentence starting "30
		// minutes of copper ore" must not have half of it eaten.
		if len(fields) > 1 {
			if next, err := time.ParseDuration(fields[1]); err == nil && next > 0 {
				return d + next, 2
			}
		}
		return d, 1
	}

	if _, err := strconv.Atoi(first); err == nil {
		return minutesFor(first), 1
	}
	return 0, 0
}

func minutesFor(s string) time.Duration {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Minute
}

// Describe renders the episode for a prompt or a log line.
//
// The time left is in it on purpose. Every caller that has to make a decision
// with this in front of it — the planner, the tick loop, the LLM — needs the
// budget, and a string without it would mean each of them re-deriving it and
// possibly rounding differently.
func (e Episode) Describe(now time.Time) string {
	if e.Objective == "" {
		return "no episode running"
	}
	head := "episode"
	if e.Number > 0 {
		head = fmt.Sprintf("episode %d", e.Number)
	}
	if e.Title != "" {
		head += " (" + e.Title + ")"
	}

	switch {
	case e.Over(now):
		return fmt.Sprintf("%s is over — %s was the objective", head, e.Objective)
	case e.WrappingUp(now):
		return fmt.Sprintf("%s: %s. Only %s left — finish up, do not start anything new.",
			head, e.Objective, humanDuration(e.Remaining(now)))
	default:
		return fmt.Sprintf("%s: %s. %s of recording time left.",
			head, e.Objective, humanDuration(e.Remaining(now)))
	}
}

// humanDuration renders a duration the way a person would say it, because
// "13m41.2s" in a prompt reads like machine output and "13 minutes" reads like
// something a player said.
func humanDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "no time"
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < time.Hour:
		mins := int(d.Minutes())
		secs := int(d.Seconds()) - mins*60
		if secs < 10 {
			return fmt.Sprintf("%d minutes", mins)
		}
		return fmt.Sprintf("%d min %d sec", mins, secs)
	default:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	}
}
