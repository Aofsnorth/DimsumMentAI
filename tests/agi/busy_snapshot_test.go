package agi_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/bot/gathering"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"

	"github.com/sandertv/gophertunnel/minecraft"
)

// The snapshot's Busy field is the one bit the natural loop trusts most: it is
// what tells the loop not to start an exploration, not to pick an activity, and
// not to idle, while something else owns the body. It has to be the same
// question b.IsBusy answers, read in the same place.

// newObservingBot builds a bot that Observe can run against.
func newObservingBot(t *testing.T) *bot.Bot {
	t.Helper()
	b, err := bot.New(
		bot.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		bot.WithDialer(func() (*minecraft.Conn, error) { return nil, nil }),
		bot.WithRegistry(handler.NewRegistry()),
		bot.WithEventBus(event.NewBus()),
	)
	if err != nil {
		t.Fatalf("build a bot: %v", err)
	}
	return b
}

// TestAChoppingBotIsBusyInTheSnapshot is the end-to-end version of the chop
// regression. It is here, and not only in the bot package, because Observe used
// to recompute the answer instead of asking for it — so the bot package's
// definition could have been correct all along while the loop still saw a free
// body and wandered off. A test on IsBusy alone would have passed against that.
func TestAChoppingBotIsBusyInTheSnapshot(t *testing.T) {
	t.Parallel()

	b := newObservingBot(t)
	// A live session builds the gatherer from the connection's game data. Here
	// there is no connection, so it is attached directly: the first Observe
	// below therefore also covers the nil-gatherer path a bot has before its
	// subsystems exist.
	b.Gatherer = gathering.NewResourceGatherer(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := agi.New(b, agi.Config{Enabled: true, Wander: true})

	if snap := r.Observe(); snap.Busy {
		t.Fatal("a freshly joined bot reports itself busy")
	}

	b.Gatherer.SetGathering(true)
	if snap := r.Observe(); !snap.Busy {
		t.Error("the snapshot says a chopping bot is free; the natural loop will start wandering mid-swing")
	}

	b.Gatherer.SetGathering(false)
	if snap := r.Observe(); snap.Busy {
		t.Error("the bot is still busy after the gather finished")
	}
}
