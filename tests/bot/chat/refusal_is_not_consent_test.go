// A refusal and an agreement differ by a prefix, and this file is about the
// code that could not tell them apart.
//
// The LLM is chatty. When it commits to an action without emitting an
// <action> tag, the pipeline looks for a marker word in the reply and
// synthesises the action itself. The markers were matched by substring, and
// several carried a trailing space specifically to try to get a word boundary.
// That did not work, because "ya " is a substring of "saya ": the refusal "Saya
// tak bisa" matched, and the action synthesised on a match is a mine run. So the
// bot cheerfully started the exact job the model had just declined.
//
// The other half is a count. "Take all of the stone" was encoded as the number
// zero, and zero is not neutral downstream — the gatherer reads a count of zero
// or less as "one block". So "all" felled one block and reported success.

package chat_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/chat"
)

// TestARefusalIsNotAnAgreement is the regression. Every one of these is a way of
// declining, in the language this bot actually speaks to players.
func TestARefusalIsNotAnAgreement(t *testing.T) {
	t.Parallel()

	refusals := []string{
		"Saya tak bisa",
		"saya tidak bisa sekarang",
		"saya belum bisa",
		"belum, tunggu dulu",
		"tidak, jangan",
		"saya belum selesai",
		"maaf saya tidak bisa",
		"saya belum siap",
	}

	for _, reply := range refusals {
		t.Run(reply, func(t *testing.T) {
			t.Parallel()

			if chat.IsAffirmativeReplyForTest(reply) {
				t.Errorf("%q was read as a commitment to act; the bot would start a job "+
					"the model just declined", reply)
			}
		})
	}
}

// TestAnAgreementIsStillAnAgreement is the other direction, so the guard cannot
// pass by never matching anything. Without this, "fix it by matching nothing" is
// a passing implementation of the first test.
func TestAnAgreementIsStillAnAgreement(t *testing.T) {
	t.Parallel()

	agreements := []string{
		"Siap",
		"oke",
		"Oke, aku kerjakan",
		"iya",
		"Iya, langsung saja",
		"ya",
		"Ya!",
		"ya.",
		"sip",
		"SIP, aku mulai",
		"oke baik",
		"lanjut",
		"sure, I'll do it",
		"got it",
		"bentar ya",
		"akan ku lakukan",
	}

	for _, reply := range agreements {
		t.Run(reply, func(t *testing.T) {
			t.Parallel()

			if !chat.IsAffirmativeReplyForTest(reply) {
				t.Errorf("%q was not read as a commitment to act; the model agreed and "+
					"the action was not synthesised", reply)
			}
		})
	}
}

// TestWordsAreNotMatchedInsideOtherWords pins the boundary rule directly, on the
// words most likely to be substrings of something innocent.
//
// This is the whole of what a marker-word matcher can promise. It is not a
// classifier: "saya coba ya" ("I'll try, ok?") contains a standalone "ya" and
// is read as a commitment, which is arguably correct, and "saya akan bilang
// nanti" ("I'll tell you later") contains "akan" and is read as a commitment to
// gather, which is not. Deciding that needs to read the sentence, and reading
// the sentence is what the model was already asked to do. What the matcher can
// promise is that a refusal which merely contains a marker as part of a longer
// word — which is where every false positive in the original came from — loses.
func TestWordsAreNotMatchedInsideOtherWords(t *testing.T) {
	t.Parallel()

	// Each of these contains a marker as a strict substring of a longer word.
	inside := []string{
		"dayat",      // contains "ya"
		"biaya",      // contains "ya"
		"kurray",     // contains "ya"
		"biarkan",    // contains "iya"
		"berokean",   // contains "oke"
		"masyarakat", // contains "ya" + "ya"
		"permainan",  // contains "main", no marker, but listed for the shape
	}
	for _, reply := range inside {
		if chat.IsAffirmativeReplyForTest(reply) {
			t.Errorf("%q matched a marker that only appears inside a longer word", reply)
		}
	}
}

// TestTheSynthesisedActionCarriesACount is the "all" half. A count of zero is
// not "everything" anywhere downstream: the gatherer clamps anything at or below
// zero to a single block, so the bot did one block of work and said it was done.
func TestTheSynthesisedActionCarriesACount(t *testing.T) {
	t.Parallel()

	steps := chat.InferActionIntentForTest("kumpulin semua batu yang ada", "")
	if len(steps) == 0 {
		t.Fatal("\"ambil semua batu yang ada\" produced no action at all")
	}

	param := steps[0].Param
	if !strings.Contains(param, ",") {
		t.Fatalf("param %q carries no count", param)
	}
	count := param[strings.LastIndex(param, ",")+1:]
	if count == "0" {
		t.Errorf("param %q encodes \"all\" as zero; downstream that means one block, "+
			"so the player asked for everything and got one", param)
	}
}

// TestAnExplicitCountStillWins makes sure the "all" path did not swallow the
// ordinary one.
func TestAnExplicitCountStillWins(t *testing.T) {
	t.Parallel()

	steps := chat.InferActionIntentForTest("kumpulin 5 batu", "")
	if len(steps) == 0 {
		t.Fatal("\"kumpulin 5 batu\" produced no action")
	}
	if !strings.HasSuffix(steps[0].Param, ",5") {
		t.Errorf("param %q, want it to end in ,5", steps[0].Param)
	}
}
