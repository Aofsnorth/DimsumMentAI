package bot_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/gathering"
)

// IsBusy is the answer the whole autonomy layer asks before it starts anything,
// so it has to mean "the body is spoken for" rather than "the body is currently
// translating". Those are not the same thing, and the difference is a bot that
// walks off in the middle of a swing.

// newTestBot builds a bot that is standing still, doing nothing, with a gatherer
// attached — the state a real bot is in between two swings of an axe.
func newTestBot() *bot.Bot {
	return &bot.Bot{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		MovementState: "idle",
		Gatherer:      gathering.NewResourceGatherer(nil, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
}

// TestAChoppingBotIsBusyWhileStandingStill is the regression. Chopping spends
// most of its time perfectly still: the bot stands in front of the trunk and
// swings. MovementState says "idle" for the whole of it, so the old definition
// of busy reported a free body and the autonomy loop started an exploration —
// which is a walking-away-from-the-tree underneath a swinging arm.
func TestAChoppingBotIsBusyWhileStandingStill(t *testing.T) {
	t.Parallel()

	b := newTestBot()
	if b.IsBusy() {
		t.Fatal("a bot that has just joined and done nothing reports itself busy")
	}

	b.Gatherer.SetGathering(true)
	if !b.IsBusy() {
		t.Error("a bot mid-chop reports itself free; the exploration loop will walk it off the tree")
	}

	b.Gatherer.SetGathering(false)
	if b.IsBusy() {
		t.Error("the bot is still busy after the gather finished")
	}
}

// TestWalkingStillCountsAsBusy guards the other half. A walk with no gatherer
// behind it is still a commitment, and tightening the definition must not have
// loosened it.
func TestWalkingStillCountsAsBusy(t *testing.T) {
	t.Parallel()

	b := newTestBot()
	b.MovementState = "walk_to"
	if !b.IsBusy() {
		t.Error("a walking bot reports itself free")
	}
}

// TestGatheringIsNotAnActionThatEndsByItself is about ordering rather than
// state. IsBusy is read while the bot lock may be held by a caller, and the
// gatherer has its own lock; reading it underneath b.Mu would be a deadlock
// waiting for a rejoin to expose it. Every path out of the bot has to be
// survivable, so a nil gatherer is a bot with no gatherer, not a panic.
func TestGatheringIsNotAnActionThatEndsByItself(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MovementState: "idle"}
	if b.IsBusy() {
		t.Error("a bot with no gatherer and no movement is not busy")
	}
}
