package ai_test

import (
	"testing"

	"bedrock-ai/internal/ai"
)

// The bot shipped its own abandoned first draft into chat:
//
//	"Berhasil! Logs/Cardboard... eh, Log sebanyak 10 biji udah masuk tas."
//
// It started to name the item, second-guessed itself, corrected, and sent all
// three beats. Every existing narration check passed it: the sentence is
// grammatical, well under the word ceiling, free of scratchpad markers, and it
// agrees with the server result. Only the hesitation marks it.

// TestTheShippedSelfCorrectionIsCaught is the reported line, verbatim.
func TestTheShippedSelfCorrectionIsCaught(t *testing.T) {
	t.Parallel()

	reported := "Berhasil! Logs/Cardboard... eh, Log sebanyak 10 biji udah masuk tas. Mau lanjut nebang atau bikin sesuatu dari kayu itu?"

	if !ai.IsSelfCorrection(reported) {
		t.Errorf("IsSelfCorrection(%q) = false, want true: the bot shipped its abandoned first draft", reported)
	}
}

// TestAnOrdinarySentenceIsNotSelfCorrection is the other direction, and it is
// the one that matters more: a filter that flags hesitation in general would
// replace every slightly-lazy sentence in the bot's vocabulary.
func TestAnOrdinarySentenceIsNotSelfCorrection(t *testing.T) {
	t.Parallel()

	ordinary := []string{
		"Dapet 10 log oak, stok aman.",
		"Kayunya kekumpul lagi, stok makin aman buat crafting.",
		"Gagal tebang: tidak ada pohon di sekitar.",
		"Sudah kubuat crafting table.",
		"Aku ke sana ya, tunggu sebentar.",
		"eh, aku ke sana dulu ya",
		"Hmm, tunggu ya.",
		"Kamu mau aku ambil yang mana?",
	}

	for _, reply := range ordinary {
		if ai.IsSelfCorrection(reply) {
			t.Errorf("IsSelfCorrection(%q) = true, want false: an ordinary line was treated as a revision", reply)
		}
	}
}

// TestAnEmptyReplyIsNotSelfCorrection keeps the check from claiming an empty
// string is a failure. Empty is handled upstream, and a filter that reports it
// as a leak would fill the fallback path with blank lines.
func TestAnEmptyReplyIsNotSelfCorrection(t *testing.T) {
	t.Parallel()

	if ai.IsSelfCorrection("") {
		t.Error("IsSelfCorrection(\"\") = true, want false")
	}
	if ai.IsSelfCorrection("   ") {
		t.Error("IsSelfCorrection(whitespace) = true, want false")
	}
}

// TestTheEnglishFormsAreCaughtToo keeps the check from being Indonesian-only.
// The model writes in whatever register it picks, and the log shows it mixing
// them freely.
func TestTheEnglishFormsAreCaughtToo(t *testing.T) {
	t.Parallel()

	english := []string{
		"Got 10 Oak Logs... I mean, 10 oak logs.",
		"Done — hmm, actually that was stone.",
		"That's 10 logs — oops, 12.",
	}

	for _, reply := range english {
		if !ai.IsSelfCorrection(reply) {
			t.Errorf("IsSelfCorrection(%q) = false, want true", reply)
		}
	}
}
