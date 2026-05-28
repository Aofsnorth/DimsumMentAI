package ai

import (
	"strings"
	"sync"
	"time"
)

type HistoryEntry struct {
	Source    string
	Text      string
	Timestamp time.Time
}

// MessageThrottler filters incoming chat to prevent duplicate/floods.
// Duplicate detection is per-source (so player A's message does not block an
// identical message from player B). Rate limiting is global.
type MessageThrottler struct {
	mu                   sync.Mutex
	duplicateWindow      time.Duration
	rateLimitWindow      time.Duration
	maxMessagesPerWindow int
	history              []HistoryEntry
}

func NewMessageThrottler(duplicateWindow, rateLimitWindow time.Duration, maxMessagesPerWindow int) *MessageThrottler {
	if duplicateWindow <= 0 {
		duplicateWindow = 3 * time.Second
	}
	if rateLimitWindow <= 0 {
		rateLimitWindow = 10 * time.Second
	}
	if maxMessagesPerWindow <= 0 {
		maxMessagesPerWindow = 100
	}
	return &MessageThrottler{
		duplicateWindow:      duplicateWindow,
		rateLimitWindow:      rateLimitWindow,
		maxMessagesPerWindow: maxMessagesPerWindow,
		history:              []HistoryEntry{},
	}
}

// DefaultThrottler returns a throttler configured with sane defaults.
func DefaultThrottler() *MessageThrottler {
	return NewMessageThrottler(3*time.Second, 10*time.Second, 100)
}

// Filter checks if a message from source should be allowed. When allowed, the
// message is recorded in history. Callers MUST call Rollback(source, message)
// if the work the message triggered fails before completing, otherwise the
// failed attempt will block legitimate retries.
func (mt *MessageThrottler) Filter(source, message string) (bool, string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	mt.cleanup()

	now := time.Now()
	cleanSource := strings.ToLower(strings.TrimSpace(source))
	cleanMsg := strings.TrimSpace(strings.ToLower(message))

	for _, entry := range mt.history {
		if now.Sub(entry.Timestamp) >= mt.duplicateWindow {
			continue
		}
		if strings.ToLower(strings.TrimSpace(entry.Source)) != cleanSource {
			continue
		}
		if strings.TrimSpace(strings.ToLower(entry.Text)) == cleanMsg {
			return false, ""
		}
	}

	count := 0
	for _, entry := range mt.history {
		if now.Sub(entry.Timestamp) < mt.rateLimitWindow {
			count++
		}
	}
	if count >= mt.maxMessagesPerWindow {
		return false, ""
	}

	mt.history = append(mt.history, HistoryEntry{
		Source:    source,
		Text:      message,
		Timestamp: now,
	})

	return true, message
}

// Rollback removes the most recent matching (source, message) entry. Use after
// downstream work fails so the same player can immediately retry.
func (mt *MessageThrottler) Rollback(source, message string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	cleanSource := strings.ToLower(strings.TrimSpace(source))
	cleanMsg := strings.TrimSpace(strings.ToLower(message))

	for i := len(mt.history) - 1; i >= 0; i-- {
		entry := mt.history[i]
		if strings.ToLower(strings.TrimSpace(entry.Source)) != cleanSource {
			continue
		}
		if strings.TrimSpace(strings.ToLower(entry.Text)) != cleanMsg {
			continue
		}
		mt.history = append(mt.history[:i], mt.history[i+1:]...)
		return
	}
}

// cleanup removes history entries older than the longest tracking window.
// Caller must hold mt.mu.
func (mt *MessageThrottler) cleanup() {
	now := time.Now()
	maxWindow := mt.duplicateWindow
	if mt.rateLimitWindow > maxWindow {
		maxWindow = mt.rateLimitWindow
	}

	filtered := mt.history[:0]
	for _, entry := range mt.history {
		if now.Sub(entry.Timestamp) < maxWindow {
			filtered = append(filtered, entry)
		}
	}
	mt.history = filtered
}
