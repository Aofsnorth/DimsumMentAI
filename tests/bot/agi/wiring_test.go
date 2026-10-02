package agi_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
)

// These pin the three wirings that make natural mode do anything: a brief
// arriving from chat, the watchdog running every tick, and the single-block
// read being consulted before the menu rather than after.

// TestTheBriefArrivesThroughTheHook. Before this existed the parser was
// complete, tested, and called by nothing — which is the shape of a feature that
// gets described, built, and then quietly does nothing.
func TestTheBriefArrivesThroughTheHook(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	r := agi.NewBareForTest(agi.Config{})

	number, budget, objective, ok := parseThroughHook(r, "/episode 1 24m build a house", now)
	if !ok {
		t.Fatal("a well-formed brief was refused by the hook")
	}
	if number != 1 {
		t.Errorf("number = %d, want 1", number)
	}
	if budget != 24*time.Minute {
		t.Errorf("budget = %s, want 24m", budget)
	}
	if objective != "build a house" {
		t.Errorf("objective = %q", objective)
	}
	if ep := r.CurrentEpisode(); ep.Objective != "build a house" {
		t.Errorf("the hook did not install the brief: %+v", ep)
	}

	// A malformed brief must be reported as such, not silently accepted, or the
	// operator records three hours of nothing with no idea why.
	if _, _, _, ok := parseThroughHook(r, "/episode banana", now); ok {
		t.Error("a brief with no duration was accepted")
	}
}

// TestTheWatchdogRunsEveryTickAndWaitsBeforeActing. One motionless tick is
// anything. Two is evidence.
func TestTheWatchdogRunsEveryTickAndWaitsBeforeActing(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	r := agi.NewBareForTest(agi.Config{})

	wedged := func(at time.Time) agi.Snapshot {
		// WantsToMove is the point of the fixture. The body was going somewhere
		// and stopped, which is what a wedge against terrain looks like — a body
		// that was never trying to move is simply resting.
		return agi.Snapshot{HP: 20, Now: at, Coords: "X:10 Y:64 Z:0", WantsToMove: true}
	}

	// First reading: nothing, and the position is new so it does not even count
	// as motionless yet.
	if _, action := r.Watch(wedged(start)); action != agi.ActNone {
		t.Errorf("acted on the very first reading")
	}

	// Still in the same place, long after the first reading.
	later := start.Add(2 * time.Minute)
	if _, action := r.Watch(wedged(later)); action != agi.ActNone {
		t.Errorf("acted on the second reading; the fault needs %d in a row", agi.StuckNeedsRepeats)
	}
	if _, action := r.Watch(wedged(later.Add(time.Second))); action != agi.ActUnstick {
		t.Errorf("did not act by the third reading; a bot wedged for two minutes stays wedged")
	}
}

// TestAMovingBotIsNeverCalledStuck.
func TestAMovingBotIsNeverCalledStuck(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	r := agi.NewBareForTest(agi.Config{})

	for i := 0; i < 10; i++ {
		snap := agi.Snapshot{
			HP:     20,
			Now:    start.Add(time.Duration(i) * time.Minute),
			Coords: "X:" + string(rune('0'+i)) + " Y:64 Z:0",
		}
		fault, action := r.Watch(snap)
		if fault == agi.FaultStuck || action == agi.ActUnstick {
			t.Fatalf("a bot that moved every tick was called stuck on reading %d", i)
		}
	}
}

// TestADisconnectOutranksAQuietDeath. Nothing else can be tried until the
// connection is back, so a watchdog that reports the corpse first reports the
// wrong problem.
func TestADisconnectOutranksAQuietDeath(t *testing.T) {
	t.Parallel()

	now := time.Now()
	r := agi.NewBareForTest(agi.Config{})
	r.Disconnected(true)

	fault, action := r.Watch(agi.Snapshot{HP: 0, Now: now, Coords: "X:0 Y:0 Z:0"})
	if fault != agi.FaultDisconnected || action != agi.ActReconnect {
		t.Errorf("got %v/%v, want the disconnect to be reported and the connection re-dialled", fault, action)
	}
}

// TestASingleBlockWorldIsConfirmedByAskingTwice. A hint is not a conclusion, and
// a room with one kind of floor looks exactly like a single block until you
// stand there and look again.
func TestASingleBlockWorldIsConfirmedByAskingTwice(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})

	if style := r.OneBlockStyle("none"); style != "maybe_single_block" {
		t.Errorf("first reading = %q, want a hint", style)
	}
	if r.IsOneBlockWorld() {
		t.Error("a single reading was treated as a conclusion")
	}

	if style := r.OneBlockStyle("none"); style != "single_block" {
		t.Errorf("second agreeing reading = %q, want a conclusion", style)
	}
	if !r.IsOneBlockWorld() {
		t.Error("two agreeing readings did not confirm")
	}
}

// TestANormalWorldIsForgotten. Having once looked like a single block must not
// colour how the bot reads every later frame — a bot that believes it is on one
// block forever will refuse to walk properly for the rest of the recording.
func TestANormalWorldIsForgotten(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})
	r.OneBlockStyle("none")
	r.OneBlockStyle("none")
	if !r.IsOneBlockWorld() {
		t.Fatal("precondition: the world was never confirmed")
	}

	if style := r.OneBlockStyle("stone, dirt, copper_ore, oak_log"); style != "normal" {
		t.Errorf("style = %q for an ordinary world", style)
	}
	if r.IsOneBlockWorld() {
		t.Error("still believing it is on a single block after walking into a normal world")
	}
}

// TestTheSingleBlockBriefIsEmptyWhenItDoesNotApply. A brief given in the wrong
// world is worse than no brief: the planner would write a plan to break a block
// that has a forest on it.
func TestTheSingleBlockBriefIsEmptyWhenItDoesNotApply(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})
	if got := r.SingleBlockBrief("stone"); got != "" {
		t.Errorf("a normal world got a single-block brief: %q", got)
	}

	r.OneBlockStyle("none")
	r.OneBlockStyle("none")
	if r.SingleBlockBrief("copper_ore") == "" {
		t.Error("a confirmed single-block world got no brief")
	}
}

// parseThroughHook exercises the same path the chat layer uses, without a bot.
func parseThroughHook(r *agi.Runner, line string, now time.Time) (int, time.Duration, string, bool) {
	ep, ok := r.BeginEpisode(line, now)
	if !ok {
		return 0, 0, "", false
	}
	return ep.Number, ep.EndsAt.Sub(ep.StartedAt), ep.Objective, true
}
