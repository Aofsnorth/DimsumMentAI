package trading_test

import (
	"context"
	"testing"

	"bedrock-ai/internal/bot/inventory/trading"

	"github.com/go-gl/mathgl/mgl32"
)

const testVillagerID uint64 = 77

// villagerWindow is the trade window a host pushes for a villager offering
// emeralds for bread. Tests vary it rather than rebuilding it.
func villagerWindow() trading.TradeWindow {
	return trading.TradeWindow{
		WindowID:         42,
		WindowType:       trading.TradeWindowType,
		DisplayName:      "minecraft:merchant",
		TradeTier:        1,
		OfferCount:       1,
		NewTradeUI:       true,
		VillagerUniqueID: int64(testVillagerID),
		Offers: []trading.Offer{{
			Inputs:  []trading.Item{{Name: "minecraft:emerald", Count: 1}},
			Output:  trading.Item{Name: "minecraft:bread", Count: 8},
			MaxUses: 16,
		}},
	}
}

// tradeFixture is a bot standing next to one villager holding emeralds, with
// the trade window already observed.
func tradeFixture() (*fakeBot, *fakeTradeObserver, *trading.Manager) {
	bot := newFakeBot()
	bot.coords = mgl32.Vec3{0, 0, 0}
	bot.addVillager(testVillagerID, "minecraft:villager", mgl32.Vec3{1, 0, 1})
	bot.putItem(0, "minecraft:emerald", 16)

	obs := newFakeTradeObserver()
	obs.windows[testVillagerID] = villagerWindow()

	m := trading.NewManager(bot, discardLogger())
	m.SetTradeObserver(obs)
	m.SetTimings(trading.FastTimings())
	return bot, obs, m
}

// TestIsVillagerMatchesByTypeName is the detection rule. Proximity is not
// enough: a pig standing in the right place is not a trading partner, and a bot
// that trades with it reports a purchase that never happened.
func TestIsVillagerMatchesByTypeName(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"minecraft:villager", "Villager", " villager "} {
		if !trading.IsVillagerType(in) {
			t.Errorf("IsVillagerType(%q) = false, want true", in)
		}
	}
	for _, in := range []string{
		"", "minecraft:pig", "minecraft:cow", "minecraft:player",
		"minecraft:item", "minecraft:villager_egg",
	} {
		if trading.IsVillagerType(in) {
			t.Errorf("IsVillagerType(%q) = true, want false", in)
		}
	}
}

// TestFindVillagerIgnoresNearbyNonVillagers pins detection to the entity type.
// Both a pig and a cow sit closer than the villager in this world.
func TestFindVillagerIgnoresNearbyNonVillagers(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addVillager(1, "minecraft:pig", mgl32.Vec3{0.5, 0, 0.5})
	bot.addVillager(2, "minecraft:cow", mgl32.Vec3{0.4, 0, 0.4})

	m := trading.NewManager(bot, discardLogger())
	m.SetTimings(trading.FastTimings())

	if _, ok := m.FindVillager(); ok {
		t.Error("FindVillager accepted a pig standing closest to the bot")
	}

	bot.addVillager(4, "minecraft:villager", mgl32.Vec3{3, 0, 3})
	found, ok := m.FindVillager()
	if !ok {
		t.Fatal("FindVillager did not find the villager once one existed")
	}
	if found.ID != 4 {
		t.Errorf("FindVillager returned entity %d, want the villager (4)", found.ID)
	}
}

// TestFindVillagerPrefersTheNearestOfSeveral is the tie-break. Two villagers in
// the same room: the bot trades with the one it can reach, not whichever the
// map iteration happened to visit last.
func TestFindVillagerPrefersTheNearestOfSeveral(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addVillager(10, "minecraft:villager", mgl32.Vec3{20, 0, 0})
	bot.addVillager(11, "minecraft:villager", mgl32.Vec3{1, 0, 0})

	m := trading.NewManager(bot, discardLogger())
	found, ok := m.FindVillager()
	if !ok {
		t.Fatal("FindVillager found nothing")
	}
	if found.ID != 11 {
		t.Errorf("FindVillager returned entity %d, want the nearer villager (11)", found.ID)
	}
}

// TestFindVillagerRespectsItsSearchRadius. A villager across the map is not one
// this action should walk the whole world for.
func TestFindVillagerRespectsItsSearchRadius(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addVillager(1, "minecraft:villager", mgl32.Vec3{64, 0, 0})

	m := trading.NewManager(bot, discardLogger())
	if _, ok := m.FindVillager(); ok {
		t.Error("FindVillager reached for a villager 64 blocks away")
	}
}

// TestTradeCompletesAndConfirms is the 7.2 happy path end to end: the window
// opens, the emerald goes into the ingredient container, the result comes back
// and lands in the inventory, and only then is the trade reported as done.
func TestTradeCompletesAndConfirms(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	res, ok := m.Trade(context.Background(), "bread")
	if !ok {
		t.Fatalf("Trade failed: %s", res.Reason)
	}

	if got := bot.countItem("minecraft:emerald"); got != 15 {
		t.Errorf("emeralds = %d after the trade, want 15 (one spent)", got)
	}
	if got := bot.countItem("minecraft:bread"); got != 8 {
		t.Errorf("bread = %d after the trade, want 8", got)
	}
	if res.Gained.Name != "minecraft:bread" || res.Gained.Count != 8 {
		t.Errorf("Gained = %+v, want 8 minecraft:bread", res.Gained)
	}
	if res.VillagerID != testVillagerID {
		t.Errorf("VillagerID = %d, want %d", res.VillagerID, testVillagerID)
	}
	if res.WindowID != bot.openWindowID {
		t.Errorf("WindowID = %d, want the server-assigned %d", res.WindowID, bot.openWindowID)
	}
	if len(res.Spent) != 1 || res.Spent[0].Name != "minecraft:emerald" || res.Spent[0].Count != 1 {
		t.Errorf("Spent = %+v, want 1 minecraft:emerald", res.Spent)
	}
}
