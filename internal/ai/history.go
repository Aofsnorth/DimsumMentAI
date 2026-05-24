package ai

import (
	"strings"
	"sync"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type MessageHistory struct {
	mu      sync.RWMutex
	history map[string][]Message
	maxSize int
}

func NewMessageHistory(maxSize int) *MessageHistory {
	return &MessageHistory{
		history: make(map[string][]Message),
		maxSize: maxSize,
	}
}

// GetHistory returns a copy of the history for a user
func (mh *MessageHistory) GetHistory(user string) []Message {
	mh.mu.RLock()
	defer mh.mu.RUnlock()

	hist, ok := mh.history[user]
	if !ok {
		return []Message{}
	}

	// Return a copy to avoid concurrent slice modification issues
	copied := make([]Message, len(hist))
	copy(copied, hist)
	return copied
}

// AddMessage appends a message to the user's history and caps it at maxSize
func (mh *MessageHistory) AddMessage(user, role, content string) {
	if content == "" {
		return
	}

	mh.mu.Lock()
	defer mh.mu.Unlock()

	hist := mh.history[user]
	hist = append(hist, Message{Role: role, Content: content})

	// Cap the size
	if len(hist) > mh.maxSize {
		hist = hist[len(hist)-mh.maxSize:]
	}

	mh.history[user] = hist
}

// Clear removes all history for a user
func (mh *MessageHistory) Clear(user string) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	delete(mh.history, user)
}

// FixMessages sanitizes a sequence of messages for NVIDIA's strict API requirements:
// 1. Merges consecutive messages with the same role.
// 2. Ensures the sequence starts with "user" (after the optional "system" prompt).
// 3. Filters out any empty messages.
func FixMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return []Message{}
	}

	var fixed []Message
	var systemParts []string

	// 1. Extract and merge system messages
	for _, m := range messages {
		if m.Role == "system" {
			trimmed := strings.TrimSpace(m.Content)
			if trimmed != "" {
				systemParts = append(systemParts, trimmed)
			}
		}
	}

	if len(systemParts) > 0 {
		fixed = append(fixed, Message{
			Role:    "system",
			Content: strings.Join(systemParts, "\n"),
		})
	}

	// 2. Filter non-system messages and merge consecutive roles
	var lastRole string
	for _, m := range messages {
		if m.Role == "system" {
			continue
		}

		trimmed := strings.TrimSpace(m.Content)
		if trimmed == "" {
			continue // Skip empty messages to prevent API 400 Bad Request
		}

		if m.Role == lastRole && len(fixed) > 0 {
			fixed[len(fixed)-1].Content += "\n" + trimmed
		} else {
			fixed = append(fixed, Message{
				Role:    m.Role,
				Content: trimmed,
			})
			lastRole = m.Role
		}
	}

	// 3. Ensure the first message after system is a "user" message.
	// If it starts with system (index 0) and the next is assistant (index 1), remove assistant.
	startIndex := 0
	if len(fixed) > 0 && fixed[0].Role == "system" {
		startIndex = 1
	}

	for len(fixed) > startIndex && fixed[startIndex].Role == "assistant" {
		fixed = append(fixed[:startIndex], fixed[startIndex+1:]...)
	}

	return fixed
}
