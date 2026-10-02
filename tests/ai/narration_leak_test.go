package ai_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/ai"
)

// What reached the player during a failed scaffold run was not a badly worded
// sentence. It was the model thinking out loud, ending with a draft of the
// answer somewhere in the middle of a paragraph:
//
//	failed, so the placement/operation didn't happen. We need answer status under
//	25 words, Indonesian, no action tags. Mention cell holds dirt ... "Gagal,
//	cuy—scaffold nggak bisa dipasang ..."
//
// That is not something an honesty filter can improve, because there is no
// sentence in it to check. The player simply reads the model's notes, so the
// check below is about recognising a working-out, not about whether it is polite.

func TestTheLiveScratchpadIsRecognisedAsALeak(t *testing.T) {
	t.Parallel()

	// Trimmed from the real log, with the parts that leaked a prompt left in.
	const leaked = `failed, so the placement/operation didn't happen. We need answer status under 25 words, Indonesian, no action tags. Mention cell holds dirt, scaffold blocked, count 0 maybe. "Gagal, cuy—scaffold nggak bisa dipasang karena selnya berisi Dirt." Is "scaffold nggak bisa dipasang" action was maybe place? Result says count 0, reason cannot place block: cell holds dirt, not something to break for scaffold.`

	if !ai.IsNarrationLeak(leaked) {
		t.Error("IsNarrationLeak = false on the scratchpad that actually reached chat, want true")
	}
}

func TestAnEscapedQuoteIsEnoughOnItsOwn(t *testing.T) {
	t.Parallel()

	// The highest-precision signal available: no chat line a player should read
	// contains a backslash-escaped quote. That is what a model writing a draft
	// answer as a string literal looks like, and it appears regardless of which
	// language or style the reasoning is in.
	const short = `Gagal, cuy—scaffold nggak bisa dipasang.\"`

	if !ai.IsNarrationLeak(short) {
		t.Error("IsNarrationLeak = false on an escaped-quote reply, want true")
	}
}

func TestASelfInstructionIsALeakEvenWhenItIsShort(t *testing.T) {
	t.Parallel()

	// The length check alone would pass this. A model can narrate its reasoning
	// in one short sentence, and that is just as unreadable as a paragraph of it.
	for _, reply := range []string{
		"We need to say this failed, so scaffold failed.",
		"I should tell the player the cell holds dirt.",
		"The prompt says to use friendly names, so Dirt it is.",
		"saya perlu jawab bahwa gagal",
	} {
		if !ai.IsNarrationLeak(reply) {
			t.Errorf("IsNarrationLeak = false on %q, want true", reply)
		}
	}
}

func TestAnOrdinaryStatusLineIsNotALeak(t *testing.T) {
	t.Parallel()

	// The whole point of the guard is that it does not fire on real replies. If it
	// did, every status line would silently become the deterministic fallback and
	// the bot would sound like a form letter.
	for _, reply := range []string{
		"Scaffold-nya gagal karena selnya berisi Dirt.",
		"Gagal: sel target berisi dirt (dirt).",
		"Sudah aku pasang satu blok, ya.",
		"Aku belum nemu batu di sini, coba ke gua其他 arah deh.",
		"Air-nya dingin banget, nanti aku ambil dulu.",
		"Chest-nya isi 12 oak log sama 3 diamond.",
	} {
		if ai.IsNarrationLeak(reply) {
			t.Errorf("IsNarrationLeak = true on the ordinary reply %q, want false", reply)
		}
	}
}

func TestAnEmptyReplyIsNotALeak(t *testing.T) {
	t.Parallel()

	// Empty is handled by the caller — there is simply nothing to send. Calling
	// it a leak here would make the two decisions indistinguishable in a log.
	for _, reply := range []string{"", "   ", "\n\t "} {
		if ai.IsNarrationLeak(reply) {
			t.Errorf("IsNarrationLeak = true on the empty reply %q, want false", reply)
		}
	}
}

func TestALongParagraphIsALeakWithoutNeedingAPhrase(t *testing.T) {
	t.Parallel()

	// A status reply is asked for under 25 words. Sixty is a loose ceiling, so
	// this catches the paragraph of reasoning that has no recognisable marker in
	// it rather than only the ones that echo the prompt.
	var sb strings.Builder
	for i := 0; i < 70; i++ {
		sb.WriteString("kata ")
	}
	if !ai.IsNarrationLeak(sb.String()) {
		t.Error("IsNarrationLeak = false on a 70-word paragraph, want true")
	}
}
