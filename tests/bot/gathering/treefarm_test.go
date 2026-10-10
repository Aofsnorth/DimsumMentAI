package gathering_test

import (
	"testing"

	"bedrock-ai/internal/bot/gathering"
)

// TreeFarmCount is the policy the narrator quotes; a run that felled nothing
// is a failure with the reason, anything else is a success.
func TestTreeFarmCount(t *testing.T) {
	t.Parallel()

	success, summary := gathering.TreeFarmCount(0, 0)
	if success || summary == "" {
		t.Fatalf("an empty run must fail with a reason: success=%v summary=%q", success, summary)
	}

	success, summary = gathering.TreeFarmCount(3, 14)
	if !success || summary == "" {
		t.Fatalf("a real run must succeed with a summary: success=%v summary=%q", success, summary)
	}
}
