package trading_test

import (
	"context"
	"testing"

	"bedrock-ai/internal/bot/inventory/trading"

	"github.com/go-gl/mathgl/mgl32"
)

// TestOpenTradeWindowReportsWhatTheServerSent is 7.1 on its own: open the UI
// and hand back the real offers, with nothing invented.
func TestOpenTradeWindowReportsWhatTheServerSent(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	tw, windowID, err := m.OpenTradeWindow(context.Background())
	if err != nil {
		t.Fatalf("OpenTradeWindow: %v", err)
	}
	if windowID != bot.openWindowID {
		t.Errorf("windowID = %d, want the server-assigned %d", windowID, bot.openWindowID)
	}
	if tw.TradeTier != 1 {
		t.Errorf("TradeTier = %d, want 1", tw.TradeTier)
	}
	if len(tw.Offers) != 1 {
		t.Fatalf("Offers = %d, want the one the server sent: %+v", len(tw.Offers), tw.Offers)
	}
	if tw.Offers[0].Output.Name != "minecraft:bread" {
		t.Errorf("offer output = %q, want minecraft:bread", tw.Offers[0].Output.Name)
	}
	if len(bot.closed) != 1 {
		t.Errorf("OpenTradeWindow closed %d windows, want it to leave the trade UI open for the caller", len(bot.closed))
	}
}

// TestOpenTradeWindowClosesWhatItOpenedOnFailure is the inverse: a window that
// was opened gets closed even when there is nothing to read, so the next open
// is not desynced.
func TestOpenTradeWindowClosesWhatItOpenedOnFailure(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addVillager(testVillagerID, "minecraft:villager", mgl32.Vec3{1, 0, 1})
	m := trading.NewManager(bot, discardLogger())
	m.SetTimings(trading.FastTimings())

	if _, _, err := m.OpenTradeWindow(context.Background()); err == nil {
		t.Fatal("OpenTradeWindow succeeded with no observed trade window")
	}
	if len(bot.closed) != 1 {
		t.Errorf("closed %d windows, want 1 — the window it opened still has to be closed", len(bot.closed))
	}
}

// TestOpenTradeWindowFailsWithoutAVillager.
func TestOpenTradeWindowFailsWithoutAVillager(t *testing.T) {
	t.Parallel()

	m := trading.NewManager(newFakeBot(), discardLogger())
	m.SetTimings(trading.FastTimings())

	if _, _, err := m.OpenTradeWindow(context.Background()); err == nil {
		t.Fatal("OpenTradeWindow succeeded in a world with no villagers")
	}
}

// TestOpenTradeWindowFailsWhenTheServerNeverOpensOne.
func TestOpenTradeWindowFailsWhenTheServerNeverOpensOne(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	bot.openOK = false

	if _, _, err := m.OpenTradeWindow(context.Background()); err == nil {
		t.Fatal("OpenTradeWindow succeeded though no ContainerOpen arrived")
	}
}

// TestXPIsNeverClaimedWhenNothingObservesIt is the 7.2 honesty rule. A bot whose
// level nothing reads must say "unknown", not report a delta it invented.
func TestXPIsNeverClaimedWhenNothingObservesIt(t *testing.T) {
	t.Parallel()

	_, _, m := tradeFixture()

	res, ok := m.Trade(context.Background(), "bread")
	if !ok {
		t.Fatalf("Trade failed: %s", res.Reason)
	}
	if res.XPObserved {
		t.Error("XPObserved = true with no experience source wired")
	}
	if res.XPDelta != 0 {
		t.Errorf("XPDelta = %d with nothing observing the level; unobserved XP has no delta", res.XPDelta)
	}
	if res.XPBefore != 0 || res.XPAfter != 0 {
		t.Errorf("XPBefore/After = %d/%d, want 0/0 when nothing observes the level", res.XPBefore, res.XPAfter)
	}
}

// TestXPIsReportedOnlyWhenItWasObserved is the other half: once the level is
// real it is read before and after, and the trade that changed it says so.
func TestXPIsReportedOnlyWhenItWasObserved(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	xp := &fakeXP{level: 12, known: true}
	bot.onTake = func() { xp.level = 7 }
	m.SetXPObserver(xp)

	res, ok := m.Trade(context.Background(), "bread")
	if !ok {
		t.Fatalf("Trade failed: %s", res.Reason)
	}
	if !res.XPObserved {
		t.Fatal("XPObserved = false with an experience source wired")
	}
	if res.XPBefore != 12 {
		t.Errorf("XPBefore = %d, want 12", res.XPBefore)
	}
	if res.XPAfter != 7 {
		t.Errorf("XPAfter = %d, want 7", res.XPAfter)
	}
	if res.XPDelta != -5 {
		t.Errorf("XPDelta = %d, want -5", res.XPDelta)
	}
}

// TestAnUnchangedXPLevelReportsZeroNotAPendingValue. A plain trade costs no
// levels; reporting "unknown" for an observed level that simply did not move
// would be a different lie.
func TestAnUnchangedXPLevelReportsZeroNotAPendingValue(t *testing.T) {
	t.Parallel()

	_, _, m := tradeFixture()
	xp := &fakeXP{level: 4, known: true}
	m.SetXPObserver(xp)

	res, ok := m.Trade(context.Background(), "bread")
	if !ok {
		t.Fatalf("Trade failed: %s", res.Reason)
	}
	if !res.XPObserved {
		t.Fatal("XPObserved = false with an experience source wired")
	}
	if res.XPDelta != 0 {
		t.Errorf("XPDelta = %d for a trade that cost no levels, want 0", res.XPDelta)
	}
}

// TestAnXPTradeIsRefusedWhenTheLevelCannotBeSeen. Spending 25 levels on a
// trade the bot cannot afford is the failure this guards: the emeralds go and
// the server refuses the trade.
func TestAnXPTradeIsRefusedWhenTheLevelCannotBeSeen(t *testing.T) {
	t.Parallel()

	bot, obs, m := tradeFixture()
	tw := obs.windows[testVillagerID]
	tw.Offers[0].XPCost = 25
	obs.windows[testVillagerID] = tw

	if _, ok := m.Trade(context.Background(), "bread"); ok {
		t.Fatal("Trade spent an XP-costing offer it could not check the level against")
	}
	if len(bot.places) != 0 {
		t.Errorf("staged %d items for an XP offer it could not verify", len(bot.places))
	}
}

// TestAnXPTradeGoesThroughWhenTheLevelIsHighEnough closes the loop: the same
// offer is accepted once the level is genuinely observed and sufficient.
func TestAnXPTradeGoesThroughWhenTheLevelIsHighEnough(t *testing.T) {
	t.Parallel()

	bot, obs, m := tradeFixture()
	tw := obs.windows[testVillagerID]
	tw.Offers[0].XPCost = 25
	obs.windows[testVillagerID] = tw

	xp := &fakeXP{level: 30, known: true}
	bot.onTake = func() { xp.level = 5 }
	m.SetXPObserver(xp)

	res, ok := m.Trade(context.Background(), "bread")
	if !ok {
		t.Fatalf("Trade failed: %s", res.Reason)
	}
	if res.XPDelta != -25 {
		t.Errorf("XPDelta = %d, want -25", res.XPDelta)
	}
	if got := bot.countItem("minecraft:bread"); got != 8 {
		t.Errorf("bread = %d, want 8", got)
	}
}

// TestAFailedTradeDoesNotReportAnXPDelta. The levels only moved if the trade
// did; a failure must not carry a number that reads like a cost.
func TestAFailedTradeDoesNotReportAnXPDelta(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	bot.takeSwallows = true
	xp := &fakeXP{level: 12, known: true}
	m.SetXPObserver(xp)

	res, ok := m.Trade(context.Background(), "bread")
	if ok {
		t.Fatal("Trade reported success though nothing arrived")
	}
	if res.XPDelta != 0 {
		t.Errorf("XPDelta = %d on a trade that did not happen", res.XPDelta)
	}
}

// TestTradeWithAnEmptyWantAcceptsTheBestDeal. "Trade with the villager" with no
// item named means take the best thing on offer, not refuse to trade at all.
func TestTradeWithAnEmptyWantAcceptsTheBestDeal(t *testing.T) {
	t.Parallel()

	_, obs, m := tradeFixture()
	tw := obs.windows[testVillagerID]
	tw.Offers = []trading.Offer{
		{
			Index:  0,
			Inputs: []trading.Item{{Name: "minecraft:emerald", Count: 1}},
			Output: trading.Item{Name: "minecraft:bread", Count: 1},
		},
		{
			Index:  1,
			Inputs: []trading.Item{{Name: "minecraft:emerald", Count: 1}},
			Output: trading.Item{Name: "minecraft:bread", Count: 8},
		},
	}
	obs.windows[testVillagerID] = tw

	res, ok := m.Trade(context.Background(), "")
	if !ok {
		t.Fatalf("Trade failed with an empty want: %s", res.Reason)
	}
	if res.Offer.Index != 1 {
		t.Errorf("chose offer %d, want the better one (1)", res.Offer.Index)
	}
}
