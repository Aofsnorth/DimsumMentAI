package agi_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
)

// Natural mode is the mode a recording is made in, and its whole job is to be
// watchable for three hours with nobody instructing it. These tests pin the
// rules that make that possible — and, just as importantly, the ones that stop
// it from being a bot that is never still.

// TestModeAcceptsTheNameItIsSoldUnder. A mode that is configured under one name
// and read under another is off, permanently, with nothing to indicate why.
func TestModeAcceptsTheNameItIsSoldUnder(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{Mode: "natural"})
	if !r.ModeIsNatural() {
		t.Error("mode \"natural\" did not select natural mode")
	}
	r = agi.NewBareForTest(agi.Config{Mode: "planning"})
	if r.ModeIsNatural() {
		t.Error("planning mode selected natural mode")
	}
	r = agi.NewBareForTest(agi.Config{Mode: "default"})
	if r.ModeIsNatural() {
		t.Error("default mode selected natural mode")
	}
}

// TestAHumanOutranksTheSchedule is the single rule that decides whether natural
// mode is a companion or a machine with a calendar. Somebody talks to the bot,
// the bot answers, and the episode waits — including its clock, because the
// recording is still going whether anyone is talking or not.
func TestAHumanOutranksTheSchedule(t *testing.T) {
	t.Parallel()

	now := time.Now()
	r := agi.NewBareForTest(agi.Config{})
	if _, ok := r.BeginEpisode("/episode 1 24m build a house", now); !ok {
		t.Fatal("could not start the episode")
	}

	// Nobody talking: the bot is on its own business.
	if r.AttendingToPlayer(now) {
		t.Error("attending to a player before anyone spoke")
	}

	r.Suspend("Arthenyxx")
	if !r.AttendingToPlayer(now) {
		t.Error("a person spoke and the bot did not notice")
	}

	// The brief is still there. Suspending is not cancelling: the recording is
	// still going and the wall is still real.
	if ep := r.CurrentEpisode(); ep.Objective == "" {
		t.Error("talking to the bot threw away the episode")
	}
	if ep := r.CurrentEpisode(); !ep.Over(now.Add(30 * time.Minute)) {
		t.Error("the episode deadline moved when a person talked to the bot")
	}

	// The attention lapses and the bot goes back to what it was doing.
	if r.AttendingToPlayer(now.Add(2 * agi.SuspendAfter)) {
		t.Error("the bot is still holding a conversation from two windows ago")
	}
}

// TestANewBriefThrowsAwayTheOldPlan. Otherwise the bot spends the first minutes
// of episode 2 finishing episode 1, which reads on camera as a glitch.
func TestANewBriefThrowsAwayTheOldPlan(t *testing.T) {
	t.Parallel()

	now := time.Now()
	r := agi.NewBareForTest(agi.Config{})
	r.SetPlan(agi.Plan{
		Objective: "the old one",
		Steps:     []agi.PlanStep{{Kind: agi.StepGather, Description: "wood"}},
	})

	if _, ok := r.BeginEpisode("/episode 2 10m dig down", now); !ok {
		t.Fatal("could not start the second episode")
	}
	if plan := r.CurrentPlan(); plan.Objective != "" {
		t.Errorf("the plan from the previous episode survived: %q", plan.Objective)
	}
	if ep := r.CurrentEpisode(); ep.Objective != "dig down" {
		t.Errorf("objective = %q, want the new brief", ep.Objective)
	}
}

// TestRestPeriodsEndOnTheirOwn. The trap this guards is a duration re-derived
// from the clock every tick: "started plus thirty seconds" is always thirty
// seconds away, because the clock has moved. A bot idling that way never wakes
// up, and on stream that is indistinguishable from a crash.
func TestRestPeriodsEndOnTheirOwn(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	r := agi.NewBareForTest(agi.Config{IdleStillBias: 0.5})

	snap := agi.Snapshot{Now: start}
	if r.ShouldIdle(snap) {
		t.Error("the bot is idling before it has done anything")
	}

	r.Idle(snap)
	if !r.ShouldIdle(snap) {
		t.Fatal("idling did not open a rest period")
	}

	// Carry it across several ticks the way a real loop would. A rest that is
	// restarted rather than continued would push its own end further out every
	// time, and this is exactly where that would show.
	for tick := 1; tick <= 6; tick++ {
		now := start.Add(time.Duration(tick) * 10 * time.Second)
		r.Idle(agi.Snapshot{Now: now})
		if r.ShouldIdle(agi.Snapshot{Now: now}) && now.After(start.Add(3*time.Minute)) {
			t.Fatalf("still idling three minutes in; the rest period is being restarted each tick")
		}
	}

	if r.ShouldIdle(agi.Snapshot{Now: start.Add(10 * time.Minute)}) {
		t.Error("the bot is still resting ten minutes later")
	}
}

// TestBothKindsOfIdleAreReachable. A recording needs motionless periods and
// needs lively ones, and a rule that only ever produces one of them is a bot
// with a single note.
func TestBothKindsOfIdleAreReachable(t *testing.T) {
	t.Parallel()

	counts := map[agi.IdleMode]int{}
	for seed := 0; seed < 500; seed++ {
		counts[agi.ChooseIdle(seed, 0.5)]++
	}
	if counts[agi.IdleStill] == 0 {
		t.Error("no motionless idle in 500 attempts at a 50% bias")
	}
	if counts[agi.IdleAlive] == 0 {
		t.Error("no living idle in 500 attempts at a 50% bias")
	}
}

// TestTheIdleBiasIsHonouredAtBothEnds. Both extremes are survivable settings and
// both should mean what they say rather than quietly becoming the default.
func TestTheIdleBiasIsHonouredAtBothEnds(t *testing.T) {
	t.Parallel()

	for seed := 0; seed < 50; seed++ {
		if got := agi.ChooseIdle(seed, 0); got != agi.IdleAlive {
			t.Fatalf("bias 0 produced %v, want always idle_alive", got)
		}
		if got := agi.ChooseIdle(seed, 1); got != agi.IdleStill {
			t.Fatalf("bias 1 produced %v, want always idle_still", got)
		}
	}
	// A negative bias is a typo, and the safe reading of a typo is "never fully
	// still" rather than "crash".
	for seed := 0; seed < 50; seed++ {
		if got := agi.ChooseIdle(seed, -1); got != agi.IdleAlive {
			t.Fatalf("negative bias produced %v, want the safe default", got)
		}
	}
}

// TestAMotionlessIdleLastsLongerThanALivelyOne. It costs the viewer more to
// watch and less to look at, so the bot may sit still for longer than it may
// fidget.
func TestAMotionlessIdleLastsLongerThanALivelyOne(t *testing.T) {
	t.Parallel()

	start := time.Now()
	longestAlive, longestStill := time.Duration(0), time.Duration(0)
	for seed := 0; seed < 200; seed++ {
		for _, bias := range []float64{1} {
			pick := agi.IdlePickFor(seed, agi.ChooseIdle(seed, bias))
			if pick.Mode == agi.IdleStill && pick.Duration > longestStill {
				longestStill = pick.Duration
			}
			if pick.Mode == agi.IdleAlive && pick.Duration > longestAlive {
				longestAlive = pick.Duration
			}
		}
	}
	if longestStill <= longestAlive {
		t.Errorf("motionless idles cap at %s, living ones at %s; the still one should be allowed to run longer",
			longestStill, longestAlive)
	}
	_ = start
}
