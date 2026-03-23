package llm

import (
	"context"
)

// Message represents a chat message in a conversation.
type Message struct {
	Role       string     // "system", "user", "assistant", "tool"
	Content    string
	Name       string
	ToolCalls  []ToolCall
	ToolCallID string
}

// ToolCall represents a tool call requested by the LLM.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// Tool represents a tool that can be called by the LLM.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON schema as map
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	ToolCallID string
	Output     string
}

// LLMClient is the interface for LLM providers.
type LLMClient interface {
	Chat(ctx context.Context, messages []Message, tools []Tool, model string) (*Message, []ToolCall, error)
	ChatWithRetry(ctx context.Context, messages []Message, tools []Tool, model string) (*Message, []ToolCall, error)
}
