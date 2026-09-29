package agi_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft"
)

// Regression: a nearby player must not make perception re-enter the bot mutex.
func TestObserveWithNearbyPlayerReturns(t *testing.T) {
	t.Parallel()
	b, err := bot.New(
		bot.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		bot.WithDialer(func() (*minecraft.Conn, error) { return nil, nil }),
		bot.WithRegistry(handler.NewRegistry()),
		bot.WithEventBus(event.NewBus()),
	)
	if err != nil {
		t.Fatal(err)
	}
	b.PlayerEntityIDs["NearbyPlayer"] = 42
	b.PlayerPositions[42] = b.Pos.Add(mgl32.Vec3{0, 0, 2})
	b.LastChatPartner = "NearbyPlayer"
	r := agi.New(b, agi.Config{Enabled: true, Vision: true, VisionRadius: 16, EyeHeight: bot.PlayerEyeHeight})
	done := make(chan agi.Snapshot, 1)
	go func() { done <- r.Observe() }()
	select {
	case snap := <-done:
		if len(snap.Nearby) != 1 || snap.Nearby[0].Name != "NearbyPlayer" || !snap.Nearby[0].SpeakingTo {
			t.Fatalf("nearby player lost from snapshot: %+v", snap.Nearby)
		}
		if !b.Mu.TryLock() {
			t.Fatal("Observe left the bot mutex locked")
		}
		b.Mu.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("Observe deadlocked with a nearby player; visibility must run outside b.Mu")
	}
}

// TestObserveReturnsInsteadOfWedgingTheBrain is the regression guard for a bug
// that made the entire autonomy layer look switched on while doing nothing.
//
// Observe held b.Mu and then called b.IsBusy(), which locks b.Mu again.
// sync.Mutex is not reentrant, so the first AGI tick blocked forever, on the
// first call, with nothing in the log to say why. The bot still joined, still
// answered chat — that path never goes through Observe — and simply stood still
// until someone typed a command. Every other symptom was healthy: "AGI loop
// started", "Jev System One active", enabled=true in the config.
//
// That shape is why it survived: the failure was invisible in the logs and only
// observable as a bot that refused to act on its own. This test asserts the only
// property that distinguishes the two cases, which is that Observe comes back.
func TestObserveReturnsInsteadOfWedgingTheBrain(t *testing.T) {
	t.Parallel()

	// None of these are used by Observe; they are only what bot.New insists on
	// having. A real dialer is never invoked because nothing here connects.
	b, err := bot.New(
		bot.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		bot.WithDialer(func() (*minecraft.Conn, error) { return nil, nil }),
		bot.WithRegistry(handler.NewRegistry()),
		bot.WithEventBus(event.NewBus()),
	)
	if err != nil {
		t.Fatalf("build a bot: %v", err)
	}
	// Enabled has to be true or New() returns nil and there is nothing to test.
	r := agi.New(b, agi.Config{
		Enabled: true,
		TickInterval: agi.TimeRange{
			Min: 30 * time.Second,
			Max: 30 * time.Second,
		},
		Wander:            true,
		Vision:            true,
		MobScanDistance:   32,
		BlockScanDistance: 12,
		NearbyRadius:      30,
	})
	if r == nil {
		t.Fatal("agi.New returned nil for an enabled config")
	}

	// Observed in a goroutine with a deadline, because a deadlock inside the
	// call is exactly the thing a normal test would hang on forever.
	type result struct {
		ok bool
	}
	done := make(chan result, 1)
	go func() {
		r.Observe()
		done <- result{ok: true}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Observe did not return within 5s; the brain has deadlocked on b.Mu")
	}
}
