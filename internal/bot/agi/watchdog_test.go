package agi

import (
	"strings"
	"testing"
	"time"
)

// A three-hour stream is not a session anybody is present for. Somewhere in
// three hours the bot dies, wedges itself against a wall, or loses the
// connection, and the difference between a recording and a crash is entirely
// whether anything noticed.

// TestDeathOutranksEverything. A dead bot is also a motionless one, so a
// watchdog that checks for stillness first spends its time unsticking a corpse.
func TestDeathOutranksEverything(t *testing.T) {
	t.Parallel()

	now := time.Now()
	dead := Health{HP: 0, Now: now, LastMoved: now.Add(-time.Hour), Disconnected: true}
	if got := Diagnose(dead); got != FaultDisconnected {
		t.Errorf("fault = %v for a dead disconnected bot; reconnection is what actually has to happen first", got)
	}

	dead.Disconnected = false
	if got := Diagnose(dead); got != FaultDead {
		t.Errorf("fault = %v, want FaultDead", got)
	}
	if _, action := Decide(dead, 1); action != ActRespawn {
		t.Errorf("action = %v, want ActRespawn on the first sighting; waiting to confirm a dead bot means lying on the respawn screen", action)
	}
}

// TestABlipMustNotEndTheRecording. A bot that panics at the first sign of a
// disconnect spends a three-hour stream reconnecting to a problem it did not
// have.
func TestABlipMustNotEndTheRecording(t *testing.T) {
	t.Parallel()

	now := time.Now()
	blip := Health{HP: 20, Now: now, Disconnected: true}
	if _, action := Decide(blip, 1); action != ActReconnect {
		t.Errorf("action = %v, want ActReconnect immediately; a dropped connection is not a reason to give up", action)
	}
}

// TestStuckNeedsTwoSightings. One reading is a server hiccup, not a fault.
func TestStuckNeedsTwoSightings(t *testing.T) {
	t.Parallel()

	now := time.Now()
	wedged := Health{HP: 20, Now: now, LastMoved: now.Add(-2 * time.Minute), PositionChanged: false}

	fault, action := Decide(wedged, 1)
	if fault != FaultStuck {
		t.Errorf("fault = %v, want FaultStuck; the bot has not moved for two minutes", fault)
	}
	if action != ActNone {
		t.Errorf("action = %v on the first sighting, want nothing; one tick can be anything", action)
	}

	if _, action := Decide(wedged, stuckNeedsRepeats); action != ActUnstick {
		t.Errorf("action = %v on the second sighting, want ActUnstick", action)
	}
}

// TestDeliberatePausingIsNotStuck. A bot idling, chatting, or walking somewhere
// on purpose is supposed to leave its position alone, and calling that stuck
// produces a bot that interrupts its own journey every thirty seconds.
func TestDeliberatePausingIsNotStuck(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cases := map[string]Health{
		"exploring": {HP: 20, Now: now, Exploring: true, LastMoved: now.Add(-time.Hour)},
		"moving":    {HP: 20, Now: now, PositionChanged: true, LastMoved: now.Add(-time.Hour)},
		"fresh":     {HP: 20, Now: now, LastMoved: now.Add(-time.Second)},
		"dead":      {HP: 0, Now: now, LastMoved: now.Add(-time.Hour)},
		"offline":   {HP: 20, Now: now, Disconnected: true, LastMoved: now.Add(-time.Hour)},
	}
	for name, h := range cases {
		if IsStuck(h) {
			t.Errorf("%s was reported as stuck", name)
		}
	}
}

// TestNeverHavingMovedIsStuck. That is a bot that has not started, and it is
// stuck by any reasonable reading.
func TestNeverHavingMovedIsStuck(t *testing.T) {
	t.Parallel()

	if !IsStuck(Health{HP: 20, Now: time.Now()}) {
		t.Error("a bot that has never moved is not reported as stuck")
	}
}

// TestTheWatchdogStaysQuietWhenThereIsNothingWrong. A watchdog that fires on
// health is a watchdog that gets switched off, and then it is not there for
// the hour it was actually needed.
func TestTheWatchdogStaysQuietWhenThereIsNothingWrong(t *testing.T) {
	t.Parallel()

	now := time.Now()
	healthy := Health{HP: 20, Now: now, PositionChanged: true, LastMoved: now}

	fault, action := Decide(healthy, 5)
	if fault != FaultNone || action != ActNone {
		t.Errorf("got %v/%v on a healthy bot, want nothing at all", fault, action)
	}
	if line := watchReport(fault, action, 5); line != "" {
		t.Errorf("a healthy pass produced a log line: %q", line)
	}
}

// TestTheReportSaysWhatItDid. A watchdog pass that changes nothing should be
// silent; one that acts should say so, because "the bot stood still for two
// minutes" and "the watchdog did something about it" are different claims.
func TestTheReportSaysWhatItDid(t *testing.T) {
	t.Parallel()

	line := watchReport(FaultStuck, ActUnstick, 2)
	for _, want := range []string{"stuck", "unstick", "2"} {
		if !strings.Contains(line, want) {
			t.Errorf("report %q does not mention %q", line, want)
		}
	}
	if line := watchReport(FaultNone, ActNone, 0); line != "" {
		t.Errorf("a clean pass produced %q", line)
	}
}

// TestOneBlockIsRecognisedFromWhatTheBotCanSee. There is no wood, no chest, no
// night and nowhere to walk; every rule the brain has assumes terrain, and a
// bot that cannot tell spends ten minutes reaching for oak_log in a world that
// has never contained a tree.
func TestOneBlockIsRecognisedFromWhatTheBotCanSee(t *testing.T) {
	t.Parallel()

	if got := DetectOneBlock("none"); got == NotOneBlock {
		t.Error("an empty world was reported as a normal one")
	}
	if got := DetectOneBlock("grass_block"); got == NotOneBlock {
		t.Error("a world with exactly one block in reach is at least a hint")
	}
	if got := DetectOneBlock("stone, dirt, copper_ore"); got != NotOneBlock {
		t.Errorf("a normal world was reported as %v", got)
	}
	// Air is not a block in reach. A bot standing in the open sees nothing and
	// would otherwise look like a single-block world on every hillside.
	if got := DetectOneBlock("minecraft:air, minecraft:air, minecraft:cave_air"); got == DefinitelyOneBlock {
		t.Error("air was counted as the world's contents")
	}
}

// TestOneBlockNeedsConfirming. A bot in a cleared room briefly looks like this,
// and a sweep of identical floor tiles looks identical and is not a
// single-block world. Standing on it and getting the same answer twice is the
// difference.
func TestOneBlockNeedsConfirming(t *testing.T) {
	t.Parallel()

	if got := ConfirmOneBlock(PossiblyOneBlock, PossiblyOneBlock); got != DefinitelyOneBlock {
		t.Errorf("two agreeing readings gave %v, want a conclusion", got)
	}
	if got := ConfirmOneBlock(PossiblyOneBlock, NotOneBlock); got != NotOneBlock {
		t.Errorf("a disagreement gave %v, want the cautious answer", got)
	}
	if got := ConfirmOneBlock(NotOneBlock, PossiblyOneBlock); got != NotOneBlock {
		t.Errorf("a disagreement gave %v, want the cautious answer", got)
	}
}

// TestTheSingleBlockPlanSaysTheOneThingThereIsToDo. A bot in a single-block
// world has exactly one verb: break the block it is on and see what turned up.
// A plan that mentions exploring or gathering is a bot that looks stuck.
func TestTheSingleBlockPlanSaysTheOneThingThereIsToDo(t *testing.T) {
	t.Parallel()

	plan := OneBlockPlan("minecraft:copper_ore")
	for _, want := range []string{"copper_ore", "build upward", "do not stand there"} {
		if !strings.Contains(strings.ToLower(plan), want) {
			t.Errorf("the single-block plan does not say %q:\n%s", want, plan)
		}
	}
	// It must not send the bot looking for things that are not there. The plan
	// does NAME them, in order to say they are absent — that is useful, and it is
	// the opposite of telling the bot to go and find them. So the check is on
	// the instruction, not the noun.
	for _, instruction := range []string{"go find", "go looking", "look for a chest", "explore"} {
		if strings.Contains(strings.ToLower(plan), instruction) {
			t.Errorf("the single-block plan tells the bot to %q, which is not possible there:\n%s", instruction, plan)
		}
	}
	if !strings.Contains(plan, "no wood, no chest, no night") {
		t.Errorf("the single-block plan does not rule out what is absent:\n%s", plan)
	}
	// An unnamed block must still produce a usable instruction.
	if OneBlockPlan("") == "" {
		t.Error("an unknown block produced an empty plan")
	}
}
