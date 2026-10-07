package bot_test

import (
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
)

// The reason this exists: a server relays a whisper to its other clients as a
// visible chat line. So a bot that answers a whisper with ordinary chat puts the
// content of a private conversation in front of everybody. A player who finds
// that out once stops using whispers, and there is no undoing that — which makes
// this a correctness property, not a tone preference.

// TestAWhisperOpensAPrivateConversation is the bookkeeping: a whisper from a
// player has to be remembered as a private channel to that player.
func TestAWhisperOpensAPrivateConversation(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{}
	if got := b.WhisperPartner(); got != "" {
		t.Fatalf("a fresh bot reports a whisper partner: %q", got)
	}

	b.NoteWhisperFrom("Synterix")
	if got := b.WhisperPartner(); got != "Synterix" {
		t.Errorf("WhisperPartner() = %q, want Synterix", got)
	}
}

// This is the claim the whole file rests on: a reply to the player who whispered
// is routed privately.
func TestAReplyToTheWhisperSenderIsPrivate(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{}
	b.NoteWhisperFrom("Synterix")

	if got := b.WhisperRouteForTest("Synterix"); got != "whisper" {
		t.Errorf("a reply to the player who whispered routes as %q, want whisper.\n"+
			"A server relays public chat to everyone, so a public answer here leaks "+
			"the private conversation to the whole server.", got)
	}
}

// One player's private conversation must not swallow a reply meant for another.
// Otherwise a bot mid-DM starts whispering at strangers.
func TestAPrivateConversationDoesNotCaptureOtherPlayers(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{}
	b.NoteWhisperFrom("Synterix")

	if got := b.WhisperRouteForTest(".Pigpiggy5612"); got != "public" {
		t.Errorf("a reply to an unrelated player routes as %q while a DM is open, want public", got)
	}
	// The name match is case-insensitive: a server may echo a name with different
	// casing than the one the tracked player list uses.
	if got := b.WhisperRouteForTest("synterix"); got != "whisper" {
		t.Errorf("the same player in different casing routes as %q, want whisper", got)
	}
}

// An open whisper conversation does not stay open forever. A public message long
// after the last whisper is not part of a DM that ended, and answering it
// privately means talking to somebody who has moved on.
func TestAStaleWhisperIsNotStillOpen(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{}
	b.NoteWhisperFrom("Synterix")
	b.AgeWhisperConversationForTest(10 * time.Minute)

	if got := b.WhisperRouteForTest("Synterix"); got != "public" {
		t.Errorf("a conversation open ten minutes routes as %q, want public", got)
	}
}

// Answering in public again is the explicit close. Without it, a player who
// switches back to public chat keeps getting whispers from the bot.
func TestAPublicReplyEndsThePrivateConversation(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{}
	b.NoteWhisperFrom("Synterix")

	// A stranger closing it must not: the conversation is with somebody else.
	b.ClearWhisperWith("SomebodyElse")
	if got := b.WhisperRouteForTest("Synterix"); got != "whisper" {
		t.Errorf("a stranger closed the conversation: reply routes as %q, want whisper", got)
	}

	b.ClearWhisperWith("synterix")
	if got := b.WhisperRouteForTest("Synterix"); got != "public" {
		t.Errorf("the conversation stayed open after the player ended it: reply routes as %q, want public", got)
	}
}

// A whisper with no sender has nobody to answer, so it goes nowhere.
func TestAWhisperWithNoSenderIsNotRoutedAnywhere(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{}
	b.NoteWhisperFrom("Synterix")

	if got := b.WhisperRouteForTest(""); got == "whisper" {
		t.Error("a reply with no recipient was routed as a whisper")
	}
}

// The line the whisper command puts on the wire is what makes the reply private
// rather than a public message with an extra word in front of it.
func TestWhisperToADisconnectedBotFailsLoudly(t *testing.T) {
	t.Parallel()

	// A silent failure here is the worst outcome: a dropped private reply is
	// invisible to the sender, who concludes the bot ignored them.
	b := &bot.Bot{}
	err := b.SendWhisper("Synterix", "halo")
	if err == nil {
		t.Fatal("SendWhisper reported success on a bot that is not connected")
	}
	if !strings.Contains(err.Error(), "terhubung") {
		t.Errorf("error = %v, want it to say the bot is not connected", err)
	}

	if err := b.SendWhisper("", "halo"); err == nil {
		t.Error("SendWhisper accepted an empty recipient")
	}
}

// SendReply with no connection must not panic, and must not claim to have sent
// anything.
func TestSendReplyOnADisconnectedBotIsSafe(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SendReply panicked on a disconnected bot: %v", r)
		}
	}()

	b := &bot.Bot{}
	b.NoteWhisperFrom("Synterix")
	b.SendReply("Synterix", "halo")
	b.SendReply("Synterix", "")
}
