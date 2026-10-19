package world_test

import (
	"testing"

	"bedrock-ai/internal/bot/world"
)

// TestNetherStemsAndHyphaeAreSolid is the regression guard for the stem rule in
// the passable-block table.
//
// crimson_stem and warped_stem are full logs in the Nether, not the plant
// "stem" the generic rule was written for. Excluding them keeps A* from treating
// a nether-forest trunk as walk-through air, which is what happened before: the
// bot pathed into the tree and then failed to move.
func TestNetherStemsAndHyphaeAreSolid(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"minecraft:crimson_stem",
		"minecraft:warped_stem",
		"minecraft:stripped_crimson_stem",
		"minecraft:crimson_hyphae",
		"minecraft:warped_hyphae",
		"minecraft:crimson_stem[axis=x]",
	} {
		if world.IsBlockNamePassable(name) {
			t.Errorf("%q was classified as passable; it is a solid Nether log", name)
		}
	}

	// The rule the exclusion list was written for must keep working.
	for _, name := range []string{
		"minecraft:short_grass",
		"minecraft:tall_grass",
		"minecraft:air",
		"minecraft:oak_sapling",
	} {
		if !world.IsBlockNamePassable(name) {
			t.Errorf("%q was classified as solid; it is a plant or air", name)
		}
	}
}
