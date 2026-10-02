package player_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/network/player"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// A smithing table was unreachable no matter what the bot carried.
//
// handleCraftingData walked the shapeless and shaped lists and stopped. The
// smithing recipes arrive in the same packet, in their own fields, and nothing
// read them — so no smithing recipe ever entered the recipe tables, and a
// planner holding a netherite upgrade template and a diamond pickaxe was told
// the server advertised no such recipe. On every run. With the right items.
//
// These tests pin the indexing. The one that matters is the first: it is the
// difference between a smithing table that works and one the bot cannot see.

// newRecipeBot builds a bot with a name table big enough to resolve the output
// item of the recipes below, which is what the indexer looks the name up in.
func newRecipeBot(names map[int32]string) *bot.Bot {
	return &bot.Bot{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ItemNames: names,
	}
}

// deliver routes a packet through the real dispatcher, so the test exercises the
// same path the network goroutine does rather than calling a handler directly.
func deliver(t *testing.T, b *bot.Bot, pk packet.Packet) {
	t.Helper()
	if !player.HandlePlayerPacket(b, pk) {
		t.Fatalf("the dispatcher did not handle %T", pk)
	}
}

// descriptor builds a recipe ingredient.
//
// A DefaultItemDescriptor names the item rather than carrying a network ID, which
// is the shape a real CraftingData packet delivers. The network ID in the test's
// name table is what the indexer resolves the descriptor's name through, so the
// two have to agree or the indexer finds no name to register.
func descriptor(name string, count int32) protocol.ItemDescriptorCount {
	return protocol.ItemDescriptorCount{
		Descriptor: &protocol.DefaultItemDescriptor{Name: name},
		Count:      count,
	}
}

// TestASmithingTransformRecipeIsIndexed is the acceptance for 4.5. A diamond
// pickaxe plus a netherite upgrade template has to be findable by its result.
func TestASmithingTransformRecipeIsIndexed(t *testing.T) {
	t.Parallel()

	const (
		netheritePickaxeNetID int32  = 700
		recipeNetID           uint32 = 9001
	)
	b := newRecipeBot(map[int32]string{netheritePickaxeNetID: "minecraft:netherite_pickaxe"})

	deliver(t, b, &packet.CraftingData{
		SmithingTransformRecipes: []protocol.SmithingTransformRecipe{{
			RecipeNetworkID: recipeNetID,
			Template:        descriptor("minecraft:netherite_upgrade", 1),
			Base:            descriptor("minecraft:diamond_pickaxe", 1),
			Addition:        descriptor("minecraft:netherite_ingot", 1),
			Result:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: netheritePickaxeNetID}, Count: 1},
			Block:           "smithing_table",
		}},
	})

	byName, ok := b.Recipes["minecraft:netherite_pickaxe"]
	if !ok {
		t.Fatalf("the smithing result was not indexed by name; the bot would be told it owns no such recipe")
	}
	if byName != recipeNetID {
		t.Errorf("name index points at recipe %d, want %d", byName, recipeNetID)
	}

	info, ok := b.RecipesByNetID[recipeNetID]
	if !ok {
		t.Fatal("the smithing recipe is not in RecipesByNetID, so nothing can address it by network ID")
	}
	if info.Block != "smithing_table" {
		t.Errorf("recipe block = %q, want smithing_table", info.Block)
	}
	if len(info.Ingredients) != 3 {
		t.Fatalf("recipe has %d ingredients, want 3 (template, base, addition)", len(info.Ingredients))
	}
	// The order is the meaning: a smithing transform is not interchangeable, so
	// template and base swapped is a different recipe.
	if got := descriptorName(info.Ingredients[0]); got != "minecraft:netherite_upgrade" {
		t.Errorf("first ingredient is %q, want the template", got)
	}
	if got := descriptorName(info.Ingredients[1]); got != "minecraft:diamond_pickaxe" {
		t.Errorf("second ingredient is %q, want the base", got)
	}
}

// descriptorName reads back the item name an ingredient names.
func descriptorName(d protocol.ItemDescriptorCount) string {
	if def, ok := d.Descriptor.(*protocol.DefaultItemDescriptor); ok {
		return def.Name
	}
	return ""
}

// TestATrimRecipeIsIndexedButNotNamed is the honest half. A trim recipe's result
// is the base item wearing a trim, and the protocol carries no output stack for
// it — so the output name is not knowable. The ingredients are recorded because
// they are real; the name is not recorded because inventing one would put an
// entry in a table the bot looks things up in, pointing at a result the server
// never described.
func TestATrimRecipeIsIndexedButNotNamed(t *testing.T) {
	t.Parallel()

	const trimRecipeNetID uint32 = 9002
	b := newRecipeBot(map[int32]string{})

	deliver(t, b, &packet.CraftingData{
		SmithingTrimRecipes: []protocol.SmithingTrimRecipe{{
			RecipeNetworkID: trimRecipeNetID,
			Template:        descriptor("minecraft:some_trim", 1),
			Base:            descriptor("minecraft:diamond_pickaxe", 1),
			Addition:        descriptor("minecraft:iron_ingot", 1),
			Block:           "smithing_table",
		}},
	})

	if _, ok := b.RecipesByNetID[trimRecipeNetID]; !ok {
		t.Error("the trim recipe's ingredients were not recorded, so the table cannot describe it at all")
	}
	// Nothing names a trim result, so no name may point at this recipe.
	for name, id := range b.Recipes {
		if id == trimRecipeNetID {
			t.Errorf("the trim recipe was registered under the name %q, but its result item is not knowable from the recipe", name)
		}
	}
}

// TestAZeroNetworkIDIsNotIndexed guards the collision. The protocol says a
// recipe network ID must never be 0, and a zero entry would sit in the same
// table every "no recipe" lookup falls back to.
func TestAZeroNetworkIDIsNotIndexed(t *testing.T) {
	t.Parallel()

	b := newRecipeBot(map[int32]string{1: "minecraft:netherite_pickaxe"})

	deliver(t, b, &packet.CraftingData{
		SmithingTransformRecipes: []protocol.SmithingTransformRecipe{{
			RecipeNetworkID: 0,
			Result:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 1}, Count: 1},
			Block:           "smithing_table",
		}},
	})

	if _, ok := b.RecipesByNetID[0]; ok {
		t.Error("a recipe with network ID 0 was indexed")
	}
	if _, ok := b.Recipes["minecraft:netherite_pickaxe"]; ok {
		t.Error("a recipe with network ID 0 was registered by name; 0 is the no-recipe value")
	}
}

// TestShapelessAndShapedRecipesStillIndex is the no-regression half. The smithing
// walk was added alongside the existing ones, and an existing recipe must not
// stop working because of it.
func TestShapelessAndShapedRecipesStillIndex(t *testing.T) {
	t.Parallel()

	b := newRecipeBot(map[int32]string{300: "minecraft:stick", 400: "minecraft:crafting_table"})

	deliver(t, b, &packet.CraftingData{
		ShapelessRecipes: []protocol.ShapelessRecipe{{
			RecipeNetworkID: 11,
			Input:           []protocol.ItemDescriptorCount{descriptor("minecraft:oak_planks", 2)},
			Output:          []protocol.ItemStack{{ItemType: protocol.ItemType{NetworkID: 300}, Count: 4}},
			Block:           "crafting_table",
		}},
		ShapedRecipes: []protocol.ShapedRecipe{{
			RecipeNetworkID: 12,
			Input:           []protocol.ItemDescriptorCount{descriptor("minecraft:oak_planks", 4)},
			Output:          []protocol.ItemStack{{ItemType: protocol.ItemType{NetworkID: 400}, Count: 1}},
			Block:           "crafting_table",
			Width:           2,
			Height:          2,
		}},
		SmithingTransformRecipes: []protocol.SmithingTransformRecipe{{
			RecipeNetworkID: 13,
			Result:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 700}, Count: 1},
			Block:           "smithing_table",
		}},
	})

	if id, ok := b.Recipes["minecraft:stick"]; !ok || id != 11 {
		t.Errorf("the shapeless recipe stopped indexing: %v (ok=%v)", id, ok)
	}
	if id, ok := b.Recipes["minecraft:crafting_table"]; !ok || id != 12 {
		t.Errorf("the shaped recipe stopped indexing: %v (ok=%v)", id, ok)
	}
	if info, ok := b.RecipesByNetID[12]; !ok || info.Width != 2 || info.Height != 2 {
		t.Errorf("the shaped recipe lost its dimensions: %+v", info)
	}
}
