package building

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// BlockEntry represents a single block placement instruction from a blueprint or template.
type BlockEntry struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Z        int    `json:"z"`
	Block    string `json:"block"`
	Facing   string `json:"facing,omitempty"`
	Metadata *int   `json:"metadata,omitempty"`
}

// BuildItem represents an item available in the inventory that can be placed as a block.
type BuildItem struct {
	Slot  uint32
	Name  string
	Count int
}

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

	// Food items heuristic
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

	// Materials & seeds heuristic
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

// ParsePlan extracts and repairs a JSON block array from raw AI output.
func ParsePlan(raw string) []BlockEntry {
	cleaned := strings.ReplaceAll(raw, "```json", "")
	cleaned = strings.ReplaceAll(cleaned, "```", "")
	cleaned = strings.TrimSpace(cleaned)

	firstBracket := strings.Index(cleaned, "[")
	if firstBracket == -1 {
		return nil
	}

	lastBracket := strings.LastIndex(cleaned, "]")
	if lastBracket == -1 || lastBracket <= firstBracket {
		// Attempt repair of truncated array
		lastBrace := strings.LastIndex(cleaned, "}")
		if lastBrace > firstBracket {
			cleaned = cleaned[firstBracket:lastBrace+1] + "]"
		} else {
			return nil
		}
	} else {
		cleaned = cleaned[firstBracket : lastBracket+1]
	}

	// Remove trailing commas that break JSON decoding
	cleaned = regexp.MustCompile(`,\s*\]`).ReplaceAllString(cleaned, "]")
	cleaned = regexp.MustCompile(`}\s*,\s*$`).ReplaceAllString(cleaned, "}]")

	var plan []BlockEntry
	if err := json.Unmarshal([]byte(cleaned), &plan); err != nil {
		return nil
	}

	var valid []BlockEntry
	for _, entry := range plan {
		if entry.Block != "" {
			entry.Block = strings.ReplaceAll(entry.Block, "minecraft:", "")
			valid = append(valid, entry)
		}
	}
	return valid
}

// GetBuildItems returns a list of buildable items currently in the bot's inventory.
func GetBuildItems(inv map[uint32]protocol.ItemStack, names map[int32]string) []BuildItem {
	var items []BuildItem
	for slot, stack := range inv {
		if stack.Count <= 0 || stack.NetworkID == 0 {
			continue
		}
		name := names[stack.NetworkID]
		if name == "" {
			continue
		}
		name = strings.ReplaceAll(name, "minecraft:", "")
		if IsBuildable(name) {
			items = append(items, BuildItem{
				Slot:  slot,
				Name:  name,
				Count: int(stack.Count),
			})
		}
	}
	return items
}

// FindItemInSlots searches inventory for a specific item by name.
func FindItemInSlots(inv map[uint32]protocol.ItemStack, names map[int32]string, name string) (uint32, bool) {
	name = strings.ReplaceAll(name, "minecraft:", "")
	for slot, stack := range inv {
		if stack.Count <= 0 || stack.NetworkID == 0 {
			continue
		}
		iName := names[stack.NetworkID]
		iName = strings.ReplaceAll(iName, "minecraft:", "")
		if iName == name {
			return slot, true
		}
	}
	return 0, false
}

// FindScaffoldForTower looks for a suitable scaffold block in the inventory.
func FindScaffoldForTower(inv map[uint32]protocol.ItemStack, names map[int32]string) (uint32, bool) {
	scaffoldPriority := []string{"dirt", "cobblestone", "netherrack", "stone", "sand", "gravel", "clay", "mud"}
	for _, pattern := range scaffoldPriority {
		for slot, stack := range inv {
			if stack.Count <= 0 || stack.NetworkID == 0 {
				continue
			}
			name := names[stack.NetworkID]
			name = strings.ReplaceAll(name, "minecraft:", "")
			if IsScaffoldSafe(name) && strings.Contains(name, pattern) {
				return slot, true
			}
		}
	}
	return 0, false
}

// IsScaffoldSafe checks if a block name is safe to use as scaffolding.
func IsScaffoldSafe(name string) bool {
	if name == "" {
		return false
	}
	name = strings.ReplaceAll(name, "minecraft:", "")

	// Explicit blocklist: NEVER scaffold with decorations, tools, interactables, structural styles
	neverScaffold := []string{
		"flower", "rose", "tulip", "orchid", "daisy", "dandelion", "poppy", "lily", "azalea", "allium", "cornflower", "bluet",
		"banner", "sign", "sapling", "torch", "lantern", "candle", "campfire",
		"fence", "wall", "gate",
		"bed", "carpet", "pot", "head", "skull",
		"chest", "barrel", "furnace", "smoker", "blast", "anvil", "enchant", "brewing", "cauldron",
		"rail", "redstone", "piston", "hopper", "dropper", "dispenser", "observer", "comparator", "repeater", "lever", "button", "pressure",
		"planks", "log", "wood", "stripped",
		"glass", "slab", "stairs", "door", "trapdoor",
		"wool", "concrete", "terracotta", "brick",
		"iron_block", "gold_block", "diamond_block", "emerald_block",
		"snow", "vine", "fern", "bush", "bamboo", "cactus", "sweet_berry",
		"coral", "sponge", "prismarine", "sea",
		"item_frame", "painting", "armor_stand",
		"shulker", "ender",
	}

	for _, bad := range neverScaffold {
		if strings.Contains(name, bad) {
			return false
		}
	}

	// Explicit allowlist: cheap, raw materials
	safe := []string{
		"dirt", "cobblestone", "netherrack", "stone", "sand", "gravel", "clay", "mud",
		"sandstone", "deepslate", "tuff", "dripstone", "basalt", "andesite", "diorite", "granite", "cobbled",
	}
	for _, s := range safe {
		if strings.Contains(name, s) {
			return true
		}
	}

	return false
}

// FindSubstitute checks available inventory blocks to find a suitable substitute for a target block type.
func FindSubstitute(target string, available []BuildItem) string {
	target = strings.ReplaceAll(target, "minecraft:", "")
	
	// If exact match is available, keep it
	for _, item := range available {
		if item.Name == target {
			return target
		}
	}

	// Planks category substitution
	if strings.Contains(target, "planks") {
		for _, item := range available {
			if strings.Contains(item.Name, "planks") {
				return item.Name
			}
		}
		// Fallback to logs
		for _, item := range available {
			if strings.Contains(item.Name, "log") || strings.Contains(item.Name, "wood") {
				return item.Name
			}
		}
	}

	// Log category substitution
	if strings.Contains(target, "log") || strings.Contains(target, "wood") {
		for _, item := range available {
			if strings.Contains(item.Name, "log") || strings.Contains(item.Name, "wood") {
				return item.Name
			}
		}
	}

	// Stone/Cobblestone category substitution
	if strings.Contains(target, "stone") || strings.Contains(target, "cobblestone") || strings.Contains(target, "brick") || strings.Contains(target, "deepslate") {
		for _, item := range available {
			if strings.Contains(item.Name, "stone") || strings.Contains(item.Name, "cobblestone") || strings.Contains(item.Name, "brick") || strings.Contains(item.Name, "deepslate") {
				return item.Name
			}
		}
	}

	// Glass category substitution
	if strings.Contains(target, "glass") {
		for _, item := range available {
			if strings.Contains(item.Name, "glass") {
				return item.Name
			}
		}
	}

	// Wool category substitution
	if strings.Contains(target, "wool") {
		for _, item := range available {
			if strings.Contains(item.Name, "wool") {
				return item.Name
			}
		}
	}

	// Concrete category substitution
	if strings.Contains(target, "concrete") {
		for _, item := range available {
			if strings.Contains(item.Name, "concrete") {
				return item.Name
			}
		}
	}

	return target
}
