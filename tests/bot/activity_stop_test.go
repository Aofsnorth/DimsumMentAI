package bot_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
)

// A "stop" the player can say has to reach work already in flight. The gather
// loop is driven by a context, so if that context cannot be cancelled the bot
// answers "okay, stopping" and then keeps re-navigating to the next tree —
// which is exactly what the log showed: "Yaudah deh, aku stop dulu ya."
// followed by more A* pathfinding and another attempt to break a block.
func TestStopCancelsTheRunningActivity(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := b.ActivityContext()

	select {
	case <-ctx.Done():
		t.Fatal("a fresh activity is already cancelled")
	default:
	}

	b.Stop()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Error("Stop did not cancel the running activity.\n" +
			"Long jobs handed this context keep going: the bot keeps chopping after being told to stop.")
	}
}

// A context that stays cancelled would make the bot refuse to do anything the
// player asks for next. Stop opens a fresh one.
func TestWorkStartedAfterAStopIsNotBornCancelled(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	b.Stop()

	ctx := b.ActivityContext()
	select {
	case <-ctx.Done():
		t.Error("a job started after the stop is already cancelled: the bot can never work again")
	default:
	}
}

// Stopping twice must not panic on an already-closed cancel channel, which is
// what happens when a stop request and a stop action land together.
func TestStopIsSafeToRepeat(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	b.Stop()
	b.Stop()
	b.Stop()

	if ctx := b.ActivityContext(); ctx.Err() != nil {
		t.Errorf("after repeated stops the next activity is cancelled: %v", ctx.Err())
	}
}

// The context handed to a job must stay the same one across calls, otherwise a
// job that fetched it twice would be watching a context nobody cancels.
func TestActivityContextIsStableUntilStopped(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	first := b.ActivityContext()
	second := b.ActivityContext()

	if first != second {
		t.Error("ActivityContext returned a different context on the second call.\n" +
			"Work that captured the first one is no longer bounded by the bot's activity.")
	}
}
