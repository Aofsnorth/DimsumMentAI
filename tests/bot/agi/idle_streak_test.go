package agi_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
)

// The lobby failure this file guards: a bot whose decision model keeps picking
// "rest" reopened one rest window after another, and the idle nudges kept
// refreshing the movement clock, so the 90-second drift test never fired. The
// bot stood in the lobby for minutes while the log happily reported decisions
// every tick. These tests pin the two exits: the consecutive-idle cap in Idle,
// and the reset of the cap the moment the bot actually does something.

func TestIdleRefusesToOpenAFourthConsecutiveWindow(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	r := agi.NewBareForTest(agi.Config{IdleStillBias: 1})

	// Three windows in a row is the cap; each one is driven to expiry before
	// the next is opened, exactly the shape the loop produces.
	for i := 0; i < 3; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		r.Idle(agi.Snapshot{Now: now})
		if !r.ShouldIdle(agi.Snapshot{Now: now}) {
			t.Fatalf("window %d did not open", i+1)
		}
	}

	// The fourth call must not open a window: it is the rest-budget-exhausted
	// turn. With no bot attached nothing wanders, but the crucial assertion is
	// that idling stops: no window, no stillness, no rest to hide in.
	now := start.Add(3 * time.Minute)
	r.Idle(agi.Snapshot{Now: now})
	if r.ShouldIdle(agi.Snapshot{Now: now}) {
		t.Fatal("a fourth rest window opened after the cap; the bot would stand still forever")
	}
}

func TestDoingSomethingResetsTheIdleStreak(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	r := agi.NewBareForTest(agi.Config{IdleStillBias: 1})

	for i := 0; i < 3; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		r.Idle(agi.Snapshot{Now: now})
		if !r.ShouldIdle(agi.Snapshot{Now: now}) {
			t.Fatalf("window %d did not open", i+1)
		}
	}

	// A real activity, the way doActivity records one. This is the path the
	// wander branch and every Jev-chosen non-rest activity share.
	r.RecordActivityForTest("wander")

	now := start.Add(3 * time.Minute)
	r.Idle(agi.Snapshot{Now: now})
	if !r.ShouldIdle(agi.Snapshot{Now: now}) {
		t.Fatal("an activity reset was not honoured; rest would be unreachable after one drift")
	}
}

func TestRestDoesNotResetTheIdleStreak(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	r := agi.NewBareForTest(agi.Config{IdleStillBias: 1})

	// Two windows, then a rest CHOICE recorded the way doActivity would record
	// it. Rest is the thing being capped, so recording it must not clear the
	// count: the third window opens (2 < cap), and the FOURTH is refused. Had
	// rest reset the streak, the fourth window would open and the bot would
	// never run out of rest budget.
	for i := 0; i < 2; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		r.Idle(agi.Snapshot{Now: now})
		if !r.ShouldIdle(agi.Snapshot{Now: now}) {
			t.Fatalf("window %d did not open", i+1)
		}
	}
	r.RecordActivityForTest("rest")

	third := start.Add(2 * time.Minute)
	r.Idle(agi.Snapshot{Now: third})
	if !r.ShouldIdle(agi.Snapshot{Now: third}) {
		t.Fatal("the third window did not open; the cap fired one window early")
	}

	fourth := start.Add(3 * time.Minute)
	r.Idle(agi.Snapshot{Now: fourth})
	if r.ShouldIdle(agi.Snapshot{Now: fourth}) {
		t.Fatal("a rest choice reset the streak; the fourth window opened anyway")
	}
}
