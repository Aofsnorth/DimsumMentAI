package ai

import (
	"strings"
	"testing"
)

func TestSystemPromptPreventsPrematureSuccessClaims(t *testing.T) {
	client := NewNvidiaClient("", "")
	prompt := client.BuildSystemPrompt("Luna", "X:0 Y:64 Z:0", "X:1 Y:64 Z:1", "nothing", "Inventory kosong")

	if !strings.Contains(prompt, "Do not claim an action is completed") {
		t.Fatal("system prompt does not prevent premature action success claims")
	}
}
