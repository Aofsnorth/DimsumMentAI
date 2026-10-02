package agi_test

import (
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
)

// The deadline is the whole promise. "24 minutes" was said once, by a person, at
// a moment that has already passed by the time anyone looks at this record — and
// the bot must be over at 24 minutes whatever the objective says. A deadline
// that can be re-read, extended, or renewed is not a deadline; it is a
// suggestion the bot is free to ignore, and the recording ends whenever the bot
// feels finished instead of when the operator said it would.

// TestParseEpisodeReadsTheBriefItWasGiven pins the shape. The objective is a
// sentence, and truncating it to fit a field would be quietly editing what the
// operator asked for.
func TestParseEpisodeReadsTheBriefItWasGiven(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name          string
		line          string
		wantNumber    int
		wantObjective string
		wantBudget    time.Duration
	}{
		{"the canonical form", "/episode 1 24m build a house", 1, "build a house", 24 * time.Minute},
		{"no slash", "episode 2 10m gather resources", 2, "gather resources", 10 * time.Minute},
		{"short alias", "/ep 3 5m explore", 3, "explore", 5 * time.Minute},
		{"unit in front", "/episode 4 m30 go mining", 4, "go mining", 30 * time.Minute},
		{"hours", "/episode 5 1h30m do a lot of things", 5, "do a lot of things", 90 * time.Minute},
		{"compound duration split by a space", "/episode 6 1h 30m build and dig", 6, "build and dig", 90 * time.Minute},
		{"a bare number means minutes", "/episode 7 24 build a house", 7, "build a house", 24 * time.Minute},
		{"seconds", "/episode 8 90s look around", 8, "look around", 90 * time.Second},
		{"objective keeps its punctuation", "/episode 9 20m find the nether portal, then use it", 9, "find the nether portal, then use it", 20 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ep, ok := agi.ParseEpisode(tc.line, now)
			if !ok {
				t.Fatalf("could not read a brief from %q", tc.line)
			}
			if ep.Number != tc.wantNumber {
				t.Errorf("number = %d, want %d", ep.Number, tc.wantNumber)
			}
			if ep.Objective != tc.wantObjective {
				t.Errorf("objective = %q, want %q", ep.Objective, tc.wantObjective)
			}
			if got := ep.EndsAt.Sub(ep.StartedAt); got != tc.wantBudget {
				t.Errorf("budget = %s, want %s", got, tc.wantBudget)
			}
			if !ep.StartedAt.Equal(now) {
				t.Errorf("StartedAt = %v, want the moment the brief was given", ep.StartedAt)
			}
		})
	}
}

// TestRefusesWhatIsNotABrief keeps the parser from inventing an episode out of
// ordinary chat. A player saying "let's do an episode tomorrow" must not
// accidentally start a 24-minute recording with no duration at all.
func TestRefusesWhatIsNotABrief(t *testing.T) {
	t.Parallel()

	now := time.Now()

	cases := []string{
		"",
		"episode",
		"/episode",
		"/episode 1",            // no duration
		"/episode 1 24m",        // no objective
		"/episode 1 24m ",       // whitespace is not an objective
		"/episodes 1 24m build", // not the command
		"/episode 1 -5m build",  // a negative budget is not a budget
		"/episode 1 0m build",   // neither is a zero one
		"halo, arcane 1 24m x",  // unrelated chat
	}

	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			if ep, ok := agi.ParseEpisode(line, now); ok {
				t.Errorf("invented an episode from %q: %+v", line, ep)
			}
		})
	}
}

// TestTheDeadlineIsHard is the central claim. Past the line, the episode is
// over, and nothing in the objective can reopen it.
func TestTheDeadlineIsHard(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ep, ok := agi.ParseEpisode("/episode 1 24m build a house", start)
	if !ok {
		t.Fatal("could not read the brief")
	}

	if ep.Over(start) {
		t.Error("a brand new episode is already over")
	}
	// "make sure it does not run past 24 minutes" means at 24:00 the recording
	// is done. The deadline instant belongs to nobody: the episode is over on
	// arrival at it, not one nanosecond later.
	if !ep.Over(ep.EndsAt) {
		t.Error("at the deadline the episode is somehow not over yet")
	}
	if ep.Over(ep.EndsAt.Add(-time.Nanosecond)) {
		t.Error("one nanosecond before the deadline and the episode is already over")
	}

	// At 23:59 there is a second left; at 24:00:01 there is nothing, and the
	// number must never be negative because every caller adds it to something.
	last := ep.EndsAt.Add(-time.Second)
	if got := ep.Remaining(last); got != time.Second {
		t.Errorf("remaining = %s one second before the end, want 1s", got)
	}
	if got := ep.Remaining(ep.EndsAt.Add(time.Hour)); got != 0 {
		t.Errorf("remaining = %s well past the end, want 0; a negative budget is a bug waiting to happen", got)
	}
}

// TestTheDeadlineDoesNotMove pins it against the most tempting bug: reading
// "24 minutes" off the config every tick, so the budget slides forward forever
// and the recording never ends.
func TestTheDeadlineDoesNotMove(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ep, _ := agi.ParseEpisode("/episode 1 24m build a house", start)
	wantEnd := start.Add(24 * time.Minute)

	for _, offset := range []time.Duration{0, time.Minute, 10 * time.Minute, 23 * time.Minute} {
		if !ep.EndsAt.Equal(wantEnd) {
			t.Fatalf("EndsAt moved to %v, want it pinned at %v", ep.EndsAt, wantEnd)
		}
		_ = offset
	}

	// Forty minutes later the budget is spent, not forty minutes fresh.
	late := start.Add(40 * time.Minute)
	if !ep.Over(late) {
		t.Error("a deadline re-read as 'now plus 24m' would never be over; this one is")
	}
}

// TestUrgencyIgnoresTheFirstHalf. A bot that starts rushing at 50% panics over
// work it had ample time for, and the panic itself wastes what was left.
func TestUrgencyIgnoresTheFirstHalf(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ep, _ := agi.ParseEpisode("/episode 1 24m build a house", start)

	for _, minute := range []int{0, 1, 5, 9} {
		if got := ep.Urgency(start.Add(time.Duration(minute) * time.Minute)); got != 0 {
			t.Errorf("urgency at %d minutes = %.2f, want 0; the first half is not the crisis", minute, got)
		}
	}

	// The band that matters is the last third, and it climbs to 1.
	mid := ep.Urgency(start.Add(18 * time.Minute))
	end := ep.Urgency(start.Add(24 * time.Minute))
	if mid <= 0 {
		t.Errorf("urgency at 18 minutes = %.2f, want it to have started climbing", mid)
	}
	if end != 1 {
		t.Errorf("urgency at the deadline = %.2f, want 1", end)
	}
	if mid >= end {
		t.Error("urgency does not climb towards the deadline")
	}
}

// TestWrappingUpIsTheShootMoment. A bot four minutes from the end, halfway
// through a house, has one honest move: stop, say what it managed, and be
// somewhere safe. Half a house plus an overrun is worse than something small and
// finished, because the overrun is what a viewer sees.
func TestWrappingUpIsTheShootMoment(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ep, _ := agi.ParseEpisode("/episode 1 24m build a house", start)

	if ep.WrappingUp(start.Add(10 * time.Minute)) {
		t.Error("wrapping up at 10 minutes of 24; that is panicking, not finishing")
	}
	if !ep.WrappingUp(start.Add(21 * time.Minute)) {
		t.Error("three minutes from the end and it is still starting new things")
	}
	if !ep.WrappingUp(start.Add(30 * time.Minute)) {
		t.Error("long past the end and still mid-episode")
	}
}

// TestNoEpisodeIsAStateNotAFault. The bot is expected to run for hours before
// anybody hands it a brief, and it should be playing the whole time.
func TestNoEpisodeIsAStateNotAFault(t *testing.T) {
	t.Parallel()

	now := time.Now()
	var none agi.Episode

	if none.Running(now) {
		t.Error("the empty episode is running")
	}
	if none.Over(now) {
		t.Error("the empty episode is over; there is nothing to be over")
	}
	if none.Urgency(now) != 0 {
		t.Error("the empty episode is urgent")
	}
	if none.WrappingUp(now) {
		t.Error("the empty episode is wrapping up")
	}
	if got := none.FractionSpent(now); got != 1 {
		t.Errorf("FractionSpent = %.2f for no episode, want 1 so callers cannot read it as fresh time", got)
	}
	if !strings.Contains(none.Describe(now), "no episode") {
		t.Errorf("Describe = %q, want it to say so plainly", none.Describe(now))
	}
}

// TestDescribeCarriesTheTimeLeft is why Describe exists at all. Every caller
// that makes a decision with this in front of it — the planner, the tick loop,
// the LLM — needs the budget, and a string without it means each of them
// re-derives it and possibly rounds differently.
func TestDescribeCarriesTheTimeLeft(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ep, _ := agi.ParseEpisode("/episode 1 24m build a house", start)

	fresh := ep.Describe(start)
	if !strings.Contains(fresh, "build a house") {
		t.Errorf("Describe dropped the objective: %q", fresh)
	}
	if !strings.Contains(fresh, "24") {
		t.Errorf("Describe dropped the budget: %q", fresh)
	}

	late := ep.Describe(start.Add(23 * time.Minute))
	if !strings.Contains(late, "finish up") {
		t.Errorf("Describe at 23/24 does not tell the bot to wrap up: %q", late)
	}

	done := ep.Describe(ep.EndsAt.Add(time.Minute))
	if !strings.Contains(done, "is over") {
		t.Errorf("Describe past the end does not say it is over: %q", done)
	}
	// "is over" must not also claim the episode is wrapping up; the bot should
	// have one clear instruction, not two that disagree.
	if strings.Contains(done, "finish up") {
		t.Errorf("Describe is giving two contradictory instructions: %q", done)
	}
}
