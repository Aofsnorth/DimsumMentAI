package recipe_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/inventory/recipe"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// smithingWorld seeds a bag and a world that can actually complete 4.5.
func smithingWorld(t *testing.T) *fakeBot {
	t.Helper()
	bot := newFakeBot()
	bot.pos = mgl32Zero()
	bot.placeBlock(0, 0, 0, "smithing_table")
	bot.put(0, "minecraft:netherite_upgrade_smithing_template", 1)
	bot.put(4, "minecraft:diamond_sword", 1)
	bot.put(7, "minecraft:netherite_ingot", 4)
	bot.recipes = smithingRecipes()
	bot.resultItem = "netherite_sword"
	return bot
}

// TestStationCraftUsesTheWindowTheServerAssigned is the packet discipline the
// furnace package established: arm the watch BEFORE the click (ContainerOpen can
// arrive within a frame), and address the window the server gave, never a
// guessed zero. The fake assigns window 5 precisely so a hardcoded 0 cannot pass
// by accident.
func TestStationCraftUsesTheWindowTheServerAssigned(t *testing.T) {
	t.Parallel()

	bot := smithingWorld(t)
	mgr := newTestManager(bot)

	plan, ok := mgr.UpgradeToNetherite(t.Context())
	if !ok {
		t.Fatal("UpgradeToNetherite() failed on a world set up for it")
	}
	if !bot.armBeforeClick {
		t.Error("the container watch was not armed before the click; a ContainerOpen in the same frame is missed")
	}
	if len(bot.clicks) != 1 {
		t.Fatalf("clicked %d times, want exactly 1", len(bot.clicks))
	}
	if len(bot.crafts) != 1 {
		t.Fatalf("submitted %d craft requests, want 1", len(bot.crafts))
	}
	if bot.crafts[0].WindowID != bot.windowID {
		t.Errorf("craft used window %d, want the server-assigned %d", bot.crafts[0].WindowID, bot.windowID)
	}
	if len(bot.closed) != 1 || bot.closed[0] != bot.windowID {
		t.Errorf("closed = %v, want the assigned window %d closed once", bot.closed, bot.windowID)
	}
	if plan.ResultName != "netherite_sword" {
		t.Errorf("plan result = %q, want netherite_sword", plan.ResultName)
	}
}

// TestStationCraftStagesEachInputIntoItsOwnStationSlot checks the staging
// addresses. Window slot 0 is the template, 1 the base, 2 the addition — a plan
// that put the base in slot 0 would burn the template.
func TestStationCraftStagesEachInputIntoItsOwnStationSlot(t *testing.T) {
	t.Parallel()

	bot := smithingWorld(t)
	if _, ok := newTestManager(bot).UpgradeToNetherite(t.Context()); !ok {
		t.Fatal("UpgradeToNetherite() failed")
	}
	want := []placedStack{
		{windowID: bot.windowID, slot: 0, count: 1, destStackNet: 0, srcSlot: 0}, // template
		{windowID: bot.windowID, slot: 1, count: 1, destStackNet: 0, srcSlot: 4}, // base
		{windowID: bot.windowID, slot: 2, count: 1, destStackNet: 0, srcSlot: 7}, // addition
	}
	if len(bot.placed) != len(want) {
		t.Fatalf("placed %d stacks, want %d: %+v", len(bot.placed), len(want), bot.placed)
	}
	for i, w := range want {
		if bot.placed[i] != w {
			t.Errorf("placement %d = %+v, want %+v", i, bot.placed[i], w)
		}
	}
}

// TestStationCraftIsNotReportedBeforeTheResultArrives is the acceptance rule
// itself. A craft the server accepted but whose result never arrived is still a
// failure: the bot has burned a template and got nothing, and reporting success
// hides exactly that.
func TestStationCraftIsNotReportedBeforeTheResultArrives(t *testing.T) {
	t.Parallel()

	bot := smithingWorld(t)
	bot.resultItem = "" // the craft is accepted, nothing is ever delivered
	// The bag already holds the ingredients, so "something is in there" is not
	// a signal. Only the count rising past the pre-craft count is.
	before := len(bot.items)

	mgr := newTestManager(bot)
	mgr.SetResultBudget(300 * time.Millisecond)
	start := time.Now()
	_, ok := mgr.UpgradeToNetherite(t.Context())
	elapsed := time.Since(start)

	if ok {
		t.Fatal("a craft with no result was reported as a success")
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("gave up after %v; the result was polled for essentially no time", elapsed)
	}
	if len(bot.items) != before {
		t.Errorf("the bag changed without a result: %d -> %d", before, len(bot.items))
	}
	if len(bot.closed) != 1 {
		t.Errorf("closed = %v; the window must be closed even when the craft fails", bot.closed)
	}
}

// TestStationCraftCountsAResultThatLooksLikeAnInput is the cartography case and
// the reason confirmation is a count, not a presence check. Copying a map turns
// one map into two; a bot that asks "is there a map in the bag" is already true
// before the craft and would report a success the moment the server accepted
// the request.
func TestStationCraftCountsAResultThatLooksLikeAnInput(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.pos = mgl32Zero()
	bot.placeBlock(0, 0, 0, "cartography_table")
	bot.put(3, "minecraft:map", 1)
	bot.put(8, "minecraft:paper", 12)
	bot.recipes = cartographyRecipes()
	// The copy only lands after a delay, so a presence check would pass
	// immediately on the pre-existing map.
	bot.resultItem = "map"
	bot.resultCount = 1
	bot.craftDelay = 200 * time.Millisecond

	mgr := newTestManager(bot)
	if _, ok := mgr.CopyMap(t.Context()); !ok {
		t.Fatal("CopyMap() failed on a world set up for it")
	}
	if got := mgr.Inventory().Count("map"); got != 2 {
		t.Errorf("map count = %d after a copy, want 2", got)
	}
	if len(bot.crafts) != 1 {
		t.Fatalf("submitted %d craft requests, want 1", len(bot.crafts))
	}
	if bot.crafts[0].RecipeNetworkID != 301 {
		t.Errorf("copy used recipe %d, want the cartography recipe 301", bot.crafts[0].RecipeNetworkID)
	}
}

// TestStationCraftFailsWhenTheStationIsMissing keeps the block search honest: a
// bag full of diamonds is not a smithing table, and the bot must say so rather
// than click whatever is nearby.
func TestStationCraftFailsWhenTheStationIsMissing(t *testing.T) {
	t.Parallel()

	bot := smithingWorld(t)
	// Replace the smithing table with a wall. A block that is merely solid is
	// what a solidity-based search would have accepted.
	bot.placeBlock(0, 0, 0, "stone")
	bot.placeBlock(1, 0, 0, "stone")
	bot.placeBlock(0, 0, 1, "stone")

	_, ok := newTestManager(bot).UpgradeToNetherite(t.Context())
	if ok {
		t.Fatal("an upgrade was reported with no smithing table in reach")
	}
	if len(bot.clicks) != 0 {
		t.Errorf("clicked %v; a wall is not a smithing table", bot.clicks)
	}
	if len(bot.crafts) != 0 {
		t.Error("a craft was submitted with no station open")
	}
}

// TestStationCraftFailsWhenTheServerNeverOpensTheWindow is the bug the furnace
// package documents: staging into a window the server never opened. If the
// window never arrives there must be no staging and no craft at all.
func TestStationCraftFailsWhenTheServerNeverOpensTheWindow(t *testing.T) {
	t.Parallel()

	bot := smithingWorld(t)
	bot.openFails = true

	_, ok := newTestManager(bot).UpgradeToNetherite(t.Context())
	if ok {
		t.Fatal("a craft was reported with no open window")
	}
	if len(bot.placed) != 0 {
		t.Errorf("staged %d stacks into a window the server never opened", len(bot.placed))
	}
	if len(bot.closed) != 0 {
		t.Errorf("closed %v; nothing was opened, so nothing should be closed", bot.closed)
	}
}

// TestStationCraftClosesTheWindowWhenStagingFails keeps the session from being
// left open server-side. A station left open desyncs the next open, so the
// close has to happen on the failure path too.
func TestStationCraftClosesTheWindowWhenStagingFails(t *testing.T) {
	t.Parallel()

	bot := smithingWorld(t)
	bot.placeErr = errRejected

	_, ok := newTestManager(bot).UpgradeToNetherite(t.Context())
	if ok {
		t.Fatal("a craft was reported after a rejected staging")
	}
	if len(bot.crafts) != 0 {
		t.Error("a craft was submitted after staging failed")
	}
	if len(bot.closed) != 1 || bot.closed[0] != bot.windowID {
		t.Errorf("closed = %v, want the assigned window %d closed once", bot.closed, bot.windowID)
	}
}

// TestStationCraftReportsARejectedCraft is the server saying no. The bot has to
// relay that failure rather than counting its own attempts as upgrades.
func TestStationCraftReportsARejectedCraft(t *testing.T) {
	t.Parallel()

	bot := smithingWorld(t)
	bot.craftErr = errRejected
	bot.resultItem = ""

	_, ok := newTestManager(bot).UpgradeToNetherite(t.Context())
	if ok {
		t.Fatal("a rejected craft was reported as a success")
	}
	if len(bot.closed) != 1 {
		t.Errorf("closed = %v, want the window closed after a rejection", bot.closed)
	}
}

// TestStationCraftReportsARefusedClick keeps the interactor's refusal visible.
func TestStationCraftReportsARefusedClick(t *testing.T) {
	t.Parallel()

	bot := smithingWorld(t)
	bot.clickOK = false
	bot.clickWhy = "smithing_table tidak merespons klik"

	_, ok := newTestManager(bot).UpgradeToNetherite(t.Context())
	if ok {
		t.Fatal("a refused click was reported as a craft")
	}
	if len(bot.closed) != 0 {
		t.Errorf("closed %v, but the server never opened anything", bot.closed)
	}
}

// TestStonecutterFlowUsesTheStonecutterBlock is 4.6 end to end, against a world
// that only has a stonecutter. The result must be stone bricks and the request
// must carry the stonecutter recipe.
func TestStonecutterFlowUsesTheStonecutterBlock(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.pos = mgl32Zero()
	bot.placeBlock(2, 0, 0, "stonecutter")
	bot.put(2, "minecraft:stone", 32)
	bot.recipes = stonecutRecipes()
	bot.resultItem = "stone_bricks"

	plan, ok := newTestManager(bot).Stonecut(t.Context(), "stone_bricks")
	if !ok {
		t.Fatal("Stonecut() failed on a world set up for it")
	}
	if plan.ResultName != "stone_bricks" {
		t.Errorf("result = %q, want stone_bricks", plan.ResultName)
	}
	if len(bot.placed) != 1 || bot.placed[0].slot != 0 {
		t.Errorf("placed = %+v, want one block into the stonecutter input slot 0", bot.placed)
	}
	if bot.crafts[0].RecipeNetworkID != 201 {
		t.Errorf("recipe = %d, want the stonecutter recipe 201", bot.crafts[0].RecipeNetworkID)
	}
}

// TestLoomFlowSendsAPatternNotARecipe covers the one station that is not
// recipe-driven on the wire. The request must carry the pattern identifier and
// no recipe network ID, because CraftLoomRecipeStackRequestAction takes a
// pattern string and a vanilla CraftRecipeStackRequestAction would be rejected.
func TestLoomFlowSendsAPatternNotARecipe(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.pos = mgl32Zero()
	bot.placeBlock(0, 0, 3, "minecraft:loom")
	bot.put(1, "minecraft:white_banner", 3)
	bot.put(6, "minecraft:red_dye", 5)
	bot.resultItem = "red_banner"

	plan, ok := newTestManager(bot).ApplyBannerPattern(t.Context(), "minecraft:globe")
	if !ok {
		t.Fatal("ApplyBannerPattern() failed on a world set up for it")
	}
	if plan.Pattern != "minecraft:globe" {
		t.Errorf("plan pattern = %q", plan.Pattern)
	}
	req := bot.crafts[0]
	if req.Pattern != "minecraft:globe" {
		t.Errorf("craft request pattern = %q, want minecraft:globe", req.Pattern)
	}
	if req.RecipeNetworkID != 0 {
		t.Errorf("craft request carried recipe %d; the loom sends a pattern, not a recipe", req.RecipeNetworkID)
	}
	if len(bot.placed) != 2 {
		t.Fatalf("placed %d stacks, want banner + dye: %+v", len(bot.placed), bot.placed)
	}
	if bot.placed[0].slot != 0 || bot.placed[1].slot != 1 {
		t.Errorf("staged into slots %d and %d, want the loom input 0 and dye 1",
			bot.placed[0].slot, bot.placed[1].slot)
	}
}

// TestFindStationPicksTheNearestOfItsOwnKind keeps a world full of every station
// from making the bot walk across the room to the wrong one.
func TestFindStationPicksTheNearestOfItsOwnKind(t *testing.T) {
	t.Parallel()

	bot := smithingWorld(t)
	bot.placeBlock(6, 0, 0, "loom")
	bot.placeBlock(7, 0, 0, "stonecutter")
	bot.placeBlock(4, 0, 0, "smithing_table")

	pos, ok := newTestManager(bot).FindStation(recipe.KindSmithingTable)
	if !ok {
		t.Fatal("no smithing table found")
	}
	if pos.X() != 0 {
		t.Errorf("found the smithing table at x=%d, want the nearest one at x=0", pos.X())
	}
	loom, ok := newTestManager(bot).FindStation(recipe.KindLoom)
	if !ok {
		t.Fatal("no loom found")
	}
	if loom.X() != 6 {
		t.Errorf("found the loom at x=%d, want x=6", loom.X())
	}
}

// TestStationCraftLeavesTheBagAloneWhenThePlanFails is the no-optimism rule.
// A missing ingredient must fail before a single stack moves.
func TestStationCraftLeavesTheBagAloneWhenThePlanFails(t *testing.T) {
	t.Parallel()

	bot := smithingWorld(t)
	// Drop the template: the plan cannot be built, so nothing should be staged
	// or clicked even though a smithing table is right there.
	delete(bot.items, 0)

	_, ok := newTestManager(bot).UpgradeToNetherite(t.Context())
	if ok {
		t.Fatal("an upgrade was reported with no template")
	}
	if len(bot.placed) != 0 {
		t.Errorf("staged %d stacks with no plan", len(bot.placed))
	}
	if len(bot.clicks) != 0 {
		t.Errorf("clicked %v with no plan", bot.clicks)
	}
}

// TestPlanExposesTheContainerIDsForTheBotSideSeam pins the data the bot-side
// CraftStationRecipe builds its consume actions from. The plan carries the
// protocol container of every staged input and of the result, so the wire
// addressing lives in one place and the helper does not have to re-derive it.
func TestPlanExposesTheContainerIDsForTheBotSideSeam(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "netherite_upgrade_smithing_template", Count: 1},
		{Slot: 4, Name: "diamond_sword", Count: 1},
		{Slot: 7, Name: "netherite_ingot", Count: 1},
	}
	plan, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
	if err != nil {
		t.Fatalf("PlanNetheriteUpgrade() error = %v", err)
	}
	got := plan.InputContainerIDs()
	want := []byte{
		protocol.ContainerSmithingTableTemplate,
		protocol.ContainerSmithingTableInput,
		protocol.ContainerSmithingTableMaterial,
	}
	if len(got) != len(want) {
		t.Fatalf("InputContainerIDs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("input container %d = %d, want %d", i, got[i], want[i])
		}
	}
	if plan.ResultContainerID() != protocol.ContainerSmithingTableResultPreview {
		t.Errorf("ResultContainerID() = %d, want %d",
			plan.ResultContainerID(), protocol.ContainerSmithingTableResultPreview)
	}
}
