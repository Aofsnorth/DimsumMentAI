package ai

import (
	"testing"
	"time"
)

func TestMessageThrottler_AllowsFirstMessage(t *testing.T) {
	t.Parallel()
	mt := NewMessageThrottler(3*time.Second, 10*time.Second, 100)
	ok, _ := mt.Filter("Alice", "hello")
	if !ok {
		t.Error("first message should be allowed")
	}
}

func TestMessageThrottler_BlocksDuplicateWithinWindow(t *testing.T) {
	t.Parallel()
	mt := NewMessageThrottler(3*time.Second, 10*time.Second, 100)
	mt.Filter("Alice", "hello")
	ok, _ := mt.Filter("Alice", "hello")
	if ok {
		t.Error("duplicate message within window should be blocked")
	}
}

func TestMessageThrottler_AllowsSameMessageDifferentSource(t *testing.T) {
	t.Parallel()
	mt := NewMessageThrottler(3*time.Second, 10*time.Second, 100)
	mt.Filter("Alice", "hello")
	ok, _ := mt.Filter("Bob", "hello")
	if !ok {
		t.Error("same message from different source should be allowed")
	}
}

func TestMessageThrottler_CaseInsensitiveDuplicate(t *testing.T) {
	t.Parallel()
	mt := NewMessageThrottler(3*time.Second, 10*time.Second, 100)
	mt.Filter("Alice", "Hello World")
	ok, _ := mt.Filter("Alice", "hello world")
	if ok {
		t.Error("case-insensitive duplicate should be blocked")
	}
}

func TestMessageThrottler_RateLimit(t *testing.T) {
	t.Parallel()
	mt := NewMessageThrottler(1*time.Second, 10*time.Second, 3)
	mt.Filter("A", "1")
	mt.Filter("B", "2")
	mt.Filter("C", "3")
	ok, _ := mt.Filter("D", "4")
	if ok {
		t.Error("message exceeding rate limit should be blocked")
	}
}

func TestMessageThrottler_RollbackAllowsRetry(t *testing.T) {
	t.Parallel()
	mt := NewMessageThrottler(3*time.Second, 10*time.Second, 100)
	mt.Filter("Alice", "hello")
	mt.Rollback("Alice", "hello")
	ok, _ := mt.Filter("Alice", "hello")
	if !ok {
		t.Error("after rollback, same message should be allowed again")
	}
}

func TestMessageThrottler_RollbackNoMatch(t *testing.T) {
	t.Parallel()
	mt := NewMessageThrottler(3*time.Second, 10*time.Second, 100)
	// Should not panic when rolling back a non-existent entry.
	mt.Rollback("Nobody", "nothing")
}

func TestMessageThrottler_DefaultsWhenZero(t *testing.T) {
	t.Parallel()
	mt := NewMessageThrottler(0, 0, 0)
	ok, _ := mt.Filter("Alice", "test")
	if !ok {
		t.Error("message with zero-config defaults should be allowed")
	}
}

func TestDefaultThrottler(t *testing.T) {
	t.Parallel()
	mt := DefaultThrottler()
	if mt == nil {
		t.Fatal("DefaultThrottler returned nil")
	}
	ok, _ := mt.Filter("Alice", "test")
	if !ok {
		t.Error("default throttler should allow first message")
	}
}

func TestMessageThrottler_TrimWhitespaceDuplicate(t *testing.T) {
	t.Parallel()
	mt := NewMessageThrottler(3*time.Second, 10*time.Second, 100)
	mt.Filter("Alice", "  hello  ")
	ok, _ := mt.Filter("Alice", "hello")
	if ok {
		t.Error("whitespace-trimmed duplicate should be blocked")
	}
}
