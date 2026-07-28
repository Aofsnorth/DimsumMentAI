package bot

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func TestItemNameMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		itemName, ingredientName string
		want                     bool
	}{
		{"oak_log", "oak_log", true},
		{"minecraft:oak_log", "oak_log", true},
		{"oak_log", "minecraft:oak_log", true},
		{"Oak Log", "oak_log", true},
		{"oak wood", "oak log", true}, // Bedrock recipe name vs. inventory name
		{"minecraft:oak_wood", "oak_log", true},
		{"oak_log", "spruce_log", false},
		{"oak_planks", "oak_log", false},
		{"oak_planks", "warped_planks", true},
		{"warped_planks", "oak_planks", true},
		// Generic tag satisfied by a specific variant.
		{"oak_log", "log", true},
		{"spruce_log", "log", true},
		// A specific variant must NOT satisfy a different specific variant. The old
		// bidirectional containment let "oak_log" satisfy "dark_oak_log".
		{"oak_log", "dark_oak_log", false},
		{"dark_oak_log", "oak_log", false},
		{"oak_log", "stripped_oak_log", false},
	}
	for _, tc := range tests {
		got := itemNameMatches(tc.itemName, tc.ingredientName)
		if got != tc.want {
			t.Errorf("itemNameMatches(%q, %q) = %v, want %v", tc.itemName, tc.ingredientName, got, tc.want)
		}
	}
}

func TestPlanIngredientConsumptionNameMatching(t *testing.T) {
	t.Parallel()
	// Simulate the Bedrock situation: recipe ingredient is "oak_wood" but the
	// inventory contains "oak_log". The runtime IDs differ.
	itemNames := map[int32]string{
		-212: "oak_wood", // recipe ingredient runtime ID
		17:   "oak_log",  // inventory runtime ID
	}
	inv := map[uint32]protocol.ItemStack{
		0: {ItemType: protocol.ItemType{NetworkID: 17}, Count: 4},
	}
	ingredients := []protocol.ItemDescriptorCount{
		{
			Descriptor: &protocol.DefaultItemDescriptor{NetworkID: -212, MetadataValue: 0},
			Count:      1,
		},
	}
	picks, err := planIngredientConsumption(inv, itemNames, ingredients, 1)
	if err != nil {
		t.Fatalf("planIngredientConsumption failed: %v", err)
	}
	if len(picks) != 1 || picks[0].slot != 0 || picks[0].count != 1 {
		t.Errorf("unexpected picks: %+v", picks)
	}
}
