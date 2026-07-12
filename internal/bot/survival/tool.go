// Package survival provides automation for managing the bot's survival needs,
// including auto-eat, auto-armor, auto-tool, time tracking, bed sleeping, torch
// placement, death recovery, shelter, and potions.
// This file implements best tool selection for mining and block breaking.
package survival

import (
	"strings"
)

// Tool mapping: block type -> best tool type
var blockToolMap = map[string][]string{
	"stone":          {"pickaxe"},
	"cobblestone":    {"pickaxe"},
	"deepslate":      {"pickaxe"},
	"iron_ore":       {"pickaxe"},
	"gold_ore":       {"pickaxe"},
	"diamond_ore":    {"pickaxe"},
	"coal_ore":       {"pickaxe"},
	"redstone_ore":   {"pickaxe"},
	"lapis_ore":      {"pickaxe"},
	"emerald_ore":    {"pickaxe"},
	"copper_ore":     {"pickaxe"},
	"netherrack":     {"pickaxe"},
	"obsidian":       {"pickaxe"},
	"oak_log":        {"axe"},
	"birch_log":      {"axe"},
	"spruce_log":     {"axe"},
	"jungle_log":     {"axe"},
	"acacia_log":     {"axe"},
	"dark_oak_log":   {"axe"},
	"oak_planks":     {"axe"},
	"crafting_table": {"axe"},
	"chest":          {"axe"},
	"dirt":           {"shovel"},
	"sand":           {"shovel"},
	"gravel":         {"shovel"},
	"clay":           {"shovel"},
	"soul_sand":      {"shovel"},
	"snow":           {"shovel"},
	"grass_block":    {"shovel"},
	"hay_block":      {"hoe"},
	"wheat":          {"hoe"},
	"leaves":         {"shears"},
}

// Tool tier priority (best first)
var toolTierPriority = []string{
	"netherite", "diamond", "iron", "stone", "golden", "wooden",
}

// SelectBestTool finds and equips the best tool for the given block name
func (m *Manager) SelectBestTool(blockName string) bool {
	blockName = strings.ToLower(strings.TrimPrefix(blockName, "minecraft:"))

	// Determine what tool type we need
	neededToolTypes := m.getToolTypesForBlock(blockName)
	if len(neededToolTypes) == 0 {
		return false // no specific tool needed
	}

	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	bestSlot := uint32(0)
	bestScore := -1
	found := false

	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		name = strings.TrimPrefix(name, "minecraft:")

		for _, toolType := range neededToolTypes {
			if !strings.Contains(name, toolType) {
				continue
			}

			// Calculate score based on tier
			score := 0
			for tierIdx, tierName := range toolTierPriority {
				if strings.Contains(name, tierName) {
					score = (len(toolTierPriority) - tierIdx) * 10
					break
				}
			}

			if score > bestScore {
				bestScore = score
				bestSlot = slot
				found = true
			}
		}
	}

	if found {
		if err := m.bot.EquipItem(bestSlot); err != nil {
			m.logger.Warn("Auto-tool: failed to equip", "slot", bestSlot, "err", err)
			return false
		}
		m.logger.Debug("Auto-tool: equipped best tool", "block", blockName, "slot", bestSlot)
		return true
	}

	return false
}

func (m *Manager) getToolTypesForBlock(blockName string) []string {
	// Check direct mapping
	for key, tools := range blockToolMap {
		if strings.Contains(blockName, key) {
			return tools
		}
	}

	// Generic ore detection
	if strings.Contains(blockName, "ore") {
		return []string{"pickaxe"}
	}
	// Generic log/wood detection
	if strings.Contains(blockName, "log") || strings.Contains(blockName, "wood") || strings.Contains(blockName, "planks") {
		return []string{"axe"}
	}
	return nil
}
