package agi_test

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/jev"
)

// The bot could already look at things. What it could not do was have an opinion
// about it: the idle gaze settled on a player, a creature or a block on a dice
// roll, and the model had no vocabulary to say otherwise. These tests pin the
// two things that make handing over that choice safe — it stays optional, and it
// stays a preference rather than an order.

func newGazeTestBot() *bot.Bot {
	return &bot.Bot{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		RecentBotMessages: make(map[string]time.Time),
	}
}

func TestEveryGazeAnswerIsUnderstood(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw  string
		want agi.GazeHint
	}{
		{"person", agi.GazeAtPerson},
		{"mob", agi.GazeAtMob},
		{"block", agi.GazeAtBlock},
		{"around", agi.GazeWanderly},
		{"  PERSON  ", agi.GazeAtPerson},
		{"\tmob\n", agi.GazeAtMob},
	} {
		if got := agi.ParseGazeForTest(tc.raw); got != tc.want {
			t.Errorf("ParseGaze(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestAMissingOrInventedGazeAnswerChangesNothing(t *testing.T) {
	t.Parallel()

	// The property that makes the whole feature safe to add. A model that has
	// never heard of the question, one that answers with an empty string, and one
	// that invents an option all have to land on auto, which is the exact
	// behaviour the bot had before this existed. Anything else would mean a
	// narrower or older model could change how the bot looks.
	for _, raw := range []string{"", "   ", "look_left", "up", "at_the_moon", "PERSON!"} {
		if got := agi.ParseGazeForTest(raw); got != agi.GazeAuto {
			t.Errorf("ParseGaze(%q) = %q, want auto", raw, got)
		}
	}
}

func TestAGazeIsOnlyAskedWhereTheHeadIsFree(t *testing.T) {
	t.Parallel()

	// Walking, gathering and chatting are activities where the head belongs to
	// the movement. Asking anyway spends a decision on an answer that cannot be
	// used, and a bot that turns away from where it is going while it walks is
	// the failure this is scoped to avoid.
	for _, activity := range []string{jev.ActivityRest, jev.ActivitySleep, jev.ActivityWander, jev.ActivityExplore} {
		if !agi.GazeAppliesToForTest(activity) {
			t.Errorf("gaze was ruled out for %q, want it available while the head is free", activity)
		}
	}
	for _, activity := range []string{jev.ActivityGather, jev.ActivityMine, jev.ActivityChat, jev.ActivityApproach, jev.ActivityCraft} {
		if agi.GazeAppliesToForTest(activity) {
			t.Errorf("gaze was allowed for %q, want it refused while the body is committed", activity)
		}
	}
}

func TestTheGazeInstructionExpiresOnItsOwn(t *testing.T) {
	t.Parallel()

	// The brain decides on a slower clock than the body moves. Without a
	// lifetime, one decision would have the bot watching a player who walked away
	// twenty minutes ago.
	b := newGazeTestBot()
	b.SetGazeHint("person", time.Now().Add(25*time.Second))

	if got := b.GazePreference(); got != "person" {
		t.Fatalf("GazePreference = %q, want %q while the instruction is live", got, "person")
	}

	b.Mu.Lock()
	b.GazeUntil = time.Now().Add(-time.Second)
	b.Mu.Unlock()

	if got := b.GazePreference(); got != "" {
		t.Errorf("GazePreference = %q, want it empty once the instruction has expired", got)
	}
}

func TestClearingTheGazeRemovesItImmediately(t *testing.T) {
	t.Parallel()

	// A model that stops caring about a player should stop looking at them on the
	// next decision, not up to a lifetime later.
	b := newGazeTestBot()
	b.SetGazeHint("mob", time.Now().Add(time.Hour))
	b.ClearGazeHint()

	if got := b.GazePreference(); got != "" {
		t.Errorf("GazePreference = %q after clearing, want empty", got)
	}
}

func TestTheGazeQuestionNamesWhatTheModelIsChoosing(t *testing.T) {
	t.Parallel()

	questions := jev.BuildGazeQuestion()
	raw, ok := questions[jev.QGaze]
	if !ok {
		t.Fatal("the gaze question is missing from the batch")
	}

	body := string(raw)
	for _, option := range []string{jev.GazePerson, jev.GazeMob, jev.GazeBlock, jev.GazeAround} {
		if !strings.Contains(body, option) {
			t.Errorf("the gaze question does not offer %q: %s", option, body)
		}
	}
	// The model has to be told the boundary, or it will answer "gaze" the way a
	// model asked where to go answers a destination.
	if !strings.Contains(body, "never where it goes") {
		t.Errorf("the gaze question does not say the head-only boundary: %s", body)
	}
}
