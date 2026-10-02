package trading_test

import (
	"context"
	"testing"
)

// TestTradeReportsAFailedPlacementInsteadOfSuccess. The server refuses the move
// and the emerald never moves; that is a failed trade, not a completed one.
func TestTradeReportsAFailedPlacementInsteadOfSuccess(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	bot.placeErr = errInjected

	res, ok := m.Trade(context.Background(), "bread")
	if ok {
		t.Fatal("Trade reported success though the placement was refused")
	}
	if res.Reason == "" {
		t.Error("Trade failed with an empty reason")
	}
	if got := bot.countItem("minecraft:bread"); got != 0 {
		t.Errorf("bread = %d after a refused placement, want 0", got)
	}
	if len(bot.closed) != 1 {
		t.Errorf("closed %d windows, want the one it opened even on failure", len(bot.closed))
	}
}

// TestTradeReportsAFailedTakeInsteadOfSuccess is the same on the way out.
func TestTradeReportsAFailedTakeInsteadOfSuccess(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	bot.takeErr = errInjected

	if _, ok := m.Trade(context.Background(), "bread"); ok {
		t.Fatal("Trade reported success though taking the result was refused")
	}
	if got := bot.countItem("minecraft:bread"); got != 0 {
		t.Errorf("bread = %d, want 0 — the take never landed", got)
	}
}

// TestTradeDoesNotReportSuccessWhenTheResultNeverArrives is the confirmation
// rule at its sharpest. The take request is accepted, the server goes quiet, and
// the trade did not happen. A bot that trusts the request instead of the
// inventory reports an emerald trade that never completed.
func TestTradeDoesNotReportSuccessWhenTheResultNeverArrives(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	bot.takeSwallows = true

	res, ok := m.Trade(context.Background(), "bread")
	if ok {
		t.Fatalf("Trade reported success though nothing arrived: %+v", res)
	}
	if res.Gained.Count != 0 {
		t.Errorf("Gained = %+v, want nothing: no result ever arrived", res.Gained)
	}
	if got := bot.countItem("minecraft:bread"); got != 0 {
		t.Errorf("bread = %d, want 0", got)
	}
}

// TestTradeDoesNotReportSuccessWhenTheResultPreviewNeverAppears. The inputs go
// in and the villager never produces a result; nothing was bought.
func TestTradeDoesNotReportSuccessWhenTheResultPreviewNeverAppears(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	bot.produceResult = false

	res, ok := m.Trade(context.Background(), "bread")
	if ok {
		t.Fatalf("Trade reported success though no result was ever offered: %+v", res)
	}
	if len(bot.takes) != 0 {
		t.Errorf("tried to take %d results out of a window that never had one", len(bot.takes))
	}
}

// TestTradeWaitsForAResultThatArrivesLate. A server that needs a few ticks to
// compute the preview is normal; a bot that reads once and gives up is not.
func TestTradeWaitsForAResultThatArrivesLate(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	bot.resultAfter = 3

	res, ok := m.Trade(context.Background(), "bread")
	if !ok {
		t.Fatalf("Trade gave up on a result that arrived after %d polls: %s", bot.resultAfter, res.Reason)
	}
	if got := bot.countItem("minecraft:bread"); got != 8 {
		t.Errorf("bread = %d, want 8", got)
	}
}

// TestTradeWithACancelledContextStops it trading after the caller walked away.
func TestTradeWithACancelledContextStops(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	bot.produceResult = false

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, ok := m.Trade(ctx, "bread")
	if ok {
		t.Fatal("Trade reported success on a cancelled context")
	}
	if res.Reason == "" {
		t.Error("Trade failed with an empty reason")
	}
}

// TestTradeSpendsTheExactOfferedCount. Handing over the whole emerald stack
// instead of the offered amount is how a bot buys one bread and loses fifteen
// emeralds.
func TestTradeSpendsTheExactOfferedCount(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()

	if _, ok := m.Trade(context.Background(), "bread"); !ok {
		t.Fatal("Trade failed")
	}
	if len(bot.places) != 1 {
		t.Fatalf("recorded %d placements, want 1", len(bot.places))
	}
	if got := bot.places[0].count; got != 1 {
		t.Errorf("placed %d emeralds, want exactly the 1 the offer asks for", got)
	}
}

// TestTradeAsksForNoStackIDWhenStagingIntoAnEmptySlot. destStackNetID 0 is the
// empty-slot convention; a real stack ID there names a stack the window does not
// have and the server refuses the move.
func TestTradeAsksForNoStackIDWhenStagingIntoAnEmptySlot(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	if _, ok := m.Trade(context.Background(), "bread"); !ok {
		t.Fatal("Trade failed")
	}
	if got := bot.places[0].destStack; got != 0 {
		t.Errorf("destStackNetID = %d, want 0 for an empty ingredient slot", got)
	}
}

// TestTradePassesTheResultStackIDBack is the mirror. Taking from the result
// preview has to name the stack the server put there, or the take is refused.
func TestTradePassesTheResultStackIDBack(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	if _, ok := m.Trade(context.Background(), "bread"); !ok {
		t.Fatal("Trade failed")
	}
	if len(bot.takes) != 1 {
		t.Fatalf("recorded %d takes, want 1", len(bot.takes))
	}
	if got := bot.takes[0].stackNetID; got != bot.resultStackID {
		t.Errorf("take named stack %d, want the server's %d", got, bot.resultStackID)
	}
	if got := bot.takes[0].count; got != 8 {
		t.Errorf("took %d, want the 8 the preview showed", got)
	}
}
