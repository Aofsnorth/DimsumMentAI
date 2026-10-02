package agi_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
)

// The watchdog declared a walking bot to be stuck, one hundred and forty-four
// times in four minutes.
//
// The cause was that "has the bot moved" was answered by comparing a formatted
// coordinate string, and that string is rendered with "%.0f" because it is
// written for a model to read. A bot walking from one corner of a block to
// another produced an identical string, so after thirty seconds the watchdog
// concluded it had not moved — and then fired ActUnstick, which clears the
// world model, one hundred and forty-four times over, while the log recorded
// sixteen repaths and a dozen distinct positions.
//
// These tests pin the property that was broken: sub-block movement counts.

// TestSubBlockMovementCountsAsMovement is the bug, stated directly.
func TestSubBlockMovementCountsAsMovement(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	// Two positions inside the same whole block. The rendered coordinate string
	// is identical for both; the body genuinely moved.
	start := time.Now()
	r.ObservePositionForTest(b, 10.5, 80.0, 194.25, start)
	health := r.ObservePositionForTest(b, 10.9, 80.0, 194.25, start.Add(time.Second))

	if !health.PositionChanged {
		t.Error("moving four-tenths of a block read as no movement: " +
			"the watchdog will declare a walking bot stuck and clear its world model")
	}
}

// TestStandingStillIsStillNotMovement is the control. The fix has to tell a bot
// that walked from one that stood still, or the watchdog becomes useless in the
// other direction and never fires at all.
func TestStandingStillIsStillNotMovement(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	start := time.Now()
	r.ObservePositionForTest(b, 10.5, 80.0, 194.25, start)
	health := r.ObservePositionForTest(b, 10.5, 80.0, 194.25, start.Add(30*time.Second))

	if health.PositionChanged {
		t.Error("a bot that did not move at all was reported as moving")
	}
}

// TestAFrozenBotStillTripsTheWatchdog is the property the whole thing exists
// for. Making the detector less twitchy must not make it blind.
func TestAFrozenBotStillTripsTheWatchdog(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	// Health matters here: Diagnose checks death before stuck, and a test bot
	// with zero health would be declared dead long before it was ever given the
	// chance to be declared stuck.
	b.Health = 20
	// The body has to be trying to move. A frozen one with no reason to be in
	// motion is a resting bot, and a watchdog that cannot tell the two apart
	// fired ActUnstick sixteen times in a row on a bot Jev had just sent to
	// rest — clearing the world model each time and moving it none of them.
	b.MovementState = "walk_to"
	r := agi.NewRunnerForTest(b, agi.Config{})

	start := time.Now()
	r.ObservePositionForTest(b, 10.5, 80.0, 194.25, start)

	// Ninety seconds of nothing, sampled every ten.
	var health agi.Health
	for i := 1; i <= 9; i++ {
		health = r.ObservePositionForTest(b, 10.5, 80.0, 194.25, start.Add(time.Duration(i)*10*time.Second))
	}

	if fault := agi.Diagnose(health); fault != agi.FaultStuck {
		t.Errorf("a bot frozen in place for ninety seconds diagnosed as %v, want stuck", fault)
	}
}

// TestAWalkingBotIsNotDiagnosedAsStuck is the regression proper: the same
// watchdog, fed a body that is genuinely walking sub-block by sub-block.
func TestAWalkingBotIsNotDiagnosedAsStuck(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	b.Health = 20
	r := agi.NewRunnerForTest(b, agi.Config{})

	start := time.Now()
	var health agi.Health
	// Slow drift, well under a block per sample, sustained for longer than the
	// stuck threshold.
	for i := 0; i <= 12; i++ {
		x := float32(10.5 + float64(i)*0.2)
		health = r.ObservePositionForTest(b, x, 80.0, 194.25, start.Add(time.Duration(i)*5*time.Second))
	}

	if fault := agi.Diagnose(health); fault == agi.FaultStuck {
		t.Error("a bot walking steadily was diagnosed as stuck: " +
			"this is the false positive that fired 144 times in four minutes")
	}
}
