package bot_test

import (
	"testing"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// TestCanCraftRecipeAggregatesAcrossStacks is the regression guard for the
// one-stack-per-ingredient check.
//
// The planner used to ask whether a single stack held the whole ingredient
// count, so a recipe needing four planks was reported unaffordable by a bot
// carrying two stacks of two. It also never deducted anything, which let two
// ingredients that resolved to the same item both claim one stack.
func TestCanCraftRecipeAggregatesAcrossStacks(t *testing.T) {
	t.Parallel()

	names := map[int32]string{5: "oak_planks"}
	b := &bot.Bot{}
	recipe := bot.RecipeInfo{Ingredients: []protocol.ItemDescriptorCount{
		{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_planks"}, Count: 4},
	}}

	inv := map[uint32]protocol.ItemStack{
		0: {ItemType: protocol.ItemType{NetworkID: 5}, Count: 2},
		1: {ItemType: protocol.ItemType{NetworkID: 5}, Count: 2},
	}
	if ok, missing := b.CanCraftRecipe(recipe, inv, names); !ok {
		t.Fatalf("4 planks across two stacks of 2 reported as uncraftable (missing=%q)", missing)
	}
}

// TestCanCraftRecipeConsumesWhatItMatches keeps the other direction honest: two
// ingredients needing the same item must not both count one stack as theirs.
func TestCanCraftRecipeConsumesWhatItMatches(t *testing.T) {
	t.Parallel()

	names := map[int32]string{5: "oak_planks"}
	b := &bot.Bot{}
	recipe := bot.RecipeInfo{Ingredients: []protocol.ItemDescriptorCount{
		{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_planks"}, Count: 2},
		{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_planks"}, Count: 2},
	}}

	inv := map[uint32]protocol.ItemStack{
		0: {ItemType: protocol.ItemType{NetworkID: 5}, Count: 2},
	}
	if ok, _ := b.CanCraftRecipe(recipe, inv, names); ok {
		t.Fatal("two ingredients needing 4 planks were satisfied by a single stack of 2; " +
			"the second ingredient reused the first one's stack")
	}
}
