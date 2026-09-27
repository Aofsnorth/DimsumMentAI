package debuglog_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"bedrock-ai/internal/debuglog"
)

// TestLogIsBoundedWhenCalledInALoop is the regression this package exists for.
//
// Log used to open, write and close the log file on every call while holding a
// global mutex, and the packet read loop calls it once per packet once the
// server is laggy. That is a feedback loop: more lag means more file I/O means
// more lag, until the server drops the client for being unresponsive — which
// looks exactly like "the bot appears for a second and then leaves".
func TestLogIsBoundedWhenCalledInALoop(t *testing.T) {
	restore := chdir(t, t.TempDir())
	defer restore()

	debuglog.SetEnabled(true)
	defer debuglog.SetEnabled(false)

	// Far more calls than the write interval allows. The point is that the hot
	// caller is cheap, not that every line lands.
	for i := 0; i < 5000; i++ {
		debuglog.Log("T", "test.go:hot_loop", "spam", map[string]any{"i": i})
	}

	data, err := os.ReadFile(filepath.Join("logs", "debug-090ce4.log"))
	if err != nil {
		t.Fatalf("log file was never created: %v", err)
	}
	lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1
	// One write per 250ms interval; a tight loop must collapse to a handful.
	if lines > 100 {
		t.Errorf("hot loop wrote %d lines; writes are not rate limited", lines)
	}
}

// TestLogIsConcurrencySafe covers the other half of the old bug: the lock was
// held across file I/O, so a slow disk blocked every other goroutine.
func TestLogIsConcurrencySafe(t *testing.T) {
	restore := chdir(t, t.TempDir())
	defer restore()

	debuglog.SetEnabled(true)
	defer debuglog.SetEnabled(false)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				debuglog.Log("T", "test.go:concurrent", "line", map[string]any{"n": n})
			}
		}(i)
	}
	wg.Wait()

	if _, err := os.Stat(filepath.Join("logs", "debug-090ce4.log")); err != nil {
		t.Errorf("log file missing after concurrent writes: %v", err)
	}
}

// chdir moves into dir and returns a func that restores the old directory.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return func() { _ = os.Chdir(prev) }
}
