package planner

import (
	"sync"
	"testing"
)

func TestTodoList_SetPlan(t *testing.T) {
	t.Parallel()
	tl := NewTodoList()
	tl.SetPlan("craft table", []string{"gather:oak_log,4", "craft:oak_planks,16", "craft:crafting_table,1"})

	if !tl.IsActive() {
		t.Error("expected plan to be active")
	}
	if tl.Goal() != "craft table" {
		t.Errorf("expected goal 'craft table', got '%s'", tl.Goal())
	}
	completed, total := tl.Progress()
	if completed != 0 || total != 3 {
		t.Errorf("expected 0/3, got %d/%d", completed, total)
	}
}

func TestTodoList_NextPending(t *testing.T) {
	t.Parallel()
	tl := NewTodoList()
	tl.SetPlan("test", []string{"gather:dirt,1", "gather:dirt,2"})

	step, ok := tl.NextPending()
	if !ok {
		t.Fatal("expected a pending step")
	}
	if step.Action != "gather:dirt,1" {
		t.Errorf("expected first step, got '%s'", step.Action)
	}
	if step.Status != StatusInProgress {
		t.Errorf("expected in_progress, got '%s'", step.Status)
	}

	// Next call should return the second step, not the first.
	step2, ok := tl.NextPending()
	if !ok {
		t.Fatal("expected second pending step")
	}
	if step2.Action != "gather:dirt,2" {
		t.Errorf("expected second step, got '%s'", step2.Action)
	}

	// No more pending.
	_, ok = tl.NextPending()
	if ok {
		t.Error("expected no more pending steps")
	}
}

func TestTodoList_MarkCompleted(t *testing.T) {
	t.Parallel()
	tl := NewTodoList()
	tl.SetPlan("test", []string{"gather:dirt,1", "gather:dirt,2"})

	step, _ := tl.NextPending()
	tl.MarkCompleted(step.Index, "got 1 dirt")

	completed, total := tl.Progress()
	if completed != 1 || total != 2 {
		t.Errorf("expected 1/2, got %d/%d", completed, total)
	}
}

func TestTodoList_ReplaceRemaining(t *testing.T) {
	t.Parallel()
	tl := NewTodoList()
	tl.SetPlan("test", []string{"gather:dirt,1", "craft:stick,4", "craft:torch,1"})

	// Complete first step.
	step, _ := tl.NextPending()
	tl.MarkCompleted(step.Index, "done")

	// Replan remaining.
	tl.ReplaceRemaining([]string{"gather:stick,2", "craft:torch,2"})

	completed, total := tl.Progress()
	if completed != 1 {
		t.Errorf("expected 1 completed after replan, got %d", completed)
	}
	if total != 3 {
		t.Errorf("expected 3 total after replan (1 done + 2 new), got %d", total)
	}

	// Next pending should be the first new step.
	next, ok := tl.NextPending()
	if !ok {
		t.Fatal("expected pending step after replan")
	}
	if next.Action != "gather:stick,2" {
		t.Errorf("expected 'gather:stick,2', got '%s'", next.Action)
	}
}

func TestTodoList_IsFinished(t *testing.T) {
	t.Parallel()
	tl := NewTodoList()
	tl.SetPlan("test", []string{"gather:dirt,1"})

	if tl.IsFinished() {
		t.Error("expected not finished")
	}

	step, _ := tl.NextPending()
	tl.MarkCompleted(step.Index, "done")

	if !tl.IsFinished() {
		t.Error("expected finished after all steps completed")
	}
}

func TestTodoList_RenderForPrompt(t *testing.T) {
	t.Parallel()
	tl := NewTodoList()
	tl.SetPlan("craft table", []string{"gather:oak_log,4", "craft:oak_planks,16"})

	rendered := tl.RenderForPrompt()
	if rendered == "" {
		t.Fatal("expected non-empty render")
	}
	if !contains(rendered, "[CURRENT PLAN]") {
		t.Error("expected [CURRENT PLAN] header")
	}
	if !contains(rendered, "craft table") {
		t.Error("expected goal in render")
	}
	if !contains(rendered, "gather:oak_log,4") {
		t.Error("expected step in render")
	}
}

func TestTodoList_RenderForPrompt_Empty(t *testing.T) {
	t.Parallel()
	tl := NewTodoList()
	if tl.RenderForPrompt() != "" {
		t.Error("expected empty render for no plan")
	}
}

func TestTodoList_Clear(t *testing.T) {
	t.Parallel()
	tl := NewTodoList()
	tl.SetPlan("test", []string{"gather:dirt,1"})
	tl.Clear()

	if tl.IsActive() {
		t.Error("expected inactive after clear")
	}
	if tl.RenderForPrompt() != "" {
		t.Error("expected empty render after clear")
	}
}

func TestTodoList_Concurrent(t *testing.T) {
	t.Parallel()
	tl := NewTodoList()
	tl.SetPlan("concurrent test", []string{"a:1", "a:2", "a:3", "a:4", "a:5"})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tl.NextPending()
		}()
	}
	wg.Wait()

	// All 5 steps should be in_progress, none pending.
	_, ok := tl.NextPending()
	if ok {
		t.Error("expected no more pending after concurrent NextPending calls")
	}
}

func TestAutoDesc(t *testing.T) {
	t.Parallel()
	tests := []struct {
		action string
		expect string
	}{
		{"gather:oak_log,4", "Gather oak_log,4"},
		{"craft:crafting_table,1", "Craft crafting_table,1"},
		{"mine:stone", "Mine stone"},
		{"come", "Walk to player"},
		{"follow", "Follow player"},
		{"unknown:foo", "unknown foo"},
		{"bare", "bare"},
	}
	for _, tt := range tests {
		got := autoDesc(tt.action)
		if got != tt.expect {
			t.Errorf("autoDesc(%q) = %q, want %q", tt.action, got, tt.expect)
		}
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
