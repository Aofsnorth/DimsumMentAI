package gathering

import (
	"strings"
	"time"
)

// blockBreakDuration returns how long the bot should swing before sending
// PredictDestroyBlock. Servers reject destroy packets that arrive earlier than
// the expected hardness×toolSpeed time, leaving the block intact and the swing
// animation looking pointless. Values track vanilla Bedrock hardness with a
// small tolerance subtracted (servers typically accept ~50ms early).
//
// blockName is matched as a substring (e.g. "minecraft:oak_log" matches "log").
// toolName is the currently equipped item; empty string means bare-handed.
func blockBreakDuration(blockName, toolName string) time.Duration {
	name := strings.ToLower(strings.TrimPrefix(blockName, "minecraft:"))
	tool := strings.ToLower(toolName)

	// Block hardness in seconds (vanilla base time × 1.5 for hand).
	var hardness float64
	var preferredTool string
	switch {
	case strings.Contains(name, "obsidian"):
		hardness, preferredTool = 50.0, "pickaxe"
	case strings.Contains(name, "ore") && !strings.Contains(name, "redstone") && !strings.Contains(name, "coal"):
		hardness, preferredTool = 3.0, "pickaxe"
	case strings.Contains(name, "redstone_ore") || strings.Contains(name, "coal_ore"):
		hardness, preferredTool = 3.0, "pickaxe"
	case strings.Contains(name, "deepslate"):
		hardness, preferredTool = 3.5, "pickaxe"
	case strings.Contains(name, "cobble") || name == "stone":
		hardness, preferredTool = 1.5, "pickaxe"
	case strings.Contains(name, "stone"):
		hardness, preferredTool = 1.5, "pickaxe"
	case strings.Contains(name, "iron_block"):
		hardness, preferredTool = 5.0, "pickaxe"
	case strings.Contains(name, "log") || strings.Contains(name, "wood") || strings.Contains(name, "planks"):
		hardness, preferredTool = 2.0, "axe"
	case strings.Contains(name, "leaves"):
		hardness, preferredTool = 0.2, "shears"
	case strings.Contains(name, "grass") && !strings.Contains(name, "block"):
		// Tall grass / fern — instant with shears, near-instant by hand.
		hardness, preferredTool = 0.1, "shears"
	case strings.Contains(name, "sand") || strings.Contains(name, "gravel"):
		hardness, preferredTool = 0.5, "shovel"
	case strings.Contains(name, "dirt") || strings.Contains(name, "grass_block") || strings.Contains(name, "podzol") || strings.Contains(name, "mycelium"):
		hardness, preferredTool = 0.5, "shovel"
	case strings.Contains(name, "snow"):
		hardness, preferredTool = 0.2, "shovel"
	case strings.Contains(name, "clay"):
		hardness, preferredTool = 0.6, "shovel"
	default:
		// Unknown: assume modest hardness, hand-mineable.
		hardness, preferredTool = 1.0, ""
	}

	// Tool multiplier: only the correct tool category applies. Wrong tool
	// gives hand-speed.
	speed := 1.0
	if preferredTool != "" && strings.Contains(tool, preferredTool) {
		switch {
		case strings.Contains(tool, "netherite"):
			speed = 9.0
		case strings.Contains(tool, "diamond"):
			speed = 8.0
		case strings.Contains(tool, "iron"):
			speed = 6.0
		case strings.Contains(tool, "stone"):
			speed = 4.0
		case strings.Contains(tool, "wooden"), strings.Contains(tool, "wood"):
			speed = 2.0
		case strings.Contains(tool, "golden"), strings.Contains(tool, "gold"):
			speed = 12.0
		}
	}

	// Vanilla formula: base = hardness × (canHarvest ? 1.5 : 5.0). Bot is
	// considered eligible to harvest its target (we already pick the right
	// tool category), so use 1.5.
	seconds := hardness * 1.5 / speed
	if seconds < 0.15 {
		seconds = 0.15 // floor so the swing has at least a tick to animate
	}

	// Subtract a small tolerance (server accepts destroys ~50ms early), but
	// never below the floor.
	ms := int(seconds*1000) - 80
	if ms < 150 {
		ms = 150
	}
	return time.Duration(ms) * time.Millisecond
}

// equippedToolName returns the item name currently in the bot's held slot.
// Returns empty string when the held slot is empty or unknown.
func (bm *BlockMiner) equippedToolName() string {
	bot := bm.rg.bot
	slot := bot.GetHeldItemSlot()
	inv := bot.GetInventorySlots()
	item, ok := inv[slot]
	if !ok || item.Count == 0 {
		return ""
	}
	names := bot.GetItemNames()
	return names[item.NetworkID]
}
