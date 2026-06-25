package ai

import (
	"testing"
)

func TestMessageHistory_EmptyUser(t *testing.T) {
	t.Parallel()
	h := NewMessageHistory(10)
	got := h.GetHistory("nobody")
	if len(got) != 0 {
		t.Errorf("GetHistory for unknown user = %v, want empty", got)
	}
}

func TestMessageHistory_AddAndRetrieve(t *testing.T) {
	t.Parallel()
	h := NewMessageHistory(10)
	h.AddMessage("Alice", "user", "hello")
	h.AddMessage("Alice", "assistant", "hi there")

	got := h.GetHistory("Alice")
	if len(got) != 2 {
		t.Fatalf("GetHistory len = %d, want 2", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "hello" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "hi there" {
		t.Errorf("got[1] = %+v", got[1])
	}
}

func TestMessageHistory_CapsAtMaxSize(t *testing.T) {
	t.Parallel()
	h := NewMessageHistory(3)
	h.AddMessage("Alice", "user", "m1")
	h.AddMessage("Alice", "assistant", "m2")
	h.AddMessage("Alice", "user", "m3")
	h.AddMessage("Alice", "assistant", "m4")
	h.AddMessage("Alice", "user", "m5")

	got := h.GetHistory("Alice")
	if len(got) != 3 {
		t.Fatalf("GetHistory len = %d, want 3 (capped)", len(got))
	}
	if got[0].Content != "m3" {
		t.Errorf("oldest after cap = %q, want %q", got[0].Content, "m3")
	}
	if got[2].Content != "m5" {
		t.Errorf("newest = %q, want %q", got[2].Content, "m5")
	}
}

func TestMessageHistory_EmptyContentIgnored(t *testing.T) {
	t.Parallel()
	h := NewMessageHistory(10)
	h.AddMessage("Alice", "user", "")

	got := h.GetHistory("Alice")
	if len(got) != 0 {
		t.Errorf("GetHistory len = %d, want 0 (empty content ignored)", len(got))
	}
}

func TestMessageHistory_WhitespaceContentStored(t *testing.T) {
	t.Parallel()
	// AddMessage only skips truly empty content (""); whitespace-only strings
	// are stored as-is. This test documents the current behavior.
	h := NewMessageHistory(10)
	h.AddMessage("Alice", "user", "   ")

	got := h.GetHistory("Alice")
	if len(got) != 1 {
		t.Errorf("GetHistory len = %d, want 1 (whitespace content is stored)", len(got))
	}
}

func TestMessageHistory_Clear(t *testing.T) {
	t.Parallel()
	h := NewMessageHistory(10)
	h.AddMessage("Alice", "user", "hello")
	h.Clear("Alice")

	got := h.GetHistory("Alice")
	if len(got) != 0 {
		t.Errorf("GetHistory after clear = %v, want empty", got)
	}
}

func TestMessageHistory_GetHistoryReturnsCopy(t *testing.T) {
	t.Parallel()
	h := NewMessageHistory(10)
	h.AddMessage("Alice", "user", "hello")

	got := h.GetHistory("Alice")
	got[0].Content = "mutated"

	again := h.GetHistory("Alice")
	if again[0].Content != "hello" {
		t.Errorf("GetHistory did not return a copy; internal state was mutated to %q", again[0].Content)
	}
}

func TestMessageHistory_IndependentUsers(t *testing.T) {
	t.Parallel()
	h := NewMessageHistory(10)
	h.AddMessage("Alice", "user", "alice-msg")
	h.AddMessage("Bob", "user", "bob-msg")

	if len(h.GetHistory("Alice")) != 1 {
		t.Error("Alice history should have 1 message")
	}
	if len(h.GetHistory("Bob")) != 1 {
		t.Error("Bob history should have 1 message")
	}
}

func TestFixMessages_Empty(t *testing.T) {
	t.Parallel()
	got := FixMessages(nil)
	if len(got) != 0 {
		t.Errorf("FixMessages(nil) = %v, want empty", got)
	}
}

func TestFixMessages_MergesConsecutiveSameRole(t *testing.T) {
	t.Parallel()
	input := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "msg1"},
		{Role: "user", Content: "msg2"},
		{Role: "assistant", Content: "reply1"},
		{Role: "assistant", Content: "reply2"},
	}
	got := FixMessages(input)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (system + merged user + merged assistant)", len(got))
	}
	if got[0].Role != "system" {
		t.Errorf("got[0].Role = %q, want system", got[0].Role)
	}
	if got[1].Role != "user" {
		t.Errorf("got[1].Role = %q, want user", got[1].Role)
	}
	if !contains(got[1].Content, "msg1") || !contains(got[1].Content, "msg2") {
		t.Errorf("merged user content = %q, should contain both messages", got[1].Content)
	}
}

func TestFixMessages_FiltersEmptyMessages(t *testing.T) {
	t.Parallel()
	input := []Message{
		{Role: "user", Content: ""},
		{Role: "user", Content: "   "},
		{Role: "user", Content: "real msg"},
	}
	got := FixMessages(input)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (empty messages filtered)", len(got))
	}
	if got[0].Content != "real msg" {
		t.Errorf("got[0].Content = %q", got[0].Content)
	}
}

func TestFixMessages_RemovesLeadingAssistant(t *testing.T) {
	t.Parallel()
	input := []Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "stale assistant"},
		{Role: "user", Content: "real user msg"},
	}
	got := FixMessages(input)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (leading assistant removed)", len(got))
	}
	if got[1].Role != "user" {
		t.Errorf("got[1].Role = %q, want user", got[1].Role)
	}
}

func TestFixMessages_MergesMultipleSystemMessages(t *testing.T) {
	t.Parallel()
	input := []Message{
		{Role: "system", Content: "rule1"},
		{Role: "system", Content: "rule2"},
		{Role: "user", Content: "hello"},
	}
	got := FixMessages(input)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (merged system + user)", len(got))
	}
	if got[0].Role != "system" {
		t.Errorf("got[0].Role = %q, want system", got[0].Role)
	}
	if !contains(got[0].Content, "rule1") || !contains(got[0].Content, "rule2") {
		t.Errorf("merged system = %q, should contain both rules", got[0].Content)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
