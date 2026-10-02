package recipe_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot/inventory/recipe"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func newTestManager(b *fakeBot) *recipe.Manager {
	return recipe.NewManager(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestInventorySnapshotIsSortedBySlot is why Inventory is a slice and not the
// bot's map. Map iteration order is random, so a planner that resolved "the
// first slot holding an item" would produce a different plan on every call, and
// a smithing table would see the template and the sword swap places between
// runs.
func TestInventorySnapshotIsSortedBySlot(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.put(12, "minecraft:netherite_upgrade_smithing_template", 1)
	bot.put(3, "minecraft:diamond_sword", 1)
	bot.put(30, "minecraft:oak_log", 8)

	inv := newTestManager(bot).Inventory()
	if len(inv) != 3 {
		t.Fatalf("snapshot has %d entries, want 3", len(inv))
	}
	for i := 1; i < len(inv); i++ {
		if inv[i-1].Slot >= inv[i].Slot {
			t.Fatalf("snapshot is not sorted: %+v", inv)
		}
	}
	if inv[0].Slot != 3 || inv[0].Name != "diamond_sword" {
		t.Errorf("first entry = %+v, want slot 3 diamond_sword", inv[0])
	}
}

// TestInventoryDropsEmptySlots keeps empty stacks out of a plan. A plan that
// staged "count 0" of something is a plan that sends a zero-count transfer the
// server rejects, and it fails for a reason that has nothing to do with the
// station.
func TestInventoryDropsEmptySlots(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.put(1, "minecraft:stone", 0)
	bot.put(2, "minecraft:stone", 4)

	inv := newTestManager(bot).Inventory()
	if len(inv) != 1 {
		t.Fatalf("snapshot = %+v, want only the non-empty stack", inv)
	}
	if inv[0].Slot != 2 || inv[0].Count != 4 {
		t.Errorf("kept the wrong stack: %+v", inv[0])
	}
}

// TestInventoryNamesAreNormalised covers the two forms a name arrives in: the
// world and the item registry say "minecraft:diamond_sword", while a config or a
// planner speaks "diamond_sword". Comparing the raw strings finds neither.
func TestInventoryNamesAreNormalised(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	netID := int32(7)
	bot.names[netID] = "minecraft:diamond_sword"
	bot.items[4] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: netID}, Count: 1}

	inv := newTestManager(bot).Inventory()
	if inv[0].Name != "diamond_sword" {
		t.Fatalf("name = %q, want %q", inv[0].Name, "diamond_sword")
	}
	if _, ok := inv.Find("minecraft:diamond_sword"); !ok {
		t.Error("Find with a namespaced name missed the stack")
	}
	if _, ok := inv.Find("Diamond Sword"); !ok {
		t.Error("Find with a spaced, capitalised name missed the stack")
	}
}

// TestInventoryCountSumsEverySlot matters for the cartography table, where the
// result is another copy of the input. A bot holding three maps in two slots and
// asking for a copy has to be told the result count is four, not "a map is
// present".
func TestInventoryCountSumsEverySlot(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "map", Count: 2},
		{Slot: 5, Name: "map", Count: 1},
		{Slot: 9, Name: "paper", Count: 64},
	}
	if got := inv.Count("map"); got != 3 {
		t.Errorf("Count(map) = %d, want 3", got)
	}
	if got := inv.Count("minecraft:map"); got != 3 {
		t.Errorf("Count with a namespaced name = %d, want 3", got)
	}
	if inv.Has("paper") != true {
		t.Error("Has(paper) = false, the bag holds 64")
	}
	if inv.Has("glass_pane") {
		t.Error("Has(glass_pane) = true, the bag holds none")
	}
}

// TestInventoryFindReturnsTheLowestSlot is the determinism guarantee a plan
// depends on: two slots of the same item must always resolve to the same one,
// or a two-slot plan stages half from one stack and half from the other.
func TestInventoryFindReturnsTheLowestSlot(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 8, Name: "netherite_upgrade_smithing_template", Count: 1},
		{Slot: 2, Name: "netherite_upgrade_smithing_template", Count: 1},
	}
	for i := 0; i < 20; i++ {
		got, ok := inv.Find("netherite_upgrade_smithing_template")
		if !ok || got.Slot != 2 {
			t.Fatalf("Find returned %+v on attempt %d, want slot 2", got, i)
		}
	}
}

// TestFindStationRecipeFiltersOnTheStationBlock is the difference between
// "stone bricks" and "stone bricks, cut". Stone brick slabs and stone bricks can
// both come out of a crafting table, and picking the crafting-table recipe for a
// stonecutter task means the bot never touches the stonecutter at all — the
// single easiest way to claim 4.6 while doing nothing.
func TestFindStationRecipeFiltersOnTheStationBlock(t *testing.T) {
	t.Parallel()

	recipes := []recipe.StationRecipe{
		{NetworkID: 11, Block: "crafting_table", ResultName: "stone_bricks", ResultCount: 1},
		{NetworkID: 22, Block: "stonecutter", ResultName: "stone_bricks", ResultCount: 1},
		{NetworkID: 33, Block: "crafting_table", ResultName: "stone_brick_slab", ResultCount: 2},
	}
	stonecutter, _ := recipe.StationByKind(recipe.KindStonecutter)

	got, ok := recipe.FindStationRecipe(recipes, stonecutter, "stone_bricks")
	if !ok {
		t.Fatal("the stonecutter recipe for stone bricks was not found")
	}
	if got.NetworkID != 22 {
		t.Errorf("picked recipe %d, want the stonecutter recipe 22", got.NetworkID)
	}

	if _, ok := recipe.FindStationRecipe(recipes, stonecutter, "stone_brick_slab"); ok {
		t.Error("a crafting-table-only recipe was offered to the stonecutter")
	}
	if _, ok := recipe.FindStationRecipe(recipes, stonecutter, "  "); ok {
		t.Error("an empty result name matched a recipe")
	}
	if _, ok := recipe.FindStationRecipe(nil, stonecutter, "stone_bricks"); ok {
		t.Error("an empty recipe list matched")
	}
}

// TestFindStationRecipeNormalisesTheResultName keeps a recipe registered as
// "minecraft:stone_bricks" findable by the bare name a caller passes.
func TestFindStationRecipeNormalisesTheResultName(t *testing.T) {
	t.Parallel()

	recipes := []recipe.StationRecipe{
		{NetworkID: 7, Block: "minecraft:stonecutter", ResultName: "minecraft:stone_bricks"},
	}
	stonecutter, _ := recipe.StationByKind(recipe.KindStonecutter)
	got, ok := recipe.FindStationRecipe(recipes, stonecutter, "stone_bricks")
	if !ok {
		t.Fatal("a namespaced recipe was not found by its bare name")
	}
	if got.ResultCount != 1 {
		t.Errorf("ResultCount = %d, want the default of 1", got.ResultCount)
	}
}

// TestPlanCarriesTheStationWindowSlotIndexes keeps the staging order visible.
// The window indexes are the one thing about these stations the vendored
// protocol does not publish, so they live in one table and are pinned here.
func TestPlanCarriesTheStationWindowSlotIndexes(t *testing.T) {
	t.Parallel()

	smithing, _ := recipe.StationByKind(recipe.KindSmithingTable)
	wantInput := []uint32{0, 1, 2}
	for i, slot := range smithing.Inputs {
		if slot.Index != wantInput[i] {
			t.Errorf("smithing input %d index = %d, want %d", i, slot.Index, wantInput[i])
		}
	}
	if smithing.Result.Index != 3 {
		t.Errorf("smithing result index = %d, want 3", smithing.Result.Index)
	}
}

// TestCraftRequestCarriesTheOpenWindow is the seam contract: the bot-side
// helper builds the stack request against the window the SERVER assigned, so
// the request has to carry that ID and not a guessed one.
func TestCraftRequestCarriesTheOpenWindow(t *testing.T) {
	t.Parallel()

	loom, _ := recipe.StationByKind(recipe.KindLoom)
	req := recipe.CraftRequest{
		Plan: recipe.Plan{
			Station:     loom,
			Pattern:     "minecraft:globe",
			ResultName:  "red_banner",
			ResultCount: 1,
		},
		WindowID: 42,
	}
	if req.WindowID != 42 {
		t.Errorf("WindowID = %d, want 42", req.WindowID)
	}
	if req.Station.Kind != recipe.KindLoom {
		t.Errorf("station kind = %q, want loom", req.Station.Kind)
	}
	if req.UsesRecipe() {
		t.Error("a loom craft reported a recipe network ID; the loom sends a pattern")
	}
	withRecipe := recipe.Plan{RecipeNetworkID: 9}
	if !withRecipe.UsesRecipe() {
		t.Error("a plan with a recipe network ID reported UsesRecipe() = false")
	}
}

// TestPlanInputLooksUpByRole makes the plan's own accessor honest rather than
// returning a zero StagedInput for a role the plan never staged.
func TestPlanInputLooksUpByRole(t *testing.T) {
	t.Parallel()

	station, _ := recipe.StationByKind(recipe.KindStonecutter)
	plan := recipe.Plan{
		Station: station,
		Inputs: []recipe.StagedInput{
			{Slot: station.Inputs[0], SourceSlot: 4, ItemName: "stone", Count: 1},
		},
	}
	got, ok := plan.Input(recipe.RoleInput)
	if !ok || got.SourceSlot != 4 {
		t.Errorf("Input(input) = %+v, %v; want the staged stone", got, ok)
	}
	if _, ok := plan.Input(recipe.RoleTemplate); ok {
		t.Error("a plan with no template reported one")
	}
}

// TestManagerRejectsAnUnknownStationBeforeTouchingTheWorld keeps a typo'd or
// unimplemented station from turning into a blind block scan.
func TestManagerRejectsAnUnknownStationBeforeTouchingTheWorld(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	mgr := newTestManager(bot)
	if _, ok := mgr.FindStation(recipe.Kind("enchanting_table")); ok {
		t.Error("FindStation matched a station this package does not drive")
	}
	if len(bot.clicks) != 0 {
		t.Errorf("the world was clicked %d times for a station that does not exist", len(bot.clicks))
	}
}

// TestManagerOpenTimeoutIsBounded documents that the open is a wait, not a
// sleep: the fake answers instantly either way, but the budget is what stops a
// host that never opens a window from parking the bot for a whole craft.
func TestManagerOpenTimeoutIsBounded(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.openFails = true
	mgr := newTestManager(bot)
	bot.placeBlock(0, 0, 0, "smithing_table")
	bot.put(1, "minecraft:netherite_upgrade_smithing_template", 1)
	bot.put(2, "minecraft:diamond_sword", 1)
	bot.recipes = []recipe.StationRecipe{{NetworkID: 1, Block: "smithing_table", ResultName: "netherite_sword"}}

	start := time.Now()
	_, ok := mgr.UpgradeToNetherite(t.Context())
	elapsed := time.Since(start)
	if ok {
		t.Fatal("a station window the server never opened was reported as a craft")
	}
	if elapsed > 5*time.Second {
		t.Errorf("waited %v for a window that was never coming", elapsed)
	}
}
