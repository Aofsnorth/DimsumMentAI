package bot

import (
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// TestCanCraftRecipe guards the ingredient-matching path used by
// ListCraftableItems. The previous stub returned false for every descriptor,
// so every recipe was reported as missing all ingredients; these cases would
// fail against that stub and pass once the canonical matcher is reused.
func TestCanCraftRecipe(t *testing.T) {
	t.Parallel()

	// itemNames mirrors the Bedrock split where the recipe ingredient's runtime
	// ID differs from the inventory item's runtime ID (oak_wood vs oak_log).
	itemNames := map[int32]string{
		-212: "oak_wood", // recipe ingredient runtime ID
		17:   "oak_log",  // inventory runtime ID
		5:    "stick",
	}

	tests := []struct {
		name    string
		recipe  RecipeInfo
		inv     map[uint32]protocol.ItemStack
		wantOK  bool
		wantHas string // substring expected in Missing when !wantOK
	}{
		{
			name: "identical name and enough count",
			recipe: RecipeInfo{
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:stick"}, Count: 2},
				},
			},
			inv:    map[uint32]protocol.ItemStack{0: {ItemType: protocol.ItemType{NetworkID: 5}, Count: 4}},
			wantOK: true,
		},
		{
			name: "canonical oak_wood ingredient matches oak_log in inventory",
			recipe: RecipeInfo{
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_wood"}, Count: 1},
				},
			},
			inv:    map[uint32]protocol.ItemStack{0: {ItemType: protocol.ItemType{NetworkID: 17}, Count: 4}},
			wantOK: true,
		},
		{
			name: "missing ingredient entirely",
			recipe: RecipeInfo{
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:stick"}, Count: 1},
				},
			},
			inv:     map[uint32]protocol.ItemStack{0: {ItemType: protocol.ItemType{NetworkID: 17}, Count: 4}},
			wantOK:  false,
			wantHas: "Stick x1",
		},
		{
			name: "insufficient count reports needed amount",
			recipe: RecipeInfo{
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:stick"}, Count: 3},
				},
			},
			inv:     map[uint32]protocol.ItemStack{0: {ItemType: protocol.ItemType{NetworkID: 5}, Count: 1}},
			wantOK:  false,
			wantHas: "Stick x3",
		},
		{
			name: "one of two ingredients missing",
			recipe: RecipeInfo{
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_log"}, Count: 1}, // have oak_log
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:stick"}, Count: 1},   // missing stick
				},
			},
			inv:     map[uint32]protocol.ItemStack{0: {ItemType: protocol.ItemType{NetworkID: 17}, Count: 2}},
			wantOK:  false,
			wantHas: "Stick x1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &Bot{}
			ok, missing := b.canCraftRecipe(tc.recipe, tc.inv, itemNames)
			if ok != tc.wantOK {
				t.Fatalf("canCraftRecipe ok = %v, want %v (missing=%q)", ok, tc.wantOK, missing)
			}
			if !tc.wantOK {
				if missing == "" {
					t.Fatalf("expected non-empty missing string, got empty")
				}
				if tc.wantHas != "" && !strings.Contains(missing, tc.wantHas) {
					t.Fatalf("missing = %q, want substring %q", missing, tc.wantHas)
				}
			}
		})
	}
}

// TestListCraftableItems_TableFilter covers the crafting-table gate: a recipe
// requiring a crafting table is excluded when the bot has no table available.
func TestListCraftableItems_TableFilter(t *testing.T) {
	t.Parallel()
	b := &Bot{
		RecipesByNetID: map[uint32]RecipeInfo{
			1: {
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:stick"}, Count: 1},
				},
				Output: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 100}},
				Block:  "",
			},
			2: {
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:stick"}, Count: 1},
				},
				Output: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 101}},
				Block:  "crafting_table",
			},
		},
		InventoryMap: map[uint32]protocol.ItemStack{0: {ItemType: protocol.ItemType{NetworkID: 5}, Count: 4}},
		ItemNames:    map[int32]string{5: "stick", 100: "wooden_sword", 101: "bow"},
	}

	if items := b.ListCraftableItems(false); len(items) != 1 {
		t.Fatalf("without table: expected 1 item, got %d", len(items))
	}
	if items := b.ListCraftableItems(true); len(items) != 2 {
		t.Fatalf("with table: expected 2 items, got %d", len(items))
	}
}
