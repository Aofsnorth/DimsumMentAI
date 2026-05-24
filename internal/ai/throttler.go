package ai

import (
	"strings"
	"sync"
	"time"
)

type HistoryEntry struct {
	Text      string
	Timestamp time.Time
}

type MessageThrottler struct {
	mu                   sync.Mutex
	duplicateWindow      time.Duration
	rateLimitWindow      time.Duration
	maxMessagesPerWindow int
	history              []HistoryEntry
}

func NewMessageThrottler(duplicateWindow, rateLimitWindow time.Duration, maxMessagesPerWindow int) *MessageThrottler {
	return &MessageThrottler{
		duplicateWindow:      duplicateWindow,
		rateLimitWindow:      rateLimitWindow,
		maxMessagesPerWindow: maxMessagesPerWindow,
		history:              []HistoryEntry{},
	}
}

// DefaultThrottler returns a throttler configured with standard values (5s duplicate window, 10s rate limit window for max 100 messages)
func DefaultThrottler() *MessageThrottler {
	return NewMessageThrottler(5*time.Second, 10*time.Second, 100)
}

// IsDuplicate checks if the exact same message was processed within the duplicate window
func (mt *MessageThrottler) IsDuplicate(message string) bool {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	mt.cleanup()

	now := time.Now()
	cleanMsg := strings.TrimSpace(strings.ToLower(message))

	for _, entry := range mt.history {
		if now.Sub(entry.Timestamp) < mt.duplicateWindow {
			if strings.TrimSpace(strings.ToLower(entry.Text)) == cleanMsg {
				return true
			}
		}
	}

	return false
}

// IsRateLimited checks if the message count in the rate limit window exceeds the maximum allowed
func (mt *MessageThrottler) IsRateLimited() bool {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	mt.cleanup()

	now := time.Now()
	count := 0

	for _, entry := range mt.history {
		if now.Sub(entry.Timestamp) < mt.rateLimitWindow {
			count++
		}
	}

	return count >= mt.maxMessagesPerWindow
}

// Add adds a message to the history logs
func (mt *MessageThrottler) Add(message string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	mt.history = append(mt.history, HistoryEntry{
		Text:      message,
		Timestamp: time.Now(),
	})
	mt.cleanup()
}

// Filter checks if a message should be allowed (neither duplicate nor rate-limited)
func (mt *MessageThrottler) Filter(message string) (bool, string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	mt.cleanup()

	now := time.Now()
	cleanMsg := strings.TrimSpace(strings.ToLower(message))

	// Check duplicates (using aggressive 10s check for repetitive bot replies)
	for _, entry := range mt.history {
		if now.Sub(entry.Timestamp) < 10*time.Second {
			if strings.TrimSpace(strings.ToLower(entry.Text)) == cleanMsg {
				return false, ""
			}
		}
	}

	// Check rate limit
	count := 0
	for _, entry := range mt.history {
		if now.Sub(entry.Timestamp) < mt.rateLimitWindow {
			count++
		}
	}
	if count >= mt.maxMessagesPerWindow {
		return false, ""
	}

	// Allowed! Append to history
	mt.history = append(mt.history, HistoryEntry{
		Text:      message,
		Timestamp: now,
	})

	return true, message
}

// cleanup removes history entries older than the longest window (must be called inside locked mutex)
func (mt *MessageThrottler) cleanup() {
	now := time.Now()
	maxWindow := mt.duplicateWindow
	if mt.rateLimitWindow > maxWindow {
		maxWindow = mt.rateLimitWindow
	}
	if 10*time.Second > maxWindow {
		maxWindow = 10 * time.Second
	}

	validIndex := 0
	for i, entry := range mt.history {
		if now.Sub(entry.Timestamp) < maxWindow {
			validIndex = i
			break
		}
		if i == len(mt.history)-1 {
			validIndex = len(mt.history)
		}
	}

	if validIndex > 0 {
		if validIndex >= len(mt.history) {
			mt.history = []HistoryEntry{}
		} else {
			mt.history = mt.history[validIndex:]
		}
	}
}
