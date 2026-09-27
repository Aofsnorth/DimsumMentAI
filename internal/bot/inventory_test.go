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

func TestReconcileCraftIngredientCounts(t *testing.T) {
	t.Parallel()

	planks := protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 5}, Count: 6}
	picks := []ingredientPick{
		{slot: 2, count: 1, ingredientIndex: 0},
		{slot: 2, count: 1, ingredientIndex: 1},
	}
	sources := snapshotIngredientSources(map[uint32]protocol.ItemStack{2: planks}, picks)

	t.Run("repairs stale source count", func(t *testing.T) {
		inventory := map[uint32]protocol.ItemStack{2: planks}
		reconcileCraftIngredientCounts(inventory, map[uint32]int32{2: 42}, sources)
		if got := inventory[2].Count; got != 4 {
			t.Fatalf("plank count = %d, want 4", got)
		}
	})

	t.Run("preserves authoritative lower count", func(t *testing.T) {
		inventory := map[uint32]protocol.ItemStack{2: {ItemType: planks.ItemType, Count: 3}}
		reconcileCraftIngredientCounts(inventory, map[uint32]int32{2: 42}, sources)
		if got := inventory[2].Count; got != 3 {
			t.Fatalf("plank count = %d, want authoritative 3", got)
		}
	})

	t.Run("does not alter replaced slot", func(t *testing.T) {
		inventory := map[uint32]protocol.ItemStack{2: {ItemType: protocol.ItemType{NetworkID: 17}, Count: 6}}
		reconcileCraftIngredientCounts(inventory, map[uint32]int32{2: 42}, sources)
		if got := inventory[2].Count; got != 6 {
			t.Fatalf("replacement count = %d, want 6", got)
		}
	})

	t.Run("removes exhausted source and stack ID", func(t *testing.T) {
		inventory := map[uint32]protocol.ItemStack{2: {ItemType: planks.ItemType, Count: 2}}
		stackNetworkIDs := map[uint32]int32{2: 42}
		exhaustedSources := snapshotIngredientSources(inventory, picks)
		reconcileCraftIngredientCounts(inventory, stackNetworkIDs, exhaustedSources)
		if _, exists := inventory[2]; exists {
			t.Fatal("expected exhausted ingredient slot to be removed")
		}
		if _, exists := stackNetworkIDs[2]; exists {
			t.Fatal("expected exhausted ingredient stack ID to be removed")
		}
	})
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
			Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_wood"},
			Count:      1,
		},
	}
	picks, err := planIngredientConsumption(inv, itemNames, ingredients, 1)
	if err != nil {
		t.Fatalf("planIngredientConsumption failed: %v", err)
	}
	if len(picks) != 1 || picks[0].slot != 0 || picks[0].count != 1 || picks[0].ingredientIndex != 0 {
		t.Errorf("unexpected picks: %+v", picks)
	}
}

func TestBuildManualCraftTransferActions(t *testing.T) {
	t.Parallel()
	source := playerStackRequestSlot(0, 10)
	take := buildTakeToCursorAction(source, 1)
	if take.Source.Container.ContainerID != protocol.ContainerHotBar || take.Source.Slot != 0 || take.Source.StackNetworkID != 10 {
		t.Errorf("unexpected take source: %+v", take.Source)
	}
	if take.Destination.Container.ContainerID != protocol.ContainerCursor || take.Destination.Slot != 0 || take.Destination.StackNetworkID != 0 {
		t.Errorf("unexpected take destination: %+v", take.Destination)
	}

	place := buildPlaceCursorToCraftingAction(11, 29, 0, 1)
	if place.Source.Container.ContainerID != protocol.ContainerCursor || place.Source.Slot != 0 || place.Source.StackNetworkID != 11 {
		t.Errorf("unexpected place source: %+v", place.Source)
	}
	if place.Destination.Container.ContainerID != protocol.ContainerCraftingInput || place.Destination.Slot != 29 || place.Destination.StackNetworkID != 0 {
		t.Errorf("unexpected place destination: %+v", place.Destination)
	}
}

func TestBuildCraftActionsVanillaSequence(t *testing.T) {
	t.Parallel()
	recipe := RecipeInfo{
		Ingredients: []protocol.ItemDescriptorCount{{
			Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_wood"},
			Count:      1,
		}},
		Output: protocol.ItemStack{
			ItemType: protocol.ItemType{NetworkID: 5},
			Count:    4,
		},
	}
	inputs := []craftingGridInput{{slot: 29, count: 1, stackNetworkID: 42}}
	actions, err := buildCraftActions(-3, 414, recipe, 1, inputs, 3, "stick")
	if err != nil {
		t.Fatalf("buildCraftActions() error = %v", err)
	}
	if len(actions) != 4 {
		t.Fatalf("len(actions) = %d, want 4", len(actions))
	}

	craft, ok := actions[0].(*protocol.CraftRecipeStackRequestAction)
	if !ok {
		t.Fatalf("actions[0] = %T, want CraftRecipe", actions[0])
	}
	if craft.RecipeNetworkID != 414 || craft.NumberOfCrafts != 1 {
		t.Errorf("unexpected craft fields: %+v", craft)
	}
	results, ok := actions[1].(*protocol.CraftResultsDeprecatedStackRequestAction)
	if !ok {
		t.Fatalf("actions[1] = %T, want CraftResultsDeprecated", actions[1])
	}
	if results.TimesCrafted != 1 || len(results.ResultItems) != 1 ||
		results.ResultItems[0].Identifier != "minecraft:stick" || results.ResultItems[0].Count != 4 {
		t.Errorf("unexpected craft results fields: %+v", results)
	}

	consume, ok := actions[2].(*protocol.ConsumeStackRequestAction)
	if !ok {
		t.Fatalf("actions[2] = %T, want Consume", actions[2])
	}
	if consume.Count != 1 || consume.Source.Container.ContainerID != protocol.ContainerCraftingInput || consume.Source.Slot != 29 || consume.Source.StackNetworkID != 42 {
		t.Errorf("unexpected consume fields: %+v", consume)
	}

	place, ok := actions[3].(*protocol.PlaceStackRequestAction)
	if !ok {
		t.Fatalf("actions[3] = %T, want Place", actions[3])
	}
	if place.Count != 4 || place.Source.Container.ContainerID != protocol.ContainerCreatedOutput || place.Source.Slot != 50 || place.Source.StackNetworkID != -3 {
		t.Errorf("unexpected place source fields: %+v", place)
	}
	if place.Destination.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory || place.Destination.Slot != 3 || place.Destination.StackNetworkID != 0 {
		t.Errorf("unexpected place destination fields: %+v", place)
	}
}

func TestCraftingGridSlot(t *testing.T) {
	t.Parallel()
	recipe := RecipeInfo{Width: 1, Height: 2}
	for index, want := range []byte{29, 31} {
		got, err := craftingGridSlot(recipe, index)
		if err != nil || got != want {
			t.Errorf("craftingGridSlot(%d) = %d, %v; want %d", index, got, err, want)
		}
	}
}
