package ai

import (
	"strings"
	"testing"
)

func TestLookupContextWindow(t *testing.T) {
	t.Parallel()
	cases := map[string]int{
		"openai/gpt-oss-120b":                    131072,
		"nvidia/llama-3.3-nemotron-super-49b-v1": 131072,
		"gemini-2.0-flash":                       1048576,
		"gemini-1.5-pro":                         2097152,
		"claude-3-5-sonnet":                      200000,
		"some/unknown-model-xyz":                 defaultContextWindow,
	}
	for model, want := range cases {
		if got := lookupContextWindow(model); got != want {
			t.Errorf("lookupContextWindow(%q) = %d, want %d", model, got, want)
		}
	}
}

func TestContextBudgetIsQuarter(t *testing.T) {
	t.Parallel()
	nc := &NvidiaClient{model: "openai/gpt-oss-120b"}
	if got := nc.ContextBudget(); got != 131072/4 {
		t.Errorf("ContextBudget = %d, want %d", got, 131072/4)
	}
	nc.SetContextWindow(1000000)
	if got := nc.ContextBudget(); got != 250000 {
		t.Errorf("ContextBudget after override = %d, want 250000", got)
	}
}

func TestCompactToBudgetKeepsSystemAndLast(t *testing.T) {
	t.Parallel()
	nc := &NvidiaClient{model: "x"}

	sys := Message{Role: "system", Content: "system prompt"}
	var msgs []Message
	msgs = append(msgs, sys)
	for i := 0; i < 50; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, Message{Role: role, Content: strings.Repeat("word ", 200)})
	}
	last := Message{Role: "user", Content: "newest question"}
	msgs = append(msgs, last)

	// Budget comfortably holds system + last but not all 50 history messages.
	budget := estimateMessagesTokens([]Message{sys, last}) + 500
	out := nc.compactToBudget(msgs, budget)

	if estimateMessagesTokens(out) > budget {
		t.Errorf("compacted tokens %d exceeds budget %d", estimateMessagesTokens(out), budget)
	}
	if len(out) == 0 || out[0].Role != "system" {
		t.Fatalf("first message should be system, got %+v", out)
	}
	if out[len(out)-1].Content != "newest question" {
		t.Errorf("last message should be preserved, got %q", out[len(out)-1].Content)
	}
	if len(out) >= len(msgs) {
		t.Errorf("compaction should drop history: in=%d out=%d", len(msgs), len(out))
	}
}

func TestCompactToBudgetNoopUnderBudget(t *testing.T) {
	t.Parallel()
	nc := &NvidiaClient{model: "x"}
	msgs := []Message{{Role: "system", Content: "hi"}, {Role: "user", Content: "hello"}}
	out := nc.compactToBudget(msgs, 100000)
	if len(out) != len(msgs) {
		t.Errorf("expected no compaction, in=%d out=%d", len(msgs), len(out))
	}
}
