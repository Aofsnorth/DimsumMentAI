package recipe_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/inventory/recipe"
)

// stonecutRecipes is what a server advertises: the stonecutter's own recipes
// plus the crafting-table recipe for the same output.
func stonecutRecipes() []recipe.StationRecipe {
	return []recipe.StationRecipe{
		{NetworkID: 201, Block: "stonecutter", ResultName: "stone_bricks", ResultCount: 1},
		{NetworkID: 202, Block: "stonecutter", ResultName: "stone_slab", ResultCount: 2},
		{NetworkID: 203, Block: "crafting_table", ResultName: "stone_bricks", ResultCount: 1},
		{NetworkID: 204, Block: "crafting_table", ResultName: "stone_brick_slab", ResultCount: 2},
	}
}

func stoneBag() recipe.Inventory {
	return recipe.Inventory{
		{Slot: 2, Name: "stone", Count: 32},
		{Slot: 5, Name: "cobblestone", Count: 11},
	}
}

// TestStonecutPlansStoneIntoStoneBricks is acceptance 4.6. The stonecutter is a
// one-input station: a single block in, one cut variant out.
func TestStonecutPlansStoneIntoStoneBricks(t *testing.T) {
	t.Parallel()

	plan, err := recipe.PlanStonecut(stoneBag(), stonecutRecipes(), "stone_bricks")
	if err != nil {
		t.Fatalf("PlanStonecut() error = %v", err)
	}
	if plan.Station.Kind != recipe.KindStonecutter {
		t.Fatalf("plan station = %q, want stonecutter", plan.Station.Kind)
	}
	if plan.ResultName != "stone_bricks" {
		t.Errorf("ResultName = %q, want stone_bricks", plan.ResultName)
	}
	if plan.ResultCount != 1 {
		t.Errorf("ResultCount = %d, want 1", plan.ResultCount)
	}
	if plan.RecipeNetworkID != 201 {
		t.Errorf("RecipeNetworkID = %d, want the stonecutter recipe 201", plan.RecipeNetworkID)
	}
	if len(plan.Inputs) != 1 {
		t.Fatalf("plan has %d inputs, want 1: %+v", len(plan.Inputs), plan.Inputs)
	}
	in := plan.Inputs[0]
	if in.Slot.Role != recipe.RoleInput || in.ItemName != "stone" || in.Count != 1 {
		t.Errorf("input = %+v, want one stone into the input slot", in)
	}
	if in.SourceSlot != 2 {
		t.Errorf("input sourced from slot %d, want the stone stack at 2", in.SourceSlot)
	}
}

// TestStonecutUsesTheStonecutterRecipeNotTheCraftingTableOne is the whole point
// of 4.6. Stone bricks can also be made with four bricks in a crafting table, so
// a planner that looks recipes up by output alone would hand the bot a
// crafting-table recipe, the bot would never touch the stonecutter, and the
// acceptance criterion would be satisfied by a log line rather than by a cut.
func TestStonecutUsesTheStonecutterRecipeNotTheCraftingTableOne(t *testing.T) {
	t.Parallel()

	// Deliberately serve the crafting-table recipe FIRST.
	recipes := []recipe.StationRecipe{
		{NetworkID: 203, Block: "crafting_table", ResultName: "stone_bricks", ResultCount: 1},
		{NetworkID: 201, Block: "stonecutter", ResultName: "stone_bricks", ResultCount: 1},
	}
	plan, err := recipe.PlanStonecut(stoneBag(), recipes, "stone_bricks")
	if err != nil {
		t.Fatalf("PlanStonecut() error = %v", err)
	}
	if plan.RecipeNetworkID != 201 {
		t.Fatalf("RecipeNetworkID = %d, want the stonecutter recipe 201", plan.RecipeNetworkID)
	}
}

// TestStonecutRejectsAnOutputTheStonecutterCannotMake keeps the planner from
// inventing transforms. Anything the catalogue does not list is refused rather
// than attempted, because a refused plan is honest and an attempted one is a
// server rejection plus a lost inventory slot.
func TestStonecutRejectsAnOutputTheStonecutterCannotMake(t *testing.T) {
	t.Parallel()

	for _, result := range []string{"diamond", "bedrock", "stonecutter", "  "} {
		_, err := recipe.PlanStonecut(stoneBag(), stonecutRecipes(), result)
		if err == nil {
			t.Errorf("a plan was produced for %q, which no stonecutter recipe makes", result)
		}
	}
}

// TestStonecutNeedsTheInput keeps a plan from staging nothing and asking the
// server to craft from an empty input slot.
func TestStonecutNeedsTheInput(t *testing.T) {
	t.Parallel()

	_, err := recipe.PlanStonecut(recipe.Inventory{}, stonecutRecipes(), "stone_bricks")
	if err == nil {
		t.Fatal("a plan was produced from an empty bag")
	}
	if !strings.Contains(err.Error(), "stone") {
		t.Errorf("error = %v, want it to name the missing input", err)
	}
}

// TestStonecutNeedsAServerRecipe is the counterpart: a catalogue entry is not
// enough. Without the recipe network ID there is nothing to send.
func TestStonecutNeedsAServerRecipe(t *testing.T) {
	t.Parallel()

	_, err := recipe.PlanStonecut(stoneBag(), nil, "stone_bricks")
	if err == nil {
		t.Fatal("a plan was built with no stonecutter recipe from the server")
	}
}

// TestStonecutTakesOneBlockNotTheWholeStack is a resource rule. Sending all 32
// stones into a one-input station would cut one block and strand 31 in the
// station window.
func TestStonecutTakesOneBlockNotTheWholeStack(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{{Slot: 0, Name: "stone", Count: 64}}
	plan, err := recipe.PlanStonecut(inv, stonecutRecipes(), "stone_bricks")
	if err != nil {
		t.Fatalf("PlanStonecut() error = %v", err)
	}
	if plan.Inputs[0].Count != 1 {
		t.Errorf("staged %d stones, want 1", plan.Inputs[0].Count)
	}
}

// TestStonecutResultCountFollowsTheServerRecipe keeps a multi-output cut honest.
// A stone slab cut yields two slabs; the bot has to expect two before it decides
// the craft worked.
func TestStonecutResultCountFollowsTheServerRecipe(t *testing.T) {
	t.Parallel()

	plan, err := recipe.PlanStonecut(stoneBag(), stonecutRecipes(), "stone_slab")
	if err != nil {
		t.Fatalf("PlanStonecut() error = %v", err)
	}
	if plan.ResultCount != 2 {
		t.Errorf("ResultCount = %d, want the recipe's 2", plan.ResultCount)
	}
	if plan.RecipeNetworkID != 202 {
		t.Errorf("RecipeNetworkID = %d, want the stonecutter slab recipe 202", plan.RecipeNetworkID)
	}
}

// TestStonecutsCatalogueStaysCutOnly guards the catalogue itself: every entry
// must be a real one-block-in transform, no transform may be listed twice, and
// the acceptance case has to be in it. A catalogue that quietly grew
// "stone_bricks from four bricks" would let 4.6 be satisfied without the
// stonecutter. Two entries may share a result — smooth stone comes from both
// stone and cobblestone — so uniqueness is on the pair, and
// TestStonecutResolvesAReachableResultFromEitherInput covers the overlap.
func TestStonecutsCatalogueStaysCutOnly(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)
	for _, sc := range recipe.Stonecuts() {
		if sc.Input == "" || sc.Result == "" {
			t.Errorf("catalogue entry %+v has an empty side", sc)
		}
		if sc.Input == sc.Result {
			t.Errorf("catalogue entry %+v cuts an item into itself", sc)
		}
		key := sc.Input + "->" + sc.Result
		if seen[key] {
			t.Errorf("catalogue lists %q twice", key)
		}
		seen[key] = true
	}
	if _, ok := recipe.FindStonecut("stone", "stone_bricks"); !ok {
		t.Error("the acceptance case stone -> stone_bricks is missing from the catalogue")
	}
	if _, ok := recipe.FindStonecut("stone", "diamond_block"); ok {
		t.Error("FindStonecut invented a transform")
	}
}

// TestStonecutResolvesAReachableResultFromEitherInput covers the catalogue
// overlap: smooth stone is cut from stone and also from cobblestone, and the
// planner has to find whichever one the bag actually holds rather than giving
// up on the first catalogue entry.
func TestStonecutResolvesAReachableResultFromEitherInput(t *testing.T) {
	t.Parallel()

	if _, ok := recipe.FindStonecut("cobblestone", "smooth_stone"); !ok {
		t.Skip("the catalogue does not offer cobblestone -> smooth_stone")
	}
	inv := recipe.Inventory{{Slot: 6, Name: "cobblestone", Count: 9}}
	plan, err := recipe.PlanStonecut(inv, []recipe.StationRecipe{
		{NetworkID: 210, Block: "stonecutter", ResultName: "smooth_stone"},
	}, "smooth_stone")
	if err != nil {
		t.Fatalf("PlanStonecut() error = %v", err)
	}
	if plan.Inputs[0].ItemName != "cobblestone" {
		t.Errorf("input = %q, want cobblestone", plan.Inputs[0].ItemName)
	}
}

// TestStonecutAcceptsANamespacedResultName is the same normalisation every other
// lookup gets: the caller may say "minecraft:stone_bricks".
func TestStonecutAcceptsANamespacedResultName(t *testing.T) {
	t.Parallel()

	plan, err := recipe.PlanStonecut(stoneBag(), stonecutRecipes(), "minecraft:stone_bricks")
	if err != nil {
		t.Fatalf("PlanStonecut() error = %v", err)
	}
	if plan.ResultName != "stone_bricks" {
		t.Errorf("ResultName = %q, want the bare name", plan.ResultName)
	}
}
