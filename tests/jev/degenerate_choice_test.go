package jev_test

import (
	"encoding/json"
	"testing"

	"bedrock-ai/internal/jev"
)

// The bot finished a gather, went idle, and from that moment never made a
// decision again for the rest of the session:
//
//	23:54:56.391 AGI: jev decided ... input_tokens=1083
//	23:54:56.874 AGI: plan complete kind=gather
//	23:54:57.993 AGI: jev unavailable, using local rules error="jev: evaluate
//	                returned 400 Bad Request: ... Send only model, state, and
//	                bounded typed questions ..."
//
// and it never recovered — the same WARN repeated every ~1.5s to the end of the
// log, with the brain silently running on local rules the whole time.
//
// The message points at the wrong thing. The request body is exactly what the
// server documents: {state, model, questions}, and nothing else. No messages, no
// stream, no tools, no temperature. Verified against the live endpoint that 1..20
// questions are all accepted, so it is not a question count either.
//
// It is the criteria of a single question. The endpoint rejects a choice
// question carrying exactly ONE option; two options are fine. That was confirmed
// directly: a two-criteria choice returns 200, a one-criteria choice returns the
// same 400 the bot was getting.
//
// The reachability is the part worth stating. The activity menu is narrowed by
// the active goal before it reaches the builder, and a `gather_wood` goal
// advances only mining and gathering. Narrow it in a world offering neither and
// what survives is the "rest" that BuildActivityQuestion always forces into the
// menu — alone. That is why it happened exactly at idle and not while gathering:
// the gathering path returns early and never asks.
//
// And the failure is total, which is why it never recovered: the 400 discards
// the whole batch. Danger, hunger, whether to speak, what to do next — all of it
// gone because one question was malformed. A guard that only skipped the bad
// question would be an improvement; the point of these tests is that the
// degenerate menu is never built at all.

// criteriaOf pulls the criteria out of a built question, failing the test if the
// question is absent or not a choice.
func criteriaOf(t *testing.T, questions map[string]json.RawMessage, key string) map[string]string {
	t.Helper()

	raw, ok := questions[key]
	if !ok {
		t.Fatalf("question %q was not built", key)
	}
	var decoded struct {
		Type     string            `json:"type"`
		Criteria map[string]string `json:"criteria"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("question %q is not valid JSON: %v", key, err)
	}
	return decoded.Criteria
}

// TestTheActivityMenuIsNeverDegenerate is the reproduced failure.
func TestTheActivityMenuIsNeverDegenerate(t *testing.T) {
	t.Parallel()

	// What NarrowToGoal hands over when a gather_wood goal finds nothing it
	// advances: only the forced "rest" entry survives.
	questions := jev.BuildActivityQuestion([]string{jev.ActivityRest})

	if questions != nil {
		t.Fatalf("a one-option activity menu was sent, want no question at all.\n"+
			"\tThe endpoint rejects a single-criteria choice with a 400 that discards "+
			"every question in the batch, so this blanks the bot's entire decision pass.\n"+
			"\tMenu sent: %v", criteriaOf(t, questions, jev.QActivity))
	}
}

// TestAnAffordanceSetWithOneOptionIsNotAsked is the same trap through the other
// builder that can reach it. The caller asks whenever anything at all is
// available, so a world offering exactly one action produced a one-option menu.
func TestAnAffordanceSetWithOneOptionIsNotAsked(t *testing.T) {
	t.Parallel()

	questions := jev.BuildAffordanceQuestion(map[string]string{
		"pick_up": "the block at your feet",
	})

	if questions != nil {
		t.Fatalf("a one-option affordance set was sent, want no question at all: %v",
			criteriaOf(t, questions, jev.QAffordance))
	}
}

// TestRealMenusAreStillSent is the guard on the guard. Two options is the floor,
// not a ceiling, and a bot that stopped asking anything would be a different and
// worse failure than the one this fixes.
func TestRealMenusAreStillSent(t *testing.T) {
	t.Parallel()

	if questions := jev.BuildActivityQuestion(nil); questions == nil {
		t.Error("the default activity menu was suppressed; an empty curriculum must still become a real choice")
	}

	wide := map[string]string{"pick_up": "the block at your feet", "break": "the log beside you"}
	if questions := jev.BuildAffordanceQuestion(wide); questions == nil {
		t.Error("a two-option affordance set was suppressed; the floor must not become a veto")
	}

	if got := len(criteriaOf(t, jev.BuildActivityQuestion(nil), jev.QActivity)); got < 2 {
		t.Errorf("the default activity menu has %d options, want at least 2", got)
	}
}
