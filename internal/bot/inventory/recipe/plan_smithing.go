package recipe

import (
	"fmt"
	"strings"
)

// netheriteIngot is the smithing addition every netherite upgrade consumes.
const netheriteIngot = "netherite_ingot"

// PlanNetheriteUpgrade resolves a diamond -> netherite smithing upgrade. It is
// acceptance 4.5.
//
// The SmithingTransformRecipe has three descriptors — Template, Base, Addition
// — and the plan fills all three or returns an error. A plan missing the
// template burns nothing and gets nothing, so "two out of three is close
// enough" is not an option here.
func PlanNetheriteUpgrade(inv Inventory, recipes []StationRecipe) (Plan, error) {
	template, ok := findUpgradeTemplate(inv)
	if !ok {
		return Plan{}, fmt.Errorf("smithing: no upgrade template in the bag (looked for %s)",
			strings.Join(NetheriteUpgradeTemplateNames, " or "))
	}

	// Prefer an upgrade the bot can complete end to end: a base in the bag, an
	// ingot to add, and a recipe the server advertised for the result. Only
	// when none of those line up is a specific reason reported, so the log
	// names the missing piece instead of just "no plan".
	var basePresent, ingotPresent bool
	for _, up := range NetheriteUpgrades() {
		base, hasBase := inv.Find(up.Base)
		if !hasBase {
			continue
		}
		basePresent = true
		ingot, hasIngot := inv.Find(netheriteIngot)
		if !hasIngot {
			continue
		}
		ingotPresent = true
		serverRecipe, hasRecipe := FindStationRecipe(recipes, smithingTable, up.Result)
		if !hasRecipe {
			continue
		}

		return Plan{
			Station:         smithingTable,
			RecipeNetworkID: serverRecipe.NetworkID,
			ResultName:      up.Result,
			ResultCount:     serverRecipe.ResultCount,
			Inputs: []StagedInput{
				{
					Slot:       smithingTable.Inputs[0],
					SourceSlot: template.Slot,
					ItemName:   template.Name,
					Count:      up.TemplateConsumed,
				},
				{
					Slot:       smithingTable.Inputs[1],
					SourceSlot: base.Slot,
					ItemName:   up.Base,
					Count:      1,
				},
				{
					Slot:       smithingTable.Inputs[2],
					SourceSlot: ingot.Slot,
					ItemName:   netheriteIngot,
					Count:      1,
				},
			},
		}, nil
	}

	switch {
	case !basePresent:
		return Plan{}, fmt.Errorf("smithing: no diamond item in the bag to upgrade")
	case !ingotPresent:
		return Plan{}, fmt.Errorf("smithing: upgrading a diamond item needs a %s", netheriteIngot)
	default:
		return Plan{}, fmt.Errorf("smithing: the server advertised no smithing_table recipe for a netherite upgrade")
	}
}

// findUpgradeTemplate returns the upgrade template in the bag, under either
// spelling. It matches on the item name rather than on the name list so a
// template in a different trim colour — there is a smithing template for every
// trim material — is still found.
func findUpgradeTemplate(inv Inventory) (Item, bool) {
	return inv.Lowest(func(item Item) bool { return TemplateMatches(item.Name) })
}
