package station_test

import (
	"context"
	"testing"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"bedrock-ai/internal/bot/inventory/station"
)

// TestFindStationPrefersTheNearestNamedBlock. A brewing stand across the room
// is not the one to walk to, and a wall in between is not a candidate at all.
func TestFindStationPrefersTheNearestNamedBlock(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	// The bot stands at 4.5,64,4.5, so (4,64,5) is one step away and (6,64,6)
	// is four; a plain wall sits between them.
	bot.addBlock(6, 64, 6, "brewing_stand")
	bot.addBlock(4, 64, 4, "stone")
	bot.addBlock(4, 64, 5, "brewing_stand")

	m := station.NewManager(bot, discardLogger())
	pos, ok := m.FindNearbyStation(station.IsBrewingStandBlock)
	if !ok {
		t.Fatal("FindNearbyStation found no brewing stand although two are in range")
	}
	if pos != (protocol.BlockPos{4, 64, 5}) {
		t.Errorf("found %v, want the nearer stand at 4,64,5", pos)
	}
}

// TestFindStationIgnoresUnrelatedBlocks. This is the fix for the original
// search, which returned the first solid block it could see.
func TestFindStationIgnoresUnrelatedBlocks(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(4, 64, 4, "stone")
	bot.addBlock(4, 64, 5, "dirt")
	bot.addBlock(4, 64, 6, "oak_planks")

	m := station.NewManager(bot, discardLogger())
	if _, ok := m.FindNearbyStation(station.IsBrewingStandBlock); ok {
		t.Error("FindNearbyStation returned a solid block that is not a brewing stand")
	}
}

// TestFindStationRespectsTheSearchRadius. A stand on the far side of the map is
// not one this action should walk the whole world for.
func TestFindStationRespectsTheSearchRadius(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(40, 64, 40, "brewing_stand")

	m := station.NewManager(bot, discardLogger())
	if _, ok := m.FindNearbyStation(station.IsBrewingStandBlock); ok {
		t.Error("FindNearbyStation walked 40 blocks for a brewing stand")
	}
}

// TestNormalizeItemName is the shared name cleanup every lookup depends on: the
// runtime name table reports "minecraft:lapis_lazuli" and a recipe table keyed
// on "lapis_lazuli" must still match it.
func TestNormalizeItemName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"minecraft:lapis_lazuli": "lapis_lazuli",
		"  LAPIS_LAZULI ":        "lapis_lazuli",
		"minecraft:Iron_Pickaxe": "iron_pickaxe",
		"":                       "",
	}
	for in, want := range cases {
		if got := station.NormalizeItemName(in); got != want {
			t.Errorf("NormalizeItemName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestItemMatchesDoesNotMatchSubstringsBlindly. The furnace matcher treats
// "iron" as matching "iron_ingot", which is right for fuel and wrong for a
// repair material: an iron pickaxe is not an iron ingot.
func TestItemMatchesDoesNotMatchSubstringsBlindly(t *testing.T) {
	t.Parallel()

	if !station.ItemMatches("minecraft:lapis_lazuli", "lapis_lazuli") {
		t.Error("ItemMatches missed a namespaced exact match")
	}
	if station.ItemMatches("iron_pickaxe", "iron_ingot") {
		t.Error("ItemMatches(iron_pickaxe, iron_ingot) = true, want false")
	}
	if station.ItemMatches("diamond_sword", "iron_ingot") {
		t.Error("ItemMatches matched two unrelated items")
	}
	if station.ItemMatches("", "lapis_lazuli") {
		t.Error("ItemMatches matched an empty name")
	}
}

// TestManagerClosesTheWindowOnEveryPath. A window left open server-side
// desyncs the next open, so both the success and the failure path must close
// it by the ID the server assigned.
func TestManagerClosesTheWindowOnEveryPath(t *testing.T) {
	t.Parallel()

	for _, openFails := range []bool{false, true} {
		openFails := openFails
		t.Run("openFails="+boolName(openFails), func(t *testing.T) {
			t.Parallel()

			bot := newFakeBot()
			bot.addBlock(6, 64, 5, "anvil")
			bot.addItem(0, 1, "iron_pickaxe", 1)
			bot.addItem(8, 2, "iron_ingot", 3)
			bot.openFails = openFails

			m := station.NewManager(bot, discardLogger())
			m.SetPollInterval(time.Millisecond)
			m.SetAnvilBudget(40 * time.Millisecond)

			_, _ = m.RepairItem(context.Background(), "iron_pickaxe")

			if !openFails && bot.closedWindow != bot.windowID {
				t.Errorf("closed window %d, want the server-assigned %d", bot.closedWindow, bot.windowID)
			}
			if !bot.closedLook {
				t.Error("the look was never reset")
			}
		})
	}
}

func boolName(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
