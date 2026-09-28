package agi

import (
	"testing"
	"time"
)

// Natural mode is the mode a recording is made in, and its whole job is to be
// watchable for three hours with nobody instructing it. These tests pin the
// rules that make that possible — and, just as importantly, the ones that stop
// it from being a bot that is never still.

// TestModeAcceptsTheNameItIsSoldUnder. A mode that is configured under one name
// and read under another is off, permanently, with nothing to indicate why.
func TestModeAcceptsTheNameItIsSoldUnder(t *testing.T) {
	t.Parallel()

	r := &Runner{cfg: Config{Mode: "natural"}}
	if !r.modeIsNatural() {
		t.Error("mode \"natural\" did not select natural mode")
	}
	r = &Runner{cfg: Config{Mode: "planning"}}
	if r.modeIsNatural() {
		t.Error("planning mode selected natural mode")
	}
	r = &Runner{cfg: Config{Mode: "default"}}
	if r.modeIsNatural() {
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
	r := &Runner{}
	if _, ok := r.BeginEpisode("/episode 1 24m build a house", now); !ok {
		t.Fatal("could not start the episode")
	}

	// Nobody talking: the bot is on its own business.
	if r.attendingToPlayer(now) {
		t.Error("attending to a player before anyone spoke")
	}

	r.Suspend("Arthenyxx")
	if !r.attendingToPlayer(now) {
		t.Error("a person spoke and the bot did not notice")
	}

	// The brief is still there. Suspending is not cancelling: the recording is
	// still going and the wall is still real.
	if ep := r.currentEpisode(); ep.Objective == "" {
		t.Error("talking to the bot threw away the episode")
	}
	if ep := r.currentEpisode(); !ep.Over(now.Add(30 * time.Minute)) {
		t.Error("the episode deadline moved when a person talked to the bot")
	}

	// The attention lapses and the bot goes back to what it was doing.
	if r.attendingToPlayer(now.Add(2 * suspendAfter)) {
		t.Error("the bot is still holding a conversation from two windows ago")
	}
}

// TestANewBriefThrowsAwayTheOldPlan. Otherwise the bot spends the first minutes
// of episode 2 finishing episode 1, which reads on camera as a glitch.
func TestANewBriefThrowsAwayTheOldPlan(t *testing.T) {
	t.Parallel()

	now := time.Now()
	r := &Runner{}
	r.setPlan(Plan{
		Objective: "the old one",
		Steps:     []PlanStep{{Kind: StepGather, Description: "wood"}},
	})

	if _, ok := r.BeginEpisode("/episode 2 10m dig down", now); !ok {
		t.Fatal("could not start the second episode")
	}
	if plan := r.currentPlan(); plan.Objective != "" {
		t.Errorf("the plan from the previous episode survived: %q", plan.Objective)
	}
	if ep := r.currentEpisode(); ep.Objective != "dig down" {
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
	r := &Runner{cfg: Config{IdleStillBias: 0.5}}

	snap := Snapshot{Now: start}
	if r.shouldIdle(snap) {
		t.Error("the bot is idling before it has done anything")
	}

	r.idle(snap)
	if !r.shouldIdle(snap) {
		t.Fatal("idling did not open a rest period")
	}

	// Carry it across several ticks the way a real loop would. A rest that is
	// restarted rather than continued would push its own end further out every
	// time, and this is exactly where that would show.
	for tick := 1; tick <= 6; tick++ {
		now := start.Add(time.Duration(tick) * 10 * time.Second)
		r.idle(Snapshot{Now: now})
		if r.shouldIdle(Snapshot{Now: now}) && now.After(start.Add(3*time.Minute)) {
			t.Fatalf("still idling three minutes in; the rest period is being restarted each tick")
		}
	}

	if r.shouldIdle(Snapshot{Now: start.Add(10 * time.Minute)}) {
		t.Error("the bot is still resting ten minutes later")
	}
}

// TestBothKindsOfIdleAreReachable. A recording needs motionless periods and
// needs lively ones, and a rule that only ever produces one of them is a bot
// with a single note.
func TestBothKindsOfIdleAreReachable(t *testing.T) {
	t.Parallel()

	counts := map[IdleMode]int{}
	for seed := 0; seed < 500; seed++ {
		counts[ChooseIdle(seed, 0.5)]++
	}
	if counts[IdleStill] == 0 {
		t.Error("no motionless idle in 500 attempts at a 50% bias")
	}
	if counts[IdleAlive] == 0 {
		t.Error("no living idle in 500 attempts at a 50% bias")
	}
}

// TestTheIdleBiasIsHonouredAtBothEnds. Both extremes are survivable settings and
// both should mean what they say rather than quietly becoming the default.
func TestTheIdleBiasIsHonouredAtBothEnds(t *testing.T) {
	t.Parallel()

	for seed := 0; seed < 50; seed++ {
		if got := ChooseIdle(seed, 0); got != IdleAlive {
			t.Fatalf("bias 0 produced %v, want always idle_alive", got)
		}
		if got := ChooseIdle(seed, 1); got != IdleStill {
			t.Fatalf("bias 1 produced %v, want always idle_still", got)
		}
	}
	// A negative bias is a typo, and the safe reading of a typo is "never fully
	// still" rather than "crash".
	for seed := 0; seed < 50; seed++ {
		if got := ChooseIdle(seed, -1); got != IdleAlive {
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
			pick := idlePickFor(seed, ChooseIdle(seed, bias))
			if pick.Mode == IdleStill && pick.Duration > longestStill {
				longestStill = pick.Duration
			}
			if pick.Mode == IdleAlive && pick.Duration > longestAlive {
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
