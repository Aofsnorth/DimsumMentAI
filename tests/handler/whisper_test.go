package handler_test

import (
	"testing"

	"bedrock-ai/internal/handler"
)

// A whisper reaches a Bedrock client as an ordinary system Text packet with an
// empty source and both participants written into the body:
//
//	"✉⬇ MSG (Synterix ➺ .ShigaLucifer) Halo, kamu pakai model apa?"
//
// Nothing else in the pipeline could read that. The source is empty, so the
// linked-player check rejected it and the log showed the whole banner as
// "chat ignored: not linked main player" — the player had whispered and the bot
// had gone silent, with no indication of why.

// TestWhisperBannerYieldsItsSenderAndBody is the case that actually happened.
func TestWhisperBannerYieldsItsSenderAndBody(t *testing.T) {
	t.Parallel()

	sender, recipient, body, ok := handler.ExtractWhisper(
		"✉⬇ MSG (Synterix ➺ .ShigaLucifer) Halo, kamu pakai model apa?", nil)

	if !ok {
		t.Fatal("a relayed whisper was not recognised, so the bot cannot answer it")
	}
	if sender != "Synterix" {
		t.Errorf("sender = %q, want Synterix", sender)
	}
	if recipient != ".ShigaLucifer" {
		t.Errorf("recipient = %q, want .ShigaLucifer", recipient)
	}
	if body != "Halo, kamu pakai model apa?" {
		t.Errorf("body = %q, want the text after the banner", body)
	}
}

// The arrow characters differ per server, and "->" is two runes that must not be
// split so that half of it is read as a name.
func TestWhisperArrowVariants(t *testing.T) {
	t.Parallel()

	for _, arrow := range []string{"➺", "➤", "»", "▶", "->", "→"} {
		line := "✉ MSG (Synterix " + arrow + " Bot) halo"
		sender, recipient, body, ok := handler.ExtractWhisper(line, nil)
		if !ok {
			t.Errorf("arrow %q: whisper not recognised", arrow)
			continue
		}
		if sender != "Synterix" || recipient != "Bot" || body != "halo" {
			t.Errorf("arrow %q: got (%q, %q, %q)", arrow, sender, recipient, body)
		}
	}
}

// A parenthesis in ordinary prose has the same shape as the banner. Without the
// label and the gap after the closing parenthesis this would be read as a
// whisper and answered as one.
func TestParentheticalProseIsNotAWhisper(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"Halo (Synterix ➺ Papua) Apa kabar?",
		"Synterix ➺ Bot halo",
		"(Synterix ➺ Bot)",
	} {
		if _, _, _, ok := handler.ExtractWhisper(line, nil); ok {
			t.Errorf("%q was read as a whisper", line)
		}
	}
}

// A whisper between two other players relays through the same banner. It must
// not be attributed to a sender at all, or the bot joins a conversation it was
// never part of.
func TestWhisperBetweenOthersStillNamesItsSender(t *testing.T) {
	t.Parallel()

	sender, recipient, _, ok := handler.ExtractWhisper(
		"✉⬇ MSG (.Pigpiggy5612 ➺ .MasOryTamfan) .baseACK disco", nil)
	if !ok {
		t.Fatal("the banner shape was not recognised at all; the recipient check has nothing to test")
	}
	if sender != ".Pigpiggy5612" {
		t.Errorf("sender = %q, want .Pigpiggy5612", sender)
	}
	if recipient != ".MasOryTamfan" {
		t.Errorf("recipient = %q, want .MasOryTamfan", recipient)
	}
}

// The resolver is how a decorated name in the banner becomes the tracked one.
func TestWhisperSenderResolvesThroughTheKnownPlayerList(t *testing.T) {
	t.Parallel()

	resolver := func(q string) (string, bool) {
		if q == "Synterix" {
			return "Synterix", true
		}
		return "", false
	}
	sender, _, body, ok := handler.ExtractWhisper("✉⬇ MSG (Synterix ➺ Bot) halo", resolver)
	if !ok {
		t.Fatal("whisper not recognised with a resolver present")
	}
	if sender != "Synterix" || body != "halo" {
		t.Errorf("got sender=%q body=%q", sender, body)
	}
}

// A system banner that happens to contain an arrow must not become a whisper.
func TestSystemSendersAreRejected(t *testing.T) {
	t.Parallel()

	if _, _, _, ok := handler.ExtractWhisper("✉ MSG (Server ➺ Bot) Maintenance at 3am", nil); ok {
		t.Error("a system sender was accepted as a whisper")
	}
}
