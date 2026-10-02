package ai_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/ai"
)

// These pin the property that makes the rest of the honesty work mean
// anything: every reply a player can read passes through ai.Parse, so a claim
// filter that lives there is the only place that can catch a claim on its way
// to chat. If this wiring is ever removed, the filter becomes dead code and
// these tests are the thing that notices.

// TestParse_ClaimOnPendingActionIsFiltered is the end-to-end shape. The model
// says it finished and asks for the action that would make it finished, in one
// reply, which is the single most common shape of a false achievement claim.
func TestParse_ClaimOnPendingActionIsFiltered(t *testing.T) {
	t.Parallel()

	r := ai.Parse("Berhasil! Aku craft crafting table sekarang. <action>craft:crafting_table,1</action>")

	if len(r.Actions) != 1 {
		t.Fatalf("Actions = %v, want the action to survive filtering", r.Actions)
	}
	if strings.Contains(strings.ToLower(r.CleanReply), "berhasil") {
		t.Errorf("CleanReply %q still claims completion while the action is still pending", r.CleanReply)
	}
}

// TestParse_IntentReplyIsUnchanged is the false-positive guard on the same
// wiring. Intent acknowledgements are the bot's normal reply to a request and
// must survive byte for byte, or the bot goes quiet whenever it agrees to do
// something.
func TestParse_IntentReplyIsUnchanged(t *testing.T) {
	t.Parallel()

	const want = "Oke siap, aku coba dulu."
	r := ai.Parse(want + " <action>gather:oak_log,4</action>")

	if r.CleanReply != want {
		t.Errorf("CleanReply = %q, want %q unchanged", r.CleanReply, want)
	}
}

// TestParse_InfoReplyWithNoActionIsUnchanged covers the reporting case: a
// player asking what the bot already has gets a past-tense answer with no
// action tag, and it must not be rewritten.
func TestParse_InfoReplyWithNoActionIsUnchanged(t *testing.T) {
	t.Parallel()

	const want = "Aku sudah kumpulin 8 oak log tadi."
	r := ai.Parse(want)

	if r.CleanReply != want {
		t.Errorf("CleanReply = %q, want %q unchanged", r.CleanReply, want)
	}
}
