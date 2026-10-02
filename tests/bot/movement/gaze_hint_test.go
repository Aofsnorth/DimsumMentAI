package movement_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// The gaze hint names WHAT the head settles on, never how the body moves, and
// never invents something to look at. Those two properties are what keep handing
// a model influence over perception from turning into a bot that lies with its
// eyes.

func gazeBot() *bot.Bot {
	return &bot.Bot{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		RecentBotMessages: make(map[string]time.Time),
		MovementState:     "idle",
		WorldModel:        pathfinder.NewLocalWorldModel(),
	}
}

func gazeTick(b *bot.Bot) *movement.TickContext {
	return &movement.TickContext{
		B:       b,
		CurrPos: mgl32.Vec3{0.5, 64, 0.5},
		MState:  "idle",
	}
}

// A gaze hint must never reach the body.
//
// This is the property the whole feature rests on, and it is the one a reviewer
// should be able to check by reading the test rather than the diff: every branch
// of the hinted path writes TargetYaw and TargetPitch, and neither of those is
// the body's facing. A bot whose torso follows a model's curiosity is a bot
// that walks into things, because computeMoveSpeed reads the body yaw.
func TestNoGazeHintEverTurnsTheBody(t *testing.T) {
	t.Parallel()

	for _, hint := range []string{"", "person", "mob", "block", "around"} {
		b := gazeBot()
		if hint != "" {
			b.GazeHint = hint
			b.GazeUntil = time.Now().Add(time.Minute)
		}

		tc := gazeTick(b)
		// A body yaw the hint has to explain for itself if it ever moved it.
		tc.Yaw = 123
		tc.ApplyIdleLookForTest()

		if tc.Yaw != 123 {
			t.Errorf("hint %q changed the body's yaw from 123 to %v: a model with the keys to the facing is a model with the keys to the physics", hint, tc.Yaw)
		}
	}
}

// A request for something that is not there must not be satisfied with a
// fabrication.
//
// The bot cannot watch a player who is not nearby, so it must fall through to
// its own rules rather than point at the nearest wall. A bot that picks a
// subject and then cannot find it looks broken in a way that no amount of extra
// realism elsewhere repairs — which is why this is a property and not a nicety.
func TestARequestWithNothingBehindItIsNotFabricated(t *testing.T) {
	t.Parallel()

	for _, hint := range []string{"person", "mob", "block"} {
		b := gazeBot()
		b.GazeHint = hint
		b.GazeUntil = time.Now().Add(time.Minute)

		tc := gazeTick(b)
		tc.ApplyIdleLookForTest()

		b.Mu.Lock()
		fixation := b.IdleLookTargetType
		b.Mu.Unlock()

		if fixation == "player" || fixation == "actor" || fixation == "block" {
			t.Errorf("hint %q produced a %q fixation in an empty world, want the request dropped", hint, fixation)
		}
	}
}

// "Around" is a real choice, not a fallback: a person watching empty air is
// scanning, and it is the most visible idle eye movement there is.
func TestLookingAroundAlwaysProducesAFixation(t *testing.T) {
	t.Parallel()

	b := gazeBot()
	b.GazeHint = "around"
	b.GazeUntil = time.Now().Add(time.Minute)

	tc := gazeTick(b)
	tc.ApplyIdleLookForTest()

	b.Mu.Lock()
	fixation := b.IdleLookTargetType
	until := b.NextIdleLookChange
	b.Mu.Unlock()

	if fixation != "wander" {
		t.Errorf("fixation = %q, want \"wander\" — looking at nothing must still be a scan", fixation)
	}
	if until.IsZero() {
		t.Error("no follow-on fixation was scheduled, so the head would stop after one glance")
	}
}

// The brain decides on a much slower clock than the body moves. Without expiry
// one instruction would outlive the thing it was about.
func TestAnExpiredGazeHintIsIgnored(t *testing.T) {
	t.Parallel()

	b := gazeBot()
	b.GazeHint = "person"
	b.GazeUntil = time.Now().Add(-time.Second)

	if got := b.GazePreference(); got != "" {
		t.Errorf("GazePreference = %q on an expired hint, want empty", got)
	}
}
