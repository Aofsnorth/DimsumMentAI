package player_test

import (
	"testing"

	"bedrock-ai/internal/bot/network/player"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

var _ = player.HandlePlayerPacket

func shapedOutput(netID int32, recipeNetID uint32) protocol.ShapedRecipe {
	return protocol.ShapedRecipe{
		Width:           1,
		Height:          1,
		Input:           []protocol.ItemDescriptorCount{descriptor("minecraft:iron_ingot", 1)},
		Output:          []protocol.ItemStack{{ItemType: protocol.ItemType{NetworkID: netID}, Count: 1}},
		Block:           "crafting_table",
		RecipeNetworkID: recipeNetID,
	}
}

// A follow-up CraftingData that does not ask for a clear must ADD to the book.
// Wiping on every packet erased the iron axe recipe on Geyser after a short
// re-send, and the bot answered "resep tidak diketahui: iron_axe".
func TestCraftingDataWithoutClearKeepsEarlierRecipes(t *testing.T) {
	t.Parallel()

	b := newRecipeBot(map[int32]string{10: "minecraft:iron_axe", 11: "minecraft:stone_bricks"})

	deliver(t, b, &packet.CraftingData{ShapedRecipes: []protocol.ShapedRecipe{shapedOutput(10, 100)}})
	deliver(t, b, &packet.CraftingData{ShapedRecipes: []protocol.ShapedRecipe{shapedOutput(11, 101)}})

	if got := b.GetRecipeCandidates("iron_axe"); len(got) != 1 || got[0] != 100 {
		t.Fatalf("iron_axe candidates = %v, want [100] after a non-clearing follow-up packet", got)
	}
	if got := b.GetRecipeCandidates("stone_bricks"); len(got) != 1 || got[0] != 101 {
		t.Fatalf("stone_bricks candidates = %v, want [101]", got)
	}
}

func TestCraftingDataWithClearReplacesRecipes(t *testing.T) {
	t.Parallel()

	b := newRecipeBot(map[int32]string{10: "minecraft:iron_axe", 11: "minecraft:stone_bricks"})

	deliver(t, b, &packet.CraftingData{ShapedRecipes: []protocol.ShapedRecipe{shapedOutput(10, 100)}})
	deliver(t, b, &packet.CraftingData{ClearRecipes: true, ShapedRecipes: []protocol.ShapedRecipe{shapedOutput(11, 101)}})

	if got := b.GetRecipeCandidates("iron_axe"); len(got) != 0 {
		t.Fatalf("iron_axe candidates = %v, want none after ClearRecipes", got)
	}
	if got := b.GetRecipeCandidates("stone_bricks"); len(got) != 1 {
		t.Fatalf("stone_bricks candidates = %v, want 1", got)
	}
}

func TestResentRecipeIsNotListedTwice(t *testing.T) {
	t.Parallel()

	b := newRecipeBot(map[int32]string{10: "minecraft:iron_axe"})
	pk := &packet.CraftingData{ShapedRecipes: []protocol.ShapedRecipe{shapedOutput(10, 100)}}

	deliver(t, b, pk)
	deliver(t, b, pk)

	if got := b.GetRecipeCandidates("iron_axe"); len(got) != 1 {
		t.Fatalf("iron_axe candidates = %v, want exactly one entry", got)
	}
}
