package perception

import "testing"

// These cover the classification that decides whether an activity is possible
// at all — the part that was missing, and the part a player notices immediately
// when it is wrong. The scan geometry around them (the vision cone and the
// line-of-sight walk) is covered in the fov and storage packages.

// TestWaterDetectionCoversTheVariantsThatExist pins the practical reality that
// there is no finite list of water blocks: Bedrock has water, flowing water,
// bubble columns, and a long tail of modded variants. A fixed enumeration would
// eventually miss one and the bot would stop fishing on that server.
func TestWaterDetectionCoversTheVariantsThatExist(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"minecraft:water",
		"minecraft:flowing_water",
		"minecraft:bubble_column",
		"some_mod:swimming_water",
		"some_mod:holy_water",
	} {
		if !matchesAny(cleanName(name), waterBlocks) {
			t.Errorf("%q was not detected as water", name)
		}
	}
	for _, name := range []string{"minecraft:stone", "minecraft:oak_log", "minecraft:sand"} {
		if matchesAny(cleanName(name), waterBlocks) {
			t.Errorf("%q was wrongly detected as water", name)
		}
	}
}

// TestRipeCropsAreDistinguishedFromSeedlings is the important one. A bot that
// harvests seedlings has destroyed the farm and gained nothing, which is
// strictly worse than not farming: it converts a future harvest into an empty
// field.
func TestRipeCropsAreDistinguishedFromSeedlings(t *testing.T) {
	t.Parallel()

	ripe := []string{
		"minecraft:wheat",
		"minecraft:carrots",
		"minecraft:potatoes",
		"minecraft:beetroot",
		"minecraft:melon",
	}
	for _, name := range ripe {
		if !isRipeCrop(cleanName(name)) {
			t.Errorf("%q is a grown crop but was not treated as ripe", name)
		}
	}

	seedlings := []string{
		"minecraft:wheat_stage0",
		"minecraft:wheat_stage1",
		"minecraft:carrots_stage0",
		"minecraft:beetroot_stage0",
		"minecraft:torchflower_crop_stage0",
	}
	for _, name := range seedlings {
		if isRipeCrop(cleanName(name)) {
			t.Errorf("%q is still growing but was treated as ripe to harvest", name)
		}
	}
}

// TestUnrecognisedCropIsNotHarvested encodes the safe default. When the bot
// cannot tell whether a plant is grown, refusing to harvest costs it a crop;
// harvesting a seedling costs it the whole field.
func TestUnrecognisedCropIsNotHarvested(t *testing.T) {
	t.Parallel()

	if isRipeCrop(cleanName("minecraft:something_new_crop_stage0")) {
		t.Error("an unrecognised growing crop was treated as ripe")
	}
	if isRipeCrop(cleanName("minecraft:stone")) {
		t.Error("stone was treated as a ripe crop")
	}
}

// TestLogDetectionCoversWoodVariants makes sure chopping is offered for every
// wood a player might meet, not just oak.
func TestLogDetectionCoversWoodVariants(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"minecraft:oak_log", "minecraft:birch_log", "minecraft:spruce_log",
		"minecraft:jungle_log", "minecraft:acacia_log", "minecraft:dark_oak_log",
		"minecraft:cherry_log", "minecraft:crimson_stem", "minecraft:warped_stem",
	} {
		if !matchesAny(cleanName(name), logBlocks) {
			t.Errorf("%q was not detected as a log", name)
		}
	}
	// Leaves are a tree, but you cannot chop them for wood the way a player
	// chops a trunk, so they must not be counted as logs.
	if matchesAny(cleanName("minecraft:oak_leaves"), logBlocks) {
		t.Error("oak leaves were counted as a choppable log")
	}
}

// TestFarmableAnimalsExcludeHostileMobs is the safety rule behind "tend the
// animals". Offering animal-tending next to a creeper is a task the bot should
// never be choosing, and the whole point of the activity is calm husbandry.
func TestFarmableAnimalsExcludeHostileMobs(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"minecraft:cow", "minecraft:pig", "minecraft:sheep", "minecraft:chicken",
		"minecraft:horse", "minecraft:goat", "minecraft:bee",
	} {
		if !matchesAny(name, farmableAnimals) {
			t.Errorf("%q should be farmable", name)
		}
	}
	for _, name := range []string{
		"minecraft:creeper", "minecraft:zombie", "minecraft:skeleton",
		"minecraft:spider", "minecraft:enderman", "minecraft:witch",
	} {
		if matchesAny(name, farmableAnimals) {
			t.Errorf("%q is hostile but was treated as farmable", name)
		}
	}
}
