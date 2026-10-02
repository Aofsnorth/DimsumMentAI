package ai_test

import (
	"testing"

	"bedrock-ai/internal/ai"
)

// The bot shipped this into chat:
//
//	"10 batang kayu berhasil_subscription砍到手啦 wkwk, stok kayu makin numpuk nih."
//
// An identifier fragment welded into the sentence, then Chinese characters, in
// a reply that was otherwise ordinary Indonesian and agreed with the server
// result. Nothing else objects to it — the length is fine, the tone is fine,
// the claim is true. Only the words in it are wrong.

// TestTheShippedCorruptionIsCaught is the reported line, verbatim.
func TestTheShippedCorruptionIsCaught(t *testing.T) {
	t.Parallel()

	reported := "10 batang kayu berhasil_subscription砍到手啦 wkwk, stok kayu makin numpuk nih."

	if !ai.IsScriptCorrupted(reported) {
		t.Errorf("IsScriptCorrupted(%q) = false, want true: the bot shipped an identifier and foreign script", reported)
	}
}

// TestABareIdentifierIsCaught covers the other half on its own. The prompt asks
// for friendly names precisely so "oak_log" never reaches a player.
func TestABareIdentifierIsCaught(t *testing.T) {
	t.Parallel()

	leaks := []string{
		"Dapet 10 oak_log di tas.",
		"sudah crafting_table jadi",
		"network_id-nya aneh",
		"BlockRuntimeID 0 buat support",
	}

	// Note what is absent: "stack-nya full, inventory penuh". A hyphen is
	// Indonesian, and "inventory" is a word. Only the underscore — the shape a
	// field name actually has — is a leak.
	for _, reply := range leaks {
		if !ai.IsScriptCorrupted(reply) {
			t.Errorf("IsScriptCorrupted(%q) = false, want true: an internal identifier reached chat", reply)
		}
	}
}

// TestOrdinaryIndonesianIsNotCorrupted is the direction that matters most. The
// bot writes casually and mixes English into Indonesian all the time, so the
// check has to be narrow enough that none of that trips it.
func TestOrdinaryIndonesianIsNotCorrupted(t *testing.T) {
	t.Parallel()

	ordinary := []string{
		"Dapet 10 log oak, stok aman.",
		"Kayunya makin deras, 10 batang lagi wkwk.",
		"Sudah kubuat crafting table, cek dulu ya.",
		"Makin lama makin banyak kayunya.",
		"Aku crafting planks dulu biar bisa lanjut.",
		"Stok wood masih aman, relax.",
	}

	for _, reply := range ordinary {
		if ai.IsScriptCorrupted(reply) {
			t.Errorf("IsScriptCorrupted(%q) = true, want false: ordinary chat was treated as corruption", reply)
		}
	}
}

// TestAnEmptyReplyIsNotCorrupted keeps the check from claiming blank output is
// broken, which would push empty strings down the fallback path.
func TestAnEmptyReplyIsNotCorrupted(t *testing.T) {
	t.Parallel()

	if ai.IsScriptCorrupted("") {
		t.Error("IsScriptCorrupted(\"\") = true, want false")
	}
}
