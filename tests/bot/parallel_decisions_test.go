package bot_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/agi"
)

// Jev decides several things per tick: what to do, how to travel, and what to
// look at. Those are not three sequential round trips — they ride in one batch
// and are applied in one tick — and the bot holds them in three independent
// places so a later one cannot silently undo an earlier one.
//
// That independence is the property worth pinning. "Sprint while looking at
// that player" only works if the gait latch and the look target are separate
// state, and the failure mode when they are not is invisible: the bot arrives
// having correctly decided both and correctly executed neither.

func newDecisionBot() *bot.Bot {
	return &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// TestGaitAndGazeDoNotOverwriteEachOther is the whole question. A sprint latch
// and a gaze latch set in the same instant must both still be readable after.
func TestGaitAndGazeDoNotOverwriteEachOther(t *testing.T) {
	t.Parallel()

	b := newDecisionBot()

	b.SetSprintHint(true)
	b.SetGazeHint("person", time.Now().Add(time.Minute))

	sprint, hop, latched := b.SprintHint()
	if !latched || !sprint || !hop {
		t.Errorf("gait latch = (%t,%t,%t), want (true,true,true): the gaze decision overwrote the travel style", sprint, hop, latched)
	}
	if kind := b.GazePreference(); kind != "person" {
		t.Errorf("gaze = %q, want person: the travel style overwrote the gaze", kind)
	}
}

// TestClearingOneDecisionLeavesTheOther is the same property from the other
// side, and it is the direction that bites: an activity that walks the bot
// somewhere clears the gait latch on arrival, and that must not take the gaze
// with it.
func TestClearingOneDecisionLeavesTheOther(t *testing.T) {
	t.Parallel()

	b := newDecisionBot()

	b.SetSprintHint(true)
	b.SetGazeHint("person", time.Now().Add(time.Minute))

	// A trip ends.
	b.ClearSprintHint()

	if _, _, latched := b.SprintHint(); latched {
		t.Error("the gait latch survived the end of the trip")
	}
	if kind := b.GazePreference(); kind != "person" {
		t.Errorf("gaze = %q after the trip ended, want person: ending a trip cleared the gaze too", kind)
	}
}

// TestAnAutoGazeDoesNotClearTheTravelStyle keeps the two decisions independent
// in the direction the tick actually applies them: applyLocomotion then
// applyGaze, in that order, every tick.
func TestAnAutoGazeDoesNotClearTheTravelStyle(t *testing.T) {
	t.Parallel()

	b := newDecisionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	// Exactly the sequence runner.go applies per tick.
	b.SetSprintHint(true)
	r.ApplyGazeForTest(agi.GazeAuto)

	sprint, _, latched := b.SprintHint()
	if !latched || !sprint {
		t.Errorf("gait = (%t,%t), want latched sprint: an auto gaze cleared a travel style the model had just chosen", sprint, latched)
	}
}

// TestAWalkingBotStillHonoursATrackedLookTarget covers the composed behaviour the
// player actually sees: travelling on a chosen gait while the head is on a
// person. The two live in different fields precisely so this is possible.
func TestAWalkingBotStillHonoursATrackedLookTarget(t *testing.T) {
	t.Parallel()

	b := newDecisionBot()
	b.MovementState = "walk_to"
	b.SetSprintHint(true)
	b.SetGazeHint("person", time.Now().Add(time.Minute))

	if sprint, _, latched := b.SprintHint(); !latched || !sprint {
		t.Fatal("the travel style was lost while the bot was walking")
	}
	if kind := b.GazePreference(); kind != "person" {
		t.Errorf("gaze = %q while walking, want person", kind)
	}
}

// TestTheGazeLatchExpiresOnItsOwn keeps the recorded answer from outliving the
// moment. Jev's gaze is a hint for a few seconds; leaving it latched forever
// would pin the bot's head to whatever it looked at when the model last spoke.
func TestTheGazeLatchExpiresOnItsOwn(t *testing.T) {
	t.Parallel()

	b := newDecisionBot()

	// Already expired.
	b.SetGazeHint("person", time.Now().Add(-time.Second))

	if kind := b.GazePreference(); kind != "" {
		t.Errorf("gaze = %q from an expired latch, want empty", kind)
	}
}
