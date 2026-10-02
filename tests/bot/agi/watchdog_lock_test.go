package agi_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
)

// TestTheWatchdogFinishesAPass is the regression for the self-deadlock in
// watch(): it took the runner lock across the whole reading and then took it
// again to count the repeat. sync.Mutex is not reentrant, so the second Lock
// blocked forever — the watchdog never completed a pass and the AGI loop wedged
// on its first tick. The symptom in a live session is deceptive: chat still
// answers and the bot still walks, because those run on other goroutines, while
// nothing the loop itself decides ever happens again.
//
// Running it on a goroutine with a timeout keeps a future recurrence from
// hanging the whole package for the test timeout with a bare stack dump that
// names no failing test.
func TestTheWatchdogFinishesAPass(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})
	snap := agi.Snapshot{HP: 20, Now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC), Coords: "X:10 Y:64 Z:0"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 3; i++ {
			r.Watch(snap)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch() did not return; the runner lock is taken twice in one pass")
	}
}
