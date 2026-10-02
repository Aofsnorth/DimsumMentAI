package bot_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
)

// Fast regression: missing error text must not turn a failure into success.
func TestStatusPromptHonorsSuccessFlag(t *testing.T) {
	t.Parallel()
	prompt := bot.BuildStatusPrompt(event.ActionStatus{Action: "craft", Count: 3})
	if !strings.Contains(prompt, "failed") || strings.Contains(prompt, "succeeded") {
		t.Fatalf("false success: %s", prompt)
	}
}

func TestExploreStatusIsNotLoot(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"explore", "exploredir"} {
		prompt := bot.BuildStatusPrompt(event.ActionStatus{Action: action, Item: "random", Count: 5, Success: true})
		if !strings.Contains(prompt, "5 navigation waypoints") || strings.Contains(prompt, "Item:") {
			t.Fatalf("misleading exploration result: %s", prompt)
		}
	}
}
