package bot

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func TestCraftingTableGridSlotKeepsShapeAndFullNineSlots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		recipe  RecipeInfo
		want    []byte
		invalid int
	}{
		{name: "wooden axe 2x3", recipe: RecipeInfo{Width: 2, Height: 3}, want: []byte{32, 33, 35, 36, 38, 39}, invalid: 6},
		{name: "three tall single column", recipe: RecipeInfo{Width: 1, Height: 3}, want: []byte{32, 35, 38}, invalid: 3},
		{name: "full bed row", recipe: RecipeInfo{Width: 3, Height: 1}, want: []byte{32, 33, 34}, invalid: 3},
		{name: "shapeless nine", recipe: RecipeInfo{Shapeless: true}, want: []byte{32, 33, 34, 35, 36, 37, 38, 39, 40}, invalid: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for index, want := range tt.want {
				slot, err := craftingTableGridSlot(tt.recipe, index)
				if err != nil || slot != want {
					t.Fatalf("grid ingredient %d = %d, %v, want %d", index, slot, err, want)
				}
			}
			if _, err := craftingTableGridSlot(tt.recipe, tt.invalid); err == nil {
				t.Fatalf("ingredient %d past grid end was accepted", tt.invalid)
			}
		})
	}
}

func TestTableCraftWoodenAxeConsumesEveryStagedGridSlot(t *testing.T) {
	t.Parallel()

	recipe := RecipeInfo{Output: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 42}, Count: 1}, Width: 2, Height: 3, Block: "crafting_table"}
	// The recipe's fifth position is empty, but all five occupied positions
	// must be staged before the server accepts CraftRecipe.
	indices := []int{0, 1, 2, 3, 5}
	inputs := make([]craftingGridInput, 0, len(indices))
	for i, ingredientIndex := range indices {
		slot, err := craftingTableGridSlot(recipe, ingredientIndex)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, craftingGridInput{slot: slot, count: 1, stackNetworkID: int32(101 + i)})
	}

	actions, err := buildCraftActions(-53, 1727, recipe, 1, inputs, 5, "wooden_axe")
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2+len(inputs)+1 {
		t.Fatalf("table craft actions = %d, want recipe, result, five consumes and place", len(actions))
	}
	if _, ok := actions[0].(*protocol.CraftRecipeStackRequestAction); !ok {
		t.Fatalf("first action = %T, want manual craft (not AutoCraft)", actions[0])
	}
	if _, ok := actions[1].(*protocol.CraftResultsDeprecatedStackRequestAction); !ok {
		t.Fatalf("second action = %T, want craft results", actions[1])
	}
	for i, input := range inputs {
		consume, ok := actions[i+2].(*protocol.ConsumeStackRequestAction)
		if !ok {
			t.Fatalf("action %d = %T, want grid consume", i+2, actions[i+2])
		}
		if consume.Source.Container.ContainerID != protocol.ContainerCraftingInput || consume.Source.Slot != input.slot || consume.Source.StackNetworkID != input.stackNetworkID || consume.Count != 1 {
			t.Fatalf("consume %d = %+v, want staged grid input %+v", i, consume, input)
		}
	}
	place, ok := actions[len(actions)-1].(*protocol.PlaceStackRequestAction)
	if !ok || place.Source.Container.ContainerID != protocol.ContainerCreatedOutput || place.Source.Slot != CreatedOutputSlot || place.Source.StackNetworkID != -53 {
		t.Fatalf("last action = %+v, want created output tied to request -53", actions[len(actions)-1])
	}
}

func TestTableCraftRejectsUnsupportedGridBeforeStaging(t *testing.T) {
	t.Parallel()

	tests := []RecipeInfo{
		{Block: "furnace", Width: 3, Height: 3},
		{Block: "crafting_table", Width: 4, Height: 1},
		{Block: "crafting_table", Shapeless: true, Ingredients: make([]protocol.ItemDescriptorCount, 10)},
		{Block: "crafting_table", Width: 3, Height: 4},
		{Block: "crafting_table", Width: 0, Height: 0},
	}
	for _, recipe := range tests {
		if err := validateTableCraftRecipe(recipe); err == nil {
			t.Fatalf("accepted unsupported recipe %+v", recipe)
		}
	}
}
