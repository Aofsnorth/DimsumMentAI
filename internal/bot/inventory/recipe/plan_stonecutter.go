package recipe

import "fmt"

// PlanStonecut resolves a stonecutter craft of result. It is acceptance 4.6.
//
// The stonecutter is a one-input station, and a stone brick can also be made
// with four bricks in a crafting table. Both facts matter: only one block is
// staged, and the recipe is looked up by station block as well as by result, so
// "stone bricks" can never be satisfied by a crafting-table recipe the bot
// would have produced without ever touching the stonecutter.
func PlanStonecut(inv Inventory, recipes []StationRecipe, result string) (Plan, error) {
	want := NormalizeName(result)
	if want == "" {
		return Plan{}, fmt.Errorf("stonecutter: no result requested")
	}

	// The catalogue is walked in order, so a result reachable from more than
	// one input resolves the same way on every call.
	var inCatalogue, inputPresent bool
	for _, sc := range Stonecuts() {
		if sc.Result != want {
			continue
		}
		inCatalogue = true
		source, hasInput := inv.Find(sc.Input)
		if !hasInput {
			continue
		}
		inputPresent = true
		serverRecipe, hasRecipe := FindStationRecipe(recipes, stonecutter, sc.Result)
		if !hasRecipe {
			continue
		}
		return Plan{
			Station:         stonecutter,
			RecipeNetworkID: serverRecipe.NetworkID,
			ResultName:      sc.Result,
			ResultCount:     serverRecipe.ResultCount,
			Inputs: []StagedInput{{
				Slot:       stonecutter.Inputs[0],
				SourceSlot: source.Slot,
				ItemName:   sc.Input,
				Count:      1,
			}},
		}, nil
	}

	switch {
	case !inCatalogue:
		return Plan{}, fmt.Errorf("stonecutter: no stonecutter transform makes %q", want)
	case !inputPresent:
		return Plan{}, fmt.Errorf("stonecutter: cutting %q needs a %s", want, stonecutInputFor(want))
	default:
		return Plan{}, fmt.Errorf("stonecutter: the server advertised no stonecutter recipe for %q", want)
	}
}

// stonecutInputFor names the first catalogue input for a result, so the error
// for a missing input is specific instead of "nothing to cut".
func stonecutInputFor(result string) string {
	for _, sc := range Stonecuts() {
		if sc.Result == result {
			return sc.Input
		}
	}
	return result
}
