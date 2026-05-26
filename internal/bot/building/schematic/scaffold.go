package schematic

import (
	"strings"

	"bedrock-ai/internal/bot/building/common"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

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
func FindSubstitute(target string, available []common.BuildItem) string {
	target = strings.ReplaceAll(target, "minecraft:", "")
	
	for _, item := range available {
		if item.Name == target {
			return target
		}
	}

	if strings.Contains(target, "planks") {
		for _, item := range available {
			if strings.Contains(item.Name, "planks") {
				return item.Name
			}
		}
		for _, item := range available {
			if strings.Contains(item.Name, "log") || strings.Contains(item.Name, "wood") {
				return item.Name
			}
		}
	}

	if strings.Contains(target, "log") || strings.Contains(target, "wood") {
		for _, item := range available {
			if strings.Contains(item.Name, "log") || strings.Contains(item.Name, "wood") {
				return item.Name
			}
		}
	}

	if strings.Contains(target, "stone") || strings.Contains(target, "cobblestone") || strings.Contains(target, "brick") || strings.Contains(target, "deepslate") {
		for _, item := range available {
			if strings.Contains(item.Name, "stone") || strings.Contains(item.Name, "cobblestone") || strings.Contains(item.Name, "brick") || strings.Contains(item.Name, "deepslate") {
				return item.Name
			}
		}
	}

	if strings.Contains(target, "glass") {
		for _, item := range available {
			if strings.Contains(item.Name, "glass") {
				return item.Name
			}
		}
	}

	if strings.Contains(target, "wool") {
		for _, item := range available {
			if strings.Contains(item.Name, "wool") {
				return item.Name
			}
		}
	}

	if strings.Contains(target, "concrete") {
		for _, item := range available {
			if strings.Contains(item.Name, "concrete") {
				return item.Name
			}
		}
	}
	return target
}
