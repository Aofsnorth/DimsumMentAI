package reaction_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/chat/reaction"
)

// A person does not answer a chat message at the speed the network returns it.
// The bot used to reply the instant the language model finished, which made the
// reply time a pure function of model latency: near-instant on a short answer,
// and identical every time the same question was asked. Both halves of that are
// wrong — a person always takes some time to notice, and the time depends on
// what the message asks.

func TestEveryMessageWaitsLongEnoughToLookConsidered(t *testing.T) {
	t.Parallel()

	// The floor matters more than it looks. A short answer can come back from a
	// fast model in well under 50ms, and a reply that leaves that fast reads as
	// a script no matter how good the reply itself is.
	for _, msg := range []string{
		"ok", "hi", "sip", "makasih", "follow me", "?", "bisa bawain batu?",
		"pergi ke gudang sekarang", " inventories",
	} {
		min, _ := reaction.Band(msg)
		if min < 200*time.Millisecond {
			t.Fatalf("message %q has a floor of %v, below the point where a reply "+
				"stops reading as instant", msg, min)
		}
		got := reaction.Delay(msg)
		if got < min {
			t.Fatalf("Delay(%q) = %v, under its own floor of %v", msg, got, min)
		}
	}
}

func TestAQuestionIsAnsweredSlowerThanAnAcknowledgement(t *testing.T) {
	t.Parallel()

	_, ackMax := reaction.Band("ok")
	askMin, _ := reaction.Band("bisa bawain batu ke gudang?")

	if askMin <= ackMax {
		t.Fatalf("a question's floor (%v) must sit above an acknowledgement's ceiling (%v); "+
			"the pause before composing is one of the things a person does", askMin, ackMax)
	}
}

func TestQuestionMarkersAreRecognisedWithoutPunctuation(t *testing.T) {
	t.Parallel()

	// Players type "gimana" and "boleh" without a question mark, and those are
	// exactly the messages that need the longer pause.
	ackMin, ackMax := reaction.Band("sip")
	for _, msg := range []string{
		"kamu gimana",
		"boleh buka chest",
		"bisa ke sini",
		"kenapa tidak jalan",
	} {
		min, max := reaction.Band(msg)
		if min < ackMax || max < min {
			t.Fatalf("message %q was not classified as needing thought: band %v-%v, "+
				"an acknowledgement band is %v-%v", msg, min, max, ackMin, ackMax)
		}
	}
}

func TestTheDelayIsCappedSoItNeverReadsAsAHang(t *testing.T) {
	t.Parallel()

	// Past a couple of seconds the wait stops looking like a person composing
	// and starts looking like a bot that stopped listening.
	for _, msg := range []string{
		"ok",
		strings300(),
		"tolong bantu saya untuk mengubah semua_gate di area Rosetta dan mengambilkan batu dari gudang",
	} {
		if _, max := reaction.Band(msg); max > 2400*time.Millisecond {
			t.Fatalf("message band ceiling %v exceeds the cap", max)
		}
		if got := reaction.Delay(msg); got > 2400*time.Millisecond {
			t.Fatalf("Delay(%q) = %v, over the cap", msg, got)
		}
	}
}

func TestTheDelayIsJitteredSoTheSameMessageIsNotMechanical(t *testing.T) {
	t.Parallel()

	// This is the part a server or an observer can actually time. A bot that
	// answers the same message in exactly the same time every time is
	// reporting a constant, and constants are what statistical checks cluster
	// on.
	const samples = 40
	msg := "bisa bawain batu ke gudang?"

	seen := make(map[time.Duration]struct{})
	for i := 0; i < samples; i++ {
		seen[reaction.Delay(msg)] = struct{}{}
	}
	if len(seen) < samples/4 {
		t.Fatalf("%d samples of the same message produced only %d distinct delays; "+
			"the delay is effectively fixed", samples, len(seen))
	}

	min, max := reaction.Band(msg)
	inside := 0
	for i := 0; i < samples; i++ {
		d := reaction.Delay(msg)
		if d >= min && d <= max {
			inside++
		}
	}
	if inside < samples-1 {
		t.Fatalf("%d of %d samples fell outside the declared band %v-%v", samples-inside, samples, min, max)
	}
}

func TestShortAcknowledgementsAreFasterThanOrdinaryStatements(t *testing.T) {
	t.Parallel()

	ackMin, _ := reaction.Band("terima kasih")
	stmtMin, _ := reaction.Band("aku ambil batu dulu ya bentar")

	if ackMin >= stmtMin {
		t.Fatalf("an acknowledgement's floor (%v) should sit below an ordinary "+
			"statement's (%v)", ackMin, stmtMin)
	}
}

func TestEmptyAndOddInputStillProducesAUsableDelay(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{"", "   ", "\n\t", "?", "!!!", "ok.", "  OK  "} {
		min, max := reaction.Band(msg)
		if max < min {
			t.Fatalf("band for %q is inverted: %v-%v", msg, min, max)
		}
		if got := reaction.Delay(msg); got < min {
			t.Fatalf("Delay(%q) = %v, under floor %v", msg, got, min)
		}
	}
}

func TestAcknowledgementMatchingIgnoresPunctuationAndCase(t *testing.T) {
	t.Parallel()

	baseMin, baseMax := reaction.Band("ok")
	for _, variant := range []string{"OK", "Ok", "ok.", "ok!", "  ok  "} {
		min, max := reaction.Band(variant)
		if min != baseMin || max != baseMax {
			t.Fatalf("variant %q was not treated as the same acknowledgement: "+
				"band %v-%v, want %v-%v", variant, min, max, baseMin, baseMax)
		}
	}
}

func strings300() string {
	b := make([]byte, 0, 300)
	for i := 0; i < 30; i++ {
		b = append(b, "permission to do the thing "...)
	}
	return string(b)
}
