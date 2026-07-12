// Package schematic provides utilities for working with building schematics.
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

// containsAny reports whether s contains any of the given substrings.
func containsAny(s string, substrings []string) bool {
	for _, sub := range substrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// substituteGroup maps a target block keyword to candidate item keywords used
// when looking for an inventory substitute.
type substituteGroup struct {
	target []string
	items  []string
}

var substituteGroups = []substituteGroup{
	{target: []string{"planks"}, items: []string{"planks", "log", "wood"}},
	{target: []string{"log", "wood"}, items: []string{"log", "wood"}},
	{target: []string{"stone", "cobblestone", "brick", "deepslate"}, items: []string{"stone", "cobblestone", "brick", "deepslate"}},
	{target: []string{"glass"}, items: []string{"glass"}},
	{target: []string{"wool"}, items: []string{"wool"}},
	{target: []string{"concrete"}, items: []string{"concrete"}},
}

// FindSubstitute checks available inventory blocks to find a suitable substitute for a target block type.
func FindSubstitute(target string, available []common.BuildItem) string {
	target = strings.ReplaceAll(target, "minecraft:", "")

	for _, item := range available {
		if item.Name == target {
			return target
		}
	}

	for _, group := range substituteGroups {
		if containsAny(target, group.target) {
			for _, item := range available {
				if containsAny(item.Name, group.items) {
					return item.Name
				}
			}
		}
	}
	return target
}
