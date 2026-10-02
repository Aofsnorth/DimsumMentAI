package ai_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/ai"
)

// --- Completion-claim detection ---
//
// A completion claim is the model asserting that something is already done.
// The bot emits action tags and the host acts on them, so a claim of completion
// paired with a still-pending action is a claim about a future event.

// TestClaimsCompletion_IndonesianMarkers is the corpus the system prompt itself
// bans at prompts.go rule 4 ("Never say 'berhasil', 'sudah', 'done'"), plus the
// everyday variants a model reaches for on its own.
func TestClaimsCompletion_IndonesianMarkers(t *testing.T) {
	t.Parallel()

	for _, phrase := range []string{
		"Berhasil!",
		"berhasil",
		"sudah kelar",
		"udah selesai",
		"selesai semua",
		"beres",
		"tuntas",
		"Sukses!",
	} {
		if !ai.ClaimsCompletion(phrase) {
			t.Errorf("ClaimsCompletion(%q) = false, want true", phrase)
		}
	}
}

func TestClaimsCompletion_EnglishMarkers(t *testing.T) {
	t.Parallel()

	for _, phrase := range []string{
		"Done!",
		"done",
		"Finished the build.",
		"Completed.",
		"Success!",
		"Successfully gathered 5 oak logs.",
		"I got it.",
		"got the wood",
		"mined the vein",
		"crafted the table",
		"killed the zombie",
		"collected the drops",
		"already cleared the area",
		"I killed the Ender Dragon",
	} {
		if !ai.ClaimsCompletion(phrase) {
			t.Errorf("ClaimsCompletion(%q) = false, want true", phrase)
		}
	}
}

// TestClaimsCompletion_FutureAndHedgedAreNotClaims is the false-positive guard,
// and it is the reason this filter can run on every parsed reply without
// eating the bot's normal conversation. "Belum berhasil" is the exact
// sentence a careful model writes when it is being honest about not having
// done something yet, and a filter that flagged it would be worse than none.
func TestClaimsCompletion_FutureAndHedgedAreNotClaims(t *testing.T) {
	t.Parallel()

	for _, phrase := range []string{
		"belum berhasil",
		"belum selesai",
		"nggak berhasil",
		"tidak berhasil",
		"gak jadi selesai",
		"akan selesai",
		"bakal kelar",
		"mau mined dulu",
		"not done yet",
		"haven't finished",
		"I haven't killed it",
		"cannot be done",
		"will finish later",
		"bisa mined nanti",
		"Oke siap, aku coba dulu.",
		"Bentar ya cuy, aku ambilin dulu kayunya.",
		"Oke, aku ke sana ya.",
		"Aku ikutin.",
		// Present-progressive. "Doing stuff" is not a claim that it is finished.
		"Doing stuff now",
	} {
		if ai.ClaimsCompletion(phrase) {
			t.Errorf("ClaimsCompletion(%q) = true, want false (future/hedged is not a claim)", phrase)
		}
	}
}

// --- Filtering claims out of a pending-action reply ---

// TestFilterCompletionClaims_DropsClaimWhenActionIsPending is the core
// invariant. The reply both says it is done and asks for the action that would
// cause it to be done, so the sentence is a statement about a future event and
// has to go. The rest of the reply survives.
func TestFilterCompletionClaims_DropsClaimWhenActionIsPending(t *testing.T) {
	t.Parallel()

	speech := "Berhasil! Aku craft crafting table sekarang. Oke."
	got, dropped := ai.FilterCompletionClaims(speech, true)

	if !dropped {
		t.Error("dropped = false, want true")
	}
	if strings.Contains(strings.ToLower(got), "berhasil") {
		t.Errorf("filtered speech still claims completion: %q", got)
	}
	if !strings.Contains(got, "Oke.") {
		t.Errorf("filtered speech dropped non-claiming text: %q", got)
	}
}

// TestFilterCompletionClaims_KeepsIntentWhenActionIsPending is the other half:
// the ordinary "acknowledging intent only" reply the prompt asks for must come
// through untouched, or the bot goes mute every time it agrees to do something.
func TestFilterCompletionClaims_KeepsIntentWhenActionIsPending(t *testing.T) {
	t.Parallel()

	speech := "Oke siap, aku coba dulu."
	got, dropped := ai.FilterCompletionClaims(speech, true)

	if dropped {
		t.Error("dropped = true, want false for an intent-only acknowledgement")
	}
	if got != speech {
		t.Errorf("speech = %q, want it unchanged %q", got, speech)
	}
}

// TestFilterCompletionClaims_KeepsInfoReplyWhenNoActionIsPending is the escape
// hatch prompts.go rule 4 grants explicitly: "unless the user only asked for
// information". A bot reporting what it already sees is allowed to speak in
// the past tense, and a filter that took that away would break every status
// and inventory question.
func TestFilterCompletionClaims_KeepsInfoReplyWhenNoActionIsPending(t *testing.T) {
	t.Parallel()

	for _, speech := range []string{
		"Aku punya 12 diamond di inventory.",
		"HP aku 18/20 sekarang.",
		"Udah selesai tadi diquest itu.",
	} {
		got, dropped := ai.FilterCompletionClaims(speech, false)
		if dropped {
			t.Errorf("FilterCompletionClaims(%q, false) dropped = true, want false", speech)
		}
		if got != speech {
			t.Errorf("speech = %q, want unchanged %q", got, speech)
		}
	}
}

// TestFilterCompletionClaims_SentenceGranularity is what keeps the bot's
// voice. A claim sitting in its own sentence costs one sentence; a claim
// sharing a sentence with real content costs only that sentence, never the
// whole reply.
func TestFilterCompletionClaims_SentenceGranularity(t *testing.T) {
	t.Parallel()

	got, dropped := ai.FilterCompletionClaims("Aku ke batu itu. Berhasil ambil 5 log. Habis itu ke pohon.", true)
	if !dropped {
		t.Fatal("dropped = false, want true")
	}
	for _, want := range []string{"Aku ke batu itu.", "Habis itu ke pohon."} {
		if !strings.Contains(got, want) {
			t.Errorf("filtered speech %q lost %q", got, want)
		}
	}
	if strings.Contains(strings.ToLower(got), "berhasil") {
		t.Errorf("filtered speech still claims completion: %q", got)
	}
}

// --- Status faithfulness ---
//
// The truth about an action arrives as an event.ActionStatus, and the reply
// the player reads is written by the model from a prompt. Nothing else in the
// pipeline compares the two, which is where a false achievement would enter.

func TestStatusFaithful_FailureNarrationThatClaimsSuccessIsUnfaithful(t *testing.T) {
	t.Parallel()

	failed := ai.Outcome{Action: "mine", Item: "diamond_ore", Success: false, Error: "tidak dapat diamond_ore"}

	for _, reply := range []string{
		"Berhasil! Aku nemu 12 diamond.",
		"Done, the vein is cleared.",
		"Sudah kutambang semua diamondnya.",
		"I mined the diamond ore.",
		" successfully gathered 8 logs",
	} {
		if ai.StatusFaithful(reply, failed) {
			t.Errorf("StatusFaithful(%q, failed) = true, want false (claims an achievement that did not happen)", reply)
		}
	}
}

func TestStatusFaithful_FailureNarrationThatAdmitsFailureIsFaithful(t *testing.T) {
	t.Parallel()

	failed := ai.Outcome{Action: "mine", Item: "diamond_ore", Success: false, Error: "tidak dapat diamond_ore"}

	for _, reply := range []string{
		"Gagal, tidak ada diamond ore di sini.",
		"Belum berhasil nemu diamondnya.",
		"Aku belum selesai nambang.",
		"tidak bisa dapat diamond ore di area ini",
	} {
		if !ai.StatusFaithful(reply, failed) {
			t.Errorf("StatusFaithful(%q, failed) = false, want true (honest failure report)", reply)
		}
	}
}

func TestStatusFaithful_SuccessNarrationThatDeniesSuccessIsUnfaithful(t *testing.T) {
	t.Parallel()

	// The mirror case: the server confirmed 8 logs, and the model tells the
	// player it got nothing. A player watching the bot pick items up while it
	// says "I couldn't find any" is being lied to just as hard.
	ok := ai.Outcome{Action: "gather", Item: "oak_log", Count: 8, Success: true}

	for _, reply := range []string{
		"Gagal, gak nemu kayu sama sekali.",
		"Tidak bisa dapat kayu di sini.",
		"failed to gather anything",
		"couldn't find any logs",
	} {
		if ai.StatusFaithful(reply, ok) {
			t.Errorf("StatusFaithful(%q, success) = true, want false (denies what the server confirmed)", reply)
		}
	}
}

func TestStatusFaithful_SuccessNarrationIsFaithful(t *testing.T) {
	t.Parallel()

	ok := ai.Outcome{Action: "gather", Item: "oak_log", Count: 8, Success: true}

	for _, reply := range []string{
		"Berhasil, aku kumpulin 8 oak log.",
		"Got 8 logs.",
		"Aku dapet kayu 8 planks.",
	} {
		if !ai.StatusFaithful(reply, ok) {
			t.Errorf("StatusFaithful(%q, success) = false, want true", reply)
		}
	}
}

// TestStatusFaithful_NeutralNarrationIsFaithful keeps the filter from becoming
// a censor. Most status replies are neutral — "the vein ran out around here" —
// and a filter that rewrote those would be noise.
func TestStatusFaithful_NeutralNarrationIsFaithful(t *testing.T) {
	t.Parallel()

	ok := ai.Outcome{Action: "explore", Count: 12, Success: true}
	if !ai.StatusFaithful("Aku udah jalan ke arah utara.", ok) {
		t.Error("a neutral reply was judged unfaithful")
	}
}

// TestStatusFaithful_EmptyReplyIsFaithful guards the degenerate case: an
// empty reply says nothing false, and treating it as a contradiction would
// make the bot repeat itself every time the model chooses silence.
func TestStatusFaithful_EmptyReplyIsFaithful(t *testing.T) {
	t.Parallel()

	if !ai.StatusFaithful("", ai.Outcome{Action: "mine", Success: false, Error: "tidak dapat"}) {
		t.Error("empty reply was judged unfaithful")
	}
}

// TestFallbackStatusMessage_ReportsTheServerTruth is what a caller falls back
// to when the model's narration contradicts the status. It has to name the
// real outcome, because the whole point of the fallback is that the player is
// no longer relying on what the model felt like saying.
func TestFallbackStatusMessage_ReportsTheServerTruth(t *testing.T) {
	t.Parallel()

	failed := ai.Outcome{Action: "mine", Item: "diamond_ore", Success: false, Error: "tidak dapat diamond_ore"}
	got := ai.FallbackStatusMessage(failed)
	if !strings.Contains(strings.ToLower(got), "tidak") && !strings.Contains(strings.ToLower(got), "gagal") {
		t.Errorf("failure fallback %q does not read as a failure", got)
	}
	if strings.Contains(strings.ToLower(got), "berhasil") {
		t.Errorf("failure fallback %q claims success", got)
	}

	ok := ai.Outcome{Action: "gather", Item: "oak_log", Count: 8, Success: true}
	got = ai.FallbackStatusMessage(ok)
	if strings.Contains(strings.ToLower(got), "gagal") {
		t.Errorf("success fallback %q reads as a failure", got)
	}
	if !strings.Contains(got, "8") {
		t.Errorf("success fallback %q does not carry the confirmed count", got)
	}
}

// --- Prompt-level honesty rules ---

// TestHonestyRules_ForbidInventedAchievements pins the block's presence and
// its wording. A prompt fix that quietly loses a rule is the failure mode
// here: nothing fails at runtime, the bot just starts claiming things again.
func TestHonestyRules_ForbidInventedAchievements(t *testing.T) {
	t.Parallel()

	rules := strings.ToLower(ai.HonestyRules)
	for _, want := range []string{
		"only what",
		"evidence",
		"never",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("HonestyRules() does not mention %q:\n%s", want, rules)
		}
	}
}

// TestBuildSystemPrompt_CarriesHonestyRules is the wiring test. The block is
// useless if it is defined and never appended, and nothing else in the build
// would notice.
func TestBuildSystemPrompt_CarriesHonestyRules(t *testing.T) {
	t.Parallel()

	nc := ai.NewLLMClient("nvidia", "test-model", "")
	prompt := nc.BuildSystemPrompt("Luna", "X:1 Y:2 Z:3", "X:1 Y:2 Z:3", "", "")

	if !strings.Contains(prompt, "[HONEST REPORTING]") {
		t.Error("system prompt is missing the [HONEST REPORTING] block")
	}
}

// TestBuildSystemPrompt_HonestyRulesDoNotEraseGrounding is the regression
// guard on my own change: honesty rules are additive, and appending them must
// not displace the inventory grounding the model needs to answer truthfully.
func TestBuildSystemPrompt_HonestyRulesDoNotEraseGrounding(t *testing.T) {
	t.Parallel()

	nc := ai.NewLLMClient("nvidia", "test-model", "")
	prompt := nc.BuildSystemPrompt("Luna", "X:1 Y:2 Z:3", "X:1 Y:2 Z:3", "Diamond Sword", "oak_log x4")

	for _, want := range []string{"[HONEST REPORTING]", "[ANTI-HALLUCINATION]", "[INVENTORY RULE]", "Diamond Sword", "oak_log x4"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt lost %q after the honesty block was added", want)
		}
	}
}
