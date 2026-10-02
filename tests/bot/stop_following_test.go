package bot_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/chat"
)

// A player who says "berhenti ikutin aku" has asked for something unambiguous.
//
// The log caught this being ignored. At 08:50:03 the player typed exactly that,
// and the bot's movement state stayed "follow" for the next four minutes,
// repathing after every "A* gave up short". A follow the player cannot stop is
// not a follow, it is a leash — and the opt-out has to be as easy to say as the
// opt-in, because the only way to stop it was a "!nofollow" command that exists
// in the source and in no player's head.

// TestTheStopPhrasingsAPlayerActuallyUses covers the opt-out in the register
// people type in.
func TestTheStopPhrasingsAPlayerActuallyUses(t *testing.T) {
	t.Parallel()

	stoppings := []string{
		"Berhenti ikutin aku",
		"berhenti ikut aku",
		"berhentilah mengikuti aku",
		"stop following me",
		"jangan ikut aku",
		"jangan ngikutin aku",
		"berhenti nemenin aku",
		"!nofollow",
	}

	for _, msg := range stoppings {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()

			if !chat.IsStopFollowingIntentForTest(msg) {
				t.Errorf("%q was not heard as an opt-out: the bot will keep following a player who asked it to stop", msg)
			}
		})
	}
}

// TestAnOptOutIsNotMisreadFromOrdinarySpeech keeps the matcher from being eager
// in the other direction. A bot that stops following whenever anyone says
// "berhenti" is a bot that walks away mid-conversation, and "berhenti dulu ya"
// is a player changing the subject rather than dismissing the bot.
func TestAnOptOutIsNotMisreadFromOrdinarySpeech(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{
		"berhenti dulu ya, aku mau makan",
		"kamu berhenti di mana tadi?",
		"stop duluan ya, gw reflection",
		"halo",
		"aku berhenti未定",
	} {
		if chat.IsStopFollowingIntentForTest(msg) {
			t.Errorf("%q was read as an opt-out: the bot would walk away mid-conversation", msg)
		}
	}
}

// TestTheOptOutOutlivesTheNextChatMessage is the actual reported failure. The
// opt-out has to be sticky, because a bot that forgets it as soon as the
// player's next message arrives is a bot that cannot be told to leave.
func TestTheOptOutOutlivesTheNextChatMessage(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if !b.ImplicitFollowAllowed("Arthenyxx") {
		t.Fatal("a fresh bot refuses to approach a player: the test is not exercising the opt-out")
	}

	b.DisableImplicitFollow("Arthenyxx")

	if b.ImplicitFollowAllowed("Arthenyxx") {
		t.Error("the opt-out did not take: the bot will drift toward a player who asked it to stop")
	}
	// Still refused, as many messages later as the player cares to send.
	for range 5 {
		if b.ImplicitFollowAllowed("Arthenyxx") {
			t.Fatal("the opt-out expired: the next chat message pulled the bot back under")
		}
	}
}

// TestTheOptOutIsPerPlayer keeps one person's decision from silencing the bot
// for everybody. The opt-in is the bot answering whoever it is configured for,
// so a refusal has to be that specific too.
func TestTheOptOutIsPerPlayer(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	b.DisableImplicitFollow("Arthenyxx")

	if !b.ImplicitFollowAllowed("SomeoneElse") {
		t.Error("one player's opt-out silenced the bot for everybody")
	}
}

// TestAnExplicitOrderClearsTheOptOut closes the loop. A player who changes
// their mind must be able to get the bot back without restarting it, and being
// told to do something is not the same as being followed around.
func TestAnExplicitOrderClearsTheOptOut(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	b.DisableImplicitFollow("Arthenyxx")
	b.AllowImplicitFollow("Arthenyxx")

	if !b.ImplicitFollowAllowed("Arthenyxx") {
		t.Error("an explicit follow order did not clear the earlier opt-out: the player would have to restart the bot")
	}
}

// TestOptingOutEndsTheFollowInForce is the other half of the opt-out: asking the
// bot to stop must actually stop it, not merely record the wish. A bot whose
// movement state is still "follow" after the request keeps repathing after the
// player, which is exactly the four minutes of log.
func TestOptingOutEndsTheFollowInForce(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	b.MovementState = "follow"
	b.TargetPlayerName = "Arthenyxx"

	b.DisableImplicitFollow("Arthenyxx")

	if b.MovementState == "follow" {
		t.Error("the follow is still in force after the player asked for it to stop")
	}
	if b.TargetPlayerName != "" {
		t.Errorf("the follow target survived the opt-out: %q", b.TargetPlayerName)
	}
}
