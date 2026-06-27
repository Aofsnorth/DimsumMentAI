package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// TodoStatus represents the lifecycle state of a single plan step.
type TodoStatus string

const (
	StatusPending    TodoStatus = "pending"
	StatusInProgress TodoStatus = "in_progress"
	StatusCompleted  TodoStatus = "completed"
	StatusFailed     TodoStatus = "failed"
	StatusSkipped    TodoStatus = "skipped"
)

// TodoItem is one step in a plan. Action follows the same "label:param"
// convention as <action> tags (e.g. "gather:oak_log,4").
type TodoItem struct {
	Index    int        `json:"index"`
	Action   string     `json:"action"`   // e.g. "gather:oak_log,4"
	Desc     string     `json:"desc"`     // human-readable, auto-generated if empty
	Status   TodoStatus `json:"status"`
	Note     string     `json:"note"`     // feedback / error from execution
	Started  time.Time  `json:"started"`
	Finished time.Time  `json:"finished"`
}

// TodoList is the thread-safe plan tracker. It is embedded in the Bot and
// surfaced to the LLM via the system prompt so the model always knows what
// is in progress.
type TodoList struct {
	mu      sync.RWMutex
	goal    string
	items   []TodoItem
	created time.Time
	active  bool
}

func NewTodoList() *TodoList {
	return &TodoList{}
}

// SetPlan replaces the current plan atomically.
func (tl *TodoList) SetPlan(goal string, actions []string) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.goal = goal
	tl.created = time.Now()
	tl.active = len(actions) > 0
	tl.items = make([]TodoItem, len(actions))
	for i, a := range actions {
		tl.items[i] = TodoItem{
			Index:  i,
			Action: a,
			Desc:   autoDesc(a),
			Status: StatusPending,
		}
	}
}

// ReplaceRemaining swaps out all pending/in-progress steps with new ones.
// Completed/failed/skipped steps are preserved. Used when the LLM emits
// <replan> during the agentic loop.
func (tl *TodoList) ReplaceRemaining(newActions []string) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	var kept []TodoItem
	for _, item := range tl.items {
		if item.Status == StatusCompleted || item.Status == StatusFailed || item.Status == StatusSkipped {
			kept = append(kept, item)
		}
	}
	newItems := make([]TodoItem, 0, len(kept)+len(newActions))
	newItems = append(newItems, kept...)
	for i, a := range newActions {
		newItems = append(newItems, TodoItem{
			Index:  len(kept) + i,
			Action: a,
			Desc:   autoDesc(a),
			Status: StatusPending,
		})
	}
	tl.items = newItems
	// Re-index
	for i := range tl.items {
		tl.items[i].Index = i
	}
}

func (tl *TodoList) Goal() string {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	return tl.goal
}

func (tl *TodoList) IsActive() bool {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	return tl.active
}

func (tl *TodoList) Clear() {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.active = false
	tl.goal = ""
	tl.items = nil
}

// NextPending returns the first pending step and marks it in_progress, or
// returns ok=false if no pending steps remain.
func (tl *TodoList) NextPending() (TodoItem, bool) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	for i := range tl.items {
		if tl.items[i].Status == StatusPending {
			tl.items[i].Status = StatusInProgress
			tl.items[i].Started = time.Now()
			return tl.items[i], true
		}
	}
	return TodoItem{}, false
}

func (tl *TodoList) MarkCompleted(index int, note string) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	if index >= 0 && index < len(tl.items) {
		tl.items[index].Status = StatusCompleted
		tl.items[index].Note = note
		tl.items[index].Finished = time.Now()
	}
}

func (tl *TodoList) MarkFailed(index int, note string) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	if index >= 0 && index < len(tl.items) {
		tl.items[index].Status = StatusFailed
		tl.items[index].Note = note
		tl.items[index].Finished = time.Now()
	}
}

// Progress returns (completed, total).
func (tl *TodoList) Progress() (int, int) {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	completed := 0
	for _, item := range tl.items {
		if item.Status == StatusCompleted {
			completed++
		}
	}
	return completed, len(tl.items)
}

// IsFinished returns true when no pending or in_progress steps remain.
func (tl *TodoList) IsFinished() bool {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	for _, item := range tl.items {
		if item.Status == StatusPending || item.Status == StatusInProgress {
			return false
		}
	}
	return true
}

// RenderForPrompt produces a compact text summary suitable for embedding in
// the LLM system prompt so the model is always aware of the active plan.
func (tl *TodoList) RenderForPrompt() string {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	if !tl.active || len(tl.items) == 0 {
		return ""
	}
	completed, total := 0, len(tl.items)
	for _, item := range tl.items {
		if item.Status == StatusCompleted {
			completed++
		}
	}
	var b strings.Builder
	b.WriteString("[CURRENT PLAN] Goal: ")
	b.WriteString(tl.goal)
	b.WriteString(fmt.Sprintf(" (Progress: %d/%d)\n", completed, total))
	for _, item := range tl.items {
		mark := " "
		switch item.Status {
		case StatusCompleted:
			mark = "x"
		case StatusInProgress:
			mark = ">"
		case StatusFailed:
			mark = "!"
		case StatusSkipped:
			mark = "-"
		}
		b.WriteString(fmt.Sprintf("  [%s] %s", mark, item.Action))
		if item.Desc != "" && item.Desc != item.Action {
			b.WriteString(" — ")
			b.WriteString(item.Desc)
		}
		if item.Note != "" {
			b.WriteString(" (")
			b.WriteString(item.Note)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderForChat produces a player-facing summary of the current plan.
func (tl *TodoList) RenderForChat() string {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	if !tl.active || len(tl.items) == 0 {
		return ""
	}
	completed, total := 0, len(tl.items)
	for _, item := range tl.items {
		if item.Status == StatusCompleted {
			completed++
		}
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Plan: %s (%d/%d)\n", tl.goal, completed, total))
	for _, item := range tl.items {
		mark := "[ ]"
		switch item.Status {
		case StatusCompleted:
			mark = "[x]"
		case StatusInProgress:
			mark = "[>]"
		case StatusFailed:
			mark = "[!]"
		case StatusSkipped:
			mark = "[-]"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", mark, item.Action))
	}
	return strings.TrimRight(b.String(), "\n")
}

// autoDesc generates a short human-readable description from an action string.
func autoDesc(action string) string {
	parts := strings.SplitN(action, ":", 2)
	label := parts[0]
	param := ""
	if len(parts) > 1 {
		param = parts[1]
	}
	switch strings.ToLower(label) {
	case "gather":
		return "Gather " + param
	case "mine", "automine":
		return "Mine " + param
	case "craft":
		return "Craft " + param
	case "smelt":
		return "Smelt " + param
	case "come":
		return "Walk to player"
	case "follow":
		return "Follow player"
	case "give":
		return "Give " + param
	case "drop":
		return "Drop " + param
	case "equip":
		return "Equip " + param
	case "eat":
		return "Eat " + param
	case "build":
		return "Build " + param
	case "explore":
		return "Explore for " + param + "s"
	default:
		if param != "" {
			return label + " " + param
		}
		return label
	}
}
