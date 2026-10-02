package chat_test

import (
	"testing"

	"bedrock-ai/internal/bot/chat"
)

// A player who says "ke tempat ku" has given an explicit instruction. The bot
// answering it in chat and then standing still is worse than not understanding
// it, because it looks like the bot heard and refused.
//
// The old matcher enumerated four contiguous strings — `kesini`, `ke sini`,
// `come here`, `datang ke sini` — and everything else fell through to a chat
// reply. These tests pin the phrasings that were being missed, in the register a
// player actually types.

// TestTheComePhrasingsAPlayerActuallyUses is the reported case. Every one of
// these is the same request and every one of them used to match nothing.
func TestTheComePhrasingsAPlayerActuallyUses(t *testing.T) {
	t.Parallel()

	phrasings := []string{
		"ke tempat ku",
		"ke tempatku",
		"ke tempat ku dong",
		"kesini",
		"ke sini",
		"datang ke sini",
		"datang sini",
		"sini dong",
		"come here",
		"come to me",
		"menuju ke sini",
		"PERGI KE TEMPAT KU",
	}

	for _, msg := range phrasings {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()

			got := chat.MovementActionsForTest(msg)
			if len(got) == 0 {
				t.Fatalf("%q produced no action: the bot will chat back and stand still", msg)
			}
			if got[0].Label != "come" && got[0].Label != "follow" {
				t.Errorf("%q produced %q, want come or follow", msg, got[0].Label)
			}
		})
	}
}

// TestAPermanentOrderIsAFollowNotAOneOffTrip separates the two requests. "come"
// walks to where the player is standing and stops; "follow" keeps going after
// them. A bot that answered a standing order with a one-off walk would arrive,
// stop, and look broken the moment the player took another step.
func TestAPermanentOrderIsAFollowNotAOneOffTrip(t *testing.T) {
	t.Parallel()

	follows := []string{
		"ikut aku",
		"ikut aku dong",
		"follow me",
		"temani aku",
		"iring aku ya",
	}

	for _, msg := range follows {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()

			got := chat.MovementActionsForTest(msg)
			if len(got) == 0 {
				t.Fatalf("%q produced no action", msg)
			}
			if got[0].Label != "follow" {
				t.Errorf("%q produced %q, want follow: a standing order answered with a one-off walk stops as soon as the player moves", msg, got[0].Label)
			}
		})
	}
}

// TestCoordinatesStillWin keeps the most specific intent first. "pergi ke 10 64
// -3" is a goto, and reading it as "come here" would send the bot to the player
// instead of the coordinates they just typed.
func TestCoordinatesStillWin(t *testing.T) {
	t.Parallel()

	got := chat.MovementActionsForTest("pergi ke 10 64 -3")
	if len(got) == 0 {
		t.Fatal("a coordinate request produced no action")
	}
	if got[0].Label != "goto" {
		t.Errorf("coordinate request produced %q, want goto", got[0].Label)
	}
	if got[0].Param != "10,64,-3" {
		t.Errorf("coordinate param = %q, want 10,64,-3", got[0].Param)
	}
}

// TestUnrelatedChatProducesNoMovement stops the matcher from becoming eager. A
// bot that walks to whoever mentions "sini" is a bot that cannot have a
// conversation, and the old four-string list was conservative for a reason.
func TestUnrelatedChatProducesNoMovement(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{
		"",
		"halo",
		"apa kabar?",
		"terima kasih sudah membantu",
		"aku lagi di rumah",
	} {
		if got := chat.MovementActionsForTest(msg); len(got) != 0 {
			t.Errorf("%q produced %v, want no action", msg, got)
		}
	}
}
