package trading_test

import (
	"context"
	"strings"
	"testing"

	"bedrock-ai/internal/bot/inventory/trading"
)

// TestTradeWithNoVillagerSaysWhy keeps the bot honest in an empty world: no
// villager means no trade, and the reason has to name the villager rather than
// a stack request that never happened.
func TestTradeWithNoVillagerSaysWhy(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.putItem(0, "minecraft:emerald", 16)
	m := trading.NewManager(bot, discardLogger())
	m.SetTimings(trading.FastTimings())

	res, ok := m.Trade(context.Background(), "bread")
	if ok {
		t.Fatal("Trade reported success with no villager in the world")
	}
	if !strings.Contains(strings.ToLower(res.Reason), "villager") {
		t.Errorf("Reason = %q, want it to name the missing villager", res.Reason)
	}
	if len(bot.written) != 0 {
		t.Errorf("Trade sent %d packets with no villager to interact with", len(bot.written))
	}
	if len(bot.closed) != 0 {
		t.Errorf("Trade closed %d windows it never opened", len(bot.closed))
	}
}

// TestTradeWithoutAnObserverDoesNotTrade is the honest default. Until the
// network layer records packet.UpdateTrade there are no offers, and a bot with
// no offers must say so rather than guess at a price and hand over emeralds.
func TestTradeWithoutAnObserverDoesNotTrade(t *testing.T) {
	t.Parallel()

	bot, _, _ := tradeFixture()
	m := trading.NewManager(bot, discardLogger())
	m.SetTimings(trading.FastTimings())

	res, ok := m.Trade(context.Background(), "bread")
	if ok {
		t.Fatal("Trade reported success with nothing observing the trade window")
	}
	if !strings.Contains(strings.ToLower(res.Reason), "offer") {
		t.Errorf("Reason = %q, want it to say no offers were observed", res.Reason)
	}
	if got := bot.countItem("minecraft:emerald"); got != 16 {
		t.Errorf("emeralds = %d; a trade that could not read the offers still spent some", got)
	}
}

// TestTradeWithNoOffersObservedDoesNotTrade is the same rule one step further
// on: the seam is wired, but this particular villager's window never arrived.
func TestTradeWithNoOffersObservedDoesNotTrade(t *testing.T) {
	t.Parallel()

	bot, obs, m := tradeFixture()
	delete(obs.windows, testVillagerID)

	res, ok := m.Trade(context.Background(), "bread")
	if ok {
		t.Fatal("Trade reported success for a villager whose offers were never observed")
	}
	if res.Reason == "" {
		t.Error("Trade failed with an empty reason")
	}
	if got := bot.countItem("minecraft:emerald"); got != 16 {
		t.Errorf("emeralds = %d, want 16 — nothing should have been spent", got)
	}
}

// TestTradeWithAnEmptyOfferListDoesNotTrade. A villager with nothing on sale is
// a real state and is not an invitation to invent an offer.
func TestTradeWithAnEmptyOfferListDoesNotTrade(t *testing.T) {
	t.Parallel()

	bot, obs, m := tradeFixture()
	tw := obs.windows[testVillagerID]
	tw.Offers = nil
	tw.OfferCount = 0
	obs.windows[testVillagerID] = tw

	if _, ok := m.Trade(context.Background(), "bread"); ok {
		t.Fatal("Trade reported success against a villager offering nothing")
	}
	if got := bot.countItem("minecraft:emerald"); got != 16 {
		t.Errorf("emeralds = %d, want 16", got)
	}
}

// TestTradeWithNoAffordableOfferDoesNotTrade is the emeralds gate at the
// manager level, with a villager that is otherwise perfectly willing.
func TestTradeWithNoAffordableOfferDoesNotTrade(t *testing.T) {
	t.Parallel()

	bot, obs, m := tradeFixture()
	tw := obs.windows[testVillagerID]
	tw.Offers[0].Inputs[0].Count = 64
	obs.windows[testVillagerID] = tw

	res, ok := m.Trade(context.Background(), "bread")
	if ok {
		t.Fatal("Trade reported success with 16 emeralds against a 64-emerald offer")
	}
	reason := strings.ToLower(res.Reason)
	if !strings.Contains(reason, "afford") && !strings.Contains(reason, "emerald") {
		t.Errorf("Reason = %q, want it to explain the offer was unaffordable", res.Reason)
	}
	if len(bot.places) != 0 {
		t.Errorf("staged %d items for an offer the bot could not afford: %+v", len(bot.places), bot.places)
	}
}

// TestTradeRefusesAnOfferTheBotDoesNotWant. A bot asked for a map must not come
// back with bread just because the bread was on offer.
func TestTradeRefusesAnOfferTheBotDoesNotWant(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()

	res, ok := m.Trade(context.Background(), "ender_pearl")
	if ok {
		t.Fatalf("Trade bought bread when asked for an ender pearl: %+v", res)
	}
	if len(bot.places) != 0 {
		t.Errorf("staged %d items for an unwanted offer", len(bot.places))
	}
}

// TestTradeRefusesWhenTheServerNeverOpensTheWindow. An interaction packet that
// produces no ContainerOpen means no trade UI, and reporting otherwise is the
// exact lie this package exists to remove.
func TestTradeRefusesWhenTheServerNeverOpensTheWindow(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	bot.openOK = false

	res, ok := m.Trade(context.Background(), "bread")
	if ok {
		t.Fatal("Trade reported success though the server never opened a window")
	}
	if res.Reason == "" {
		t.Error("Trade failed with an empty reason")
	}
	if len(bot.places) != 0 || len(bot.takes) != 0 {
		t.Errorf("staged items against a window that never opened: %+v %+v", bot.places, bot.takes)
	}
}
