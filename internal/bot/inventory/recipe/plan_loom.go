package recipe

import (
	"fmt"
	"strings"
)

// bannerSuffix is what every colourable banner item name ends in.
const bannerSuffix = "_banner"

// wallBannerInfix marks the wall variant, which the loom cannot decorate. A
// "white_wall_banner" ends in "_banner" like a regular one does, so matching on
// the suffix alone would stage an item the loom has no slot for.
const wallBannerInfix = "wall_banner"

// dyeSuffix marks a colour dye, the only thing that gives a banner its colour.
const dyeSuffix = "_dye"

// PlanBannerPattern resolves a loom craft that applies a pattern to a banner. It
// is acceptance 4.7.
//
// The loom is the one station here with no CraftingData recipe: the client sends
// CraftLoomRecipeStackRequestAction, which names a pattern string rather than a
// recipe network ID. So this plan carries a Pattern and leaves RecipeNetworkID
// at zero, and a caller that sent a recipe ID to the loom would have its request
// rejected.
//
// The result is named after the dye. Applying a pattern replaces the banner's
// colour with the dye's, so a red dye on a white banner yields a red patterned
// banner. The banner's stored colour lives in item NBT, which the vendored
// protocol cannot build or read, so the dye is the only honest source for the
// result name.
func PlanBannerPattern(inv Inventory, pattern string) (Plan, error) {
	resolved, ok := FindBannerPattern(pattern)
	if !ok {
		return Plan{}, fmt.Errorf("loom: unknown banner pattern %q", pattern)
	}

	banner, ok := findBanner(inv)
	if !ok {
		return Plan{}, fmt.Errorf("loom: no banner in the bag to pattern")
	}

	inputs := []StagedInput{{
		Slot:       loom.Inputs[0],
		SourceSlot: banner.Slot,
		ItemName:   banner.Name,
		Count:      1,
	}}

	if !resolved.ConsumesDye {
		// The base border pattern is what a plain dye produces, so the banner
		// keeps its own colour and a second dye would be thrown away.
		return Plan{
			Station:     loom,
			Pattern:     resolved.ID,
			ResultName:  banner.Name,
			ResultCount: 1,
			Inputs:      inputs,
		}, nil
	}

	dye, ok := findDye(inv)
	if !ok {
		return Plan{}, fmt.Errorf("loom: pattern %q needs a dye", resolved.ID)
	}
	result, ok := bannerColourFromName(dye.Name)
	if !ok {
		return Plan{}, fmt.Errorf("loom: %q is not a dye a banner can take", dye.Name)
	}

	inputs = append(inputs, StagedInput{
		Slot:       loom.Inputs[1],
		SourceSlot: dye.Slot,
		ItemName:   dye.Name,
		Count:      1,
	})
	return Plan{
		Station:     loom,
		Pattern:     resolved.ID,
		ResultName:  result,
		ResultCount: 1,
		Inputs:      inputs,
	}, nil
}

// findBanner returns the lowest-numbered loomable banner in the bag.
func findBanner(inv Inventory) (Item, bool) {
	return inv.Lowest(func(item Item) bool { return isLoomableBanner(item.Name) })
}

// isLoomableBanner reports whether a name is a standing banner. Wall banners
// share the suffix and are excluded.
func isLoomableBanner(name string) bool {
	name = NormalizeName(name)
	if name == "" || !strings.HasSuffix(name, bannerSuffix) {
		return false
	}
	return !strings.HasSuffix(name, wallBannerInfix)
}

// findDye returns the lowest-numbered dye in the bag. The result colour follows
// it, so a bag with two dyes must always pick the same one.
func findDye(inv Inventory) (Item, bool) {
	return inv.Lowest(func(item Item) bool { return strings.HasSuffix(item.Name, dyeSuffix) })
}

// bannerColourFromName turns a dye name into the banner the loom returns, and
// reports false for anything that is not a colour dye.
func bannerColourFromName(name string) (string, bool) {
	banner := bannerColourFromDye(NormalizeName(name))
	if banner == "" {
		return "", false
	}
	return banner, true
}
