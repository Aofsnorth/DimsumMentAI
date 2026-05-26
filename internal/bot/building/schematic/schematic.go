package schematic

import (
	"strings"
)

// NonPlaceable holds names of items that cannot be placed as blocks.
var NonPlaceable = map[string]bool{
	"air":             true,
	"unknown":         true,
	"wooden_sword":    true,
	"stone_sword":     true,
	"iron_sword":      true,
	"diamond_sword":   true,
	"netherite_sword": true,
	"wooden_pickaxe":  true,
	"stone_pickaxe":   true,
	"iron_pickaxe":    true,
	"diamond_pickaxe": true,
	"netherite_pickaxe": true,
	"wooden_axe":      true,
	"stone_axe":       true,
	"iron_axe":        true,
	"diamond_axe":     true,
	"netherite_axe":   true,
	"wooden_shovel":   true,
	"stone_shovel":    true,
	"iron_shovel":     true,
	"diamond_shovel":  true,
	"netherite_shovel": true,
	"wooden_hoe":      true,
	"stone_hoe":       true,
	"iron_hoe":        true,
	"diamond_hoe":     true,
	"netherite_hoe":   true,
	"bow":             true,
	"crossbow":        true,
	"fishing_rod":     true,
	"shield":          true,
	"trident":         true,
	"mace":            true,
	"flint_and_steel": true,
	"shears":          true,
	"spyglass":        true,
	"brush":           true,
	"leather_helmet":  true,
	"leather_chestplate": true,
	"leather_leggings": true,
	"leather_boots":   true,
	"iron_helmet":     true,
	"iron_chestplate": true,
	"iron_leggings":   true,
	"iron_boots":      true,
	"diamond_helmet":  true,
	"diamond_chestplate": true,
	"diamond_leggings": true,
	"diamond_boots":   true,
	"netherite_helmet": true,
	"netherite_chestplate": true,
	"netherite_leggings": true,
	"netherite_boots": true,
	"chainmail_helmet": true,
	"chainmail_chestplate": true,
	"chainmail_leggings": true,
	"chainmail_boots": true,
	"golden_helmet":   true,
	"golden_chestplate": true,
	"golden_leggings": true,
	"golden_boots":    true,
	"elytra":          true,
	"turtle_helmet":   true,
	"bucket":          true,
	"water_bucket":    true,
	"lava_bucket":     true,
	"milk_bucket":     true,
	"powder_snow_bucket": true,
	"stick":           true,
	"string":          true,
	"feather":         true,
	"flint":           true,
	"bone":            true,
	"leather":         true,
	"rabbit_hide":     true,
	"arrow":           true,
	"spectral_arrow":  true,
	"tipped_arrow":    true,
	"firework_rocket": true,
	"ender_pearl":     true,
	"ender_eye":       true,
	"blaze_rod":       true,
	"blaze_powder":    true,
	"ghast_tear":      true,
	"compass":         true,
	"clock":           true,
	"map":             true,
	"filled_map":      true,
	"name_tag":        true,
	"lead":            true,
	"saddle":          true,
	"potion":          true,
	"splash_potion":   true,
	"lingering_potion": true,
	"experience_bottle": true,
	"book":            true,
	"writable_book":   true,
	"written_book":    true,
	"enchanted_book":  true,
	"knowledge_book":  true,
	"paper":           true,
	"gunpowder":       true,
	"sugar":           true,
	"egg":             true,
	"snowball":        true,
	"minecart":        true,
	"chest_minecart":  true,
	"hopper_minecart": true,
	"tnt_minecart":    true,
	"furnace_minecart": true,
	"boat":            true,
	"oak_boat":        true,
	"spruce_boat":     true,
	"birch_boat":      true,
	"jungle_boat":     true,
	"acacia_boat":     true,
	"dark_oak_boat":   true,
}

// IsBuildable returns true if the block/item name represents a placeable block.
func IsBuildable(name string) bool {
	if name == "" || name == "air" || name == "unknown" {
		return false
	}
	name = strings.ReplaceAll(name, "minecraft:", "")
	if NonPlaceable[name] {
		return false
	}

	if strings.Contains(name, "cooked_") || strings.Contains(name, "raw_") ||
		name == "apple" || name == "bread" || name == "cookie" ||
		name == "cake" || name == "beef" || name == "porkchop" ||
		name == "chicken" || name == "mutton" || name == "rabbit" ||
		name == "cod" || name == "salmon" || name == "rotten_flesh" ||
		name == "spider_eye" || name == "carrot" || name == "potato" ||
		name == "beetroot" || name == "sweet_berries" || name == "glow_berries" ||
		name == "melon_slice" || name == "dried_kelp" {
		return false
	}

	if strings.Contains(name, "_ingot") || name == "diamond" || name == "emerald" ||
		name == "coal" || name == "charcoal" || name == "lapis_lazuli" ||
		name == "quartz" || name == "amethyst_shard" || strings.Contains(name, "_shard") ||
		strings.Contains(name, "_nugget") || name == "redstone" || name == "glowstone_dust" ||
		strings.Contains(name, "_dye") || name == "ink_sac" || name == "bone_meal" ||
		name == "wheat" || strings.Contains(name, "seeds") {
		return false
	}

	return true
}
