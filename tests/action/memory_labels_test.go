package action_test

import (
	"testing"

	"bedrock-ai/internal/bot/action"
)

func TestSupportedLabels_ContainsMinePalParity(t *testing.T) {
	t.Parallel()
	labels := action.SupportedLabels()
	parity := []string{
		"remember", "recall", "memories", "forget",
		"sethome", "home", "analyze", "move", "look",
	}
	for _, label := range parity {
		if _, ok := labels[label]; !ok {
			t.Errorf("SupportedLabels missing MinePal-parity action %q", label)
		}
	}
}
