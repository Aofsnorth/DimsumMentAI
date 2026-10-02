package recipe

import "fmt"

// PlanCartography resolves a cartography table craft. Copying a map is
// acceptance 4.8.
//
// Every cartography operation ends in a map and several of them take map plus
// paper, so the action is what distinguishes them and the plan carries it as the
// caller chose it. The map's own data — the scale and origin coordinates that
// separate a copy from an extend — rides in the map item's NBT, which the
// vendored protocol cannot build, so this package does not claim to encode it.
func PlanCartography(inv Inventory, recipes []StationRecipe, action CartographyAction) (Plan, error) {
	op, ok := FindCartographyOp(action)
	if !ok {
		return Plan{}, fmt.Errorf("cartography: unknown operation %q", action)
	}
	if len(op.Inputs) != len(cartographyTable.Inputs) {
		return Plan{}, fmt.Errorf("cartography: operation %q has %d inputs but the station has %d slots",
			action, len(op.Inputs), len(cartographyTable.Inputs))
	}

	inputs := make([]StagedInput, 0, len(op.Inputs))
	for i, name := range op.Inputs {
		item, ok := inv.Find(name)
		if !ok {
			return Plan{}, fmt.Errorf("cartography %s: no %s in the bag", action, name)
		}
		// One of each. Paper stacks to 64 and redstone to 64; staging the whole
		// stack would leave the remainder stranded in the station window.
		inputs = append(inputs, StagedInput{
			Slot:       cartographyTable.Inputs[i],
			SourceSlot: item.Slot,
			ItemName:   name,
			Count:      1,
		})
	}

	serverRecipe, ok := FindStationRecipe(recipes, cartographyTable, op.Result)
	if !ok {
		return Plan{}, fmt.Errorf("cartography %s: the server advertised no cartography_table recipe for %s",
			action, op.Result)
	}
	return Plan{
		Station:         cartographyTable,
		RecipeNetworkID: serverRecipe.NetworkID,
		ResultName:      op.Result,
		ResultCount:     serverRecipe.ResultCount,
		Inputs:          inputs,
	}, nil
}
