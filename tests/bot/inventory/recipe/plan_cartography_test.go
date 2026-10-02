package recipe_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/inventory/recipe"
)

// cartographyRecipes is what a server advertises for a cartography table.
func cartographyRecipes() []recipe.StationRecipe {
	return []recipe.StationRecipe{
		{NetworkID: 301, Block: "cartography_table", ResultName: "map", ResultCount: 1},
		{NetworkID: 302, Block: "crafting_table", ResultName: "map", ResultCount: 1},
	}
}

// TestMapCopyStagesAMapAndPaper is acceptance 4.8. The cartography table is a
// two-input station: the map, and the reagent that changes it.
func TestMapCopyStagesAMapAndPaper(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 3, Name: "map", Count: 1},
		{Slot: 8, Name: "paper", Count: 12},
	}
	plan, err := recipe.PlanCartography(inv, cartographyRecipes(), recipe.CartographyCopy)
	if err != nil {
		t.Fatalf("PlanCartography() error = %v", err)
	}
	if plan.Station.Kind != recipe.KindCartographyTable {
		t.Fatalf("plan station = %q, want cartography_table", plan.Station.Kind)
	}
	if plan.RecipeNetworkID != 301 {
		t.Errorf("RecipeNetworkID = %d, want the cartography-table recipe 301", plan.RecipeNetworkID)
	}
	if plan.ResultName != "map" || plan.ResultCount != 1 {
		t.Errorf("result = %q x%d, want map x1", plan.ResultName, plan.ResultCount)
	}
	if len(plan.Inputs) != 2 {
		t.Fatalf("plan has %d inputs, want map + paper: %+v", len(plan.Inputs), plan.Inputs)
	}
	in, ok := plan.Input(recipe.RoleInput)
	if !ok || in.ItemName != "map" || in.Count != 1 || in.SourceSlot != 3 {
		t.Errorf("input = %+v, ok=%v; want one map from slot 3", in, ok)
	}
	add, ok := plan.Input(recipe.RoleAddition)
	if !ok || add.ItemName != "paper" || add.Count != 1 || add.SourceSlot != 8 {
		t.Errorf("addition = %+v, ok=%v; want one paper from slot 8", add, ok)
	}
}

// TestMapCopyUsesTheCartographyRecipeNotTheCraftingTableOne matters because map
// and paper also make a map at a crafting table. A planner that ignored the
// station would never open a cartography table at all.
func TestMapCopyUsesTheCartographyRecipeNotTheCraftingTableOne(t *testing.T) {
	t.Parallel()

	recipes := []recipe.StationRecipe{
		{NetworkID: 302, Block: "crafting_table", ResultName: "map", ResultCount: 1},
		{NetworkID: 301, Block: "cartography_table", ResultName: "map", ResultCount: 1},
	}
	inv := recipe.Inventory{
		{Slot: 0, Name: "map", Count: 1},
		{Slot: 1, Name: "paper", Count: 1},
	}
	plan, err := recipe.PlanCartography(inv, recipes, recipe.CartographyCopy)
	if err != nil {
		t.Fatalf("PlanCartography() error = %v", err)
	}
	if plan.RecipeNetworkID != 301 {
		t.Fatalf("RecipeNetworkID = %d, want the cartography-table recipe 301", plan.RecipeNetworkID)
	}
}

// TestMapLockNeedsItsOwnReagent keeps the operations apart. Every cartography op
// ends in a map, so a planner that only looked at the result would happily lock
// a map with paper.
func TestMapLockNeedsItsOwnReagent(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "filled_map", Count: 1},
		{Slot: 1, Name: "paper", Count: 8},
	}
	_, err := recipe.PlanCartography(inv, cartographyRecipes(), recipe.CartographyLock)
	if err == nil {
		t.Fatal("a lock was planned with paper instead of a glass pane")
	}
	if !strings.Contains(err.Error(), "glass_pane") {
		t.Errorf("error = %v, want it to name the glass pane", err)
	}
}

// TestMapCloneNeedsAFilledMap is the other half of keeping the ops apart: a
// clone consumes a filled map, and an empty map in the bag is not one.
func TestMapCloneNeedsAFilledMap(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "map", Count: 1},
		{Slot: 1, Name: "redstone", Count: 9},
	}
	_, err := recipe.PlanCartography(inv, cartographyRecipes(), recipe.CartographyClone)
	if err == nil {
		t.Fatal("a clone was planned from an empty map")
	}
	if !strings.Contains(err.Error(), "filled_map") {
		t.Errorf("error = %v, want it to name the filled map", err)
	}
}

// TestMapCopyNeedsPaper keeps the reagent requirement from being optional.
func TestMapCopyNeedsPaper(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{{Slot: 0, Name: "map", Count: 1}}
	_, err := recipe.PlanCartography(inv, cartographyRecipes(), recipe.CartographyCopy)
	if err == nil {
		t.Fatal("a copy was planned with no paper")
	}
	if !strings.Contains(err.Error(), "paper") {
		t.Errorf("error = %v, want it to name the paper", err)
	}
}

// TestUnknownCartographyActionIsRejected keeps a typo from silently becoming a
// copy. The action is the only thing distinguishing copy from extend on the
// wire, so guessing one is exactly the kind of quiet lie this package avoids.
func TestUnknownCartographyActionIsRejected(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "map", Count: 1},
		{Slot: 1, Name: "paper", Count: 1},
	}
	_, err := recipe.PlanCartography(inv, cartographyRecipes(), recipe.CartographyAction("teleport"))
	if err == nil {
		t.Fatal("an unknown cartography action produced a plan")
	}
}

// TestCartographyStagesOneReagentNotTheWholeStack is a resource rule: paper
// stacks to 64 and sending all of them into the station would leave 63 stranded.
func TestCartographyStagesOneReagentNotTheWholeStack(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "map", Count: 1},
		{Slot: 1, Name: "paper", Count: 64},
	}
	plan, err := recipe.PlanCartography(inv, cartographyRecipes(), recipe.CartographyCopy)
	if err != nil {
		t.Fatalf("PlanCartography() error = %v", err)
	}
	if plan.Inputs[1].Count != 1 {
		t.Errorf("staged %d paper, want 1", plan.Inputs[1].Count)
	}
}

// TestCartographyOpsCatalogueIsConsistent keeps the operation table honest: two
// inputs, one result, and every action named exactly once.
func TestCartographyOpsCatalogueIsConsistent(t *testing.T) {
	t.Parallel()

	ops := recipe.CartographyOps()
	if len(ops) == 0 {
		t.Fatal("the cartography catalogue is empty")
	}
	seen := make(map[recipe.CartographyAction]bool, len(ops))
	for _, op := range ops {
		if seen[op.Action] {
			t.Errorf("action %q is listed twice", op.Action)
		}
		seen[op.Action] = true
		if len(op.Inputs) != 2 {
			t.Errorf("action %q has %d inputs, want 2", op.Action, len(op.Inputs))
		}
		if op.Result == "" {
			t.Errorf("action %q has no result", op.Action)
		}
		for _, in := range op.Inputs {
			if in == "" {
				t.Errorf("action %q has an empty input", op.Action)
			}
		}
		if _, ok := recipe.FindCartographyOp(op.Action); !ok {
			t.Errorf("FindCartographyOp(%q) did not find its own entry", op.Action)
		}
	}
	if _, ok := recipe.FindCartographyOp(recipe.CartographyAction("teleport")); ok {
		t.Error("FindCartographyOp invented an operation")
	}
	for _, want := range []recipe.CartographyAction{
		recipe.CartographyCopy, recipe.CartographyExtend,
		recipe.CartographyLock, recipe.CartographyClone,
	} {
		if !seen[want] {
			t.Errorf("action %q is missing from the catalogue", want)
		}
	}
}
