package ai_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/ai"
)

// The grammar is only half the fix for "cariin aku 6 oak log lagi".
//
// The model chooses the action tag. The handler can accept "+6" all it likes — if
// no prompt anywhere explains that "+" exists and when to reach for it, that
// branch is unreachable from the model's side and only the Indonesian keyword
// fallback ever fills it. Then the bug survives for every player whose phrasing
// the keyword list misses, or for any language the list has no word for.
//
// This is a documentation test, which is unusual, and it is here because the
// failure it guards is silent: nothing crashes, the tag simply comes out bare,
// and the bot quietly does nothing. Only the prompt text distinguishes the two.

// TestTheSystemRulesTeachTheAdditiveGatherForm is the pin.
func TestTheSystemRulesTeachTheAdditiveGatherForm(t *testing.T) {
	t.Parallel()

	rules := ai.BedrockSystemRules
	if rules == "" {
		t.Fatal("BedrockSystemRules is empty; there is nothing to teach the model with")
	}

	if !strings.Contains(rules, "gather:item_name,+count") {
		t.Error("the system rules never document <action>gather:item_name,+count</action>.\n" +
			"\tThe handler accepts the additive form, but the model has no way to know it exists.")
	}

	// A worked example, not just a signature. The bare form and the additive form
	// have to appear side by side, or the model cannot tell which one a given
	// request wants — which is the whole confusion that produced the bug.
	if !strings.Contains(rules, "gather:oak_log,+6") {
		t.Error("the system rules contain no worked example of the additive form.\n" +
			"\tWithout one the model has to infer when \"+\" applies from a sentence of prose.")
	}
}
