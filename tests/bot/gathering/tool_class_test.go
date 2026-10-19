package gathering_test

import (
	"testing"

	"bedrock-ai/internal/bot/gathering"
)

// TestStoneToolsAreMinedWithAPickaxe is the regression guard for the tool-class
// order in ToolTypeForBlock.
//
// The wood check ran first, and "stone_axe" contains "stone"... but the reverse
// is what bit: any block whose name holds a wood word was classified before the
// stone rule could run, so a stone-tier item in the world was mined with an axe
// instead of a pickaxe. Stone-specific names have to be settled first.
func TestStoneToolsAreMinedWithAPickaxe(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"minecraft:stone_pickaxe":      "pickaxe",
		"minecraft:cobblestone":        "pickaxe",
		"minecraft:deepslate_gold_ore": "pickaxe",
		"minecraft:obsidian":           "pickaxe",
		"minecraft:crying_obsidian":    "pickaxe",
		"minecraft:netherrack":         "pickaxe",
		"minecraft:ancient_debris":     "pickaxe",
		"minecraft:oak_log":            "axe",
		"minecraft:dark_oak_planks":    "axe",
		"minecraft:crimson_stem":       "axe",
		"minecraft:warped_hyphae":      "axe",
		"minecraft:dirt":               "shovel",
		"minecraft:soul_soil":          "shovel",
	}
	for block, want := range cases {
		if got := gathering.ToolTypeForBlock(block); got != want {
			t.Errorf("ToolTypeForBlock(%q) = %q, want %q", block, got, want)
		}
	}
}

// TestUnknownBlocksGetNoTool keeps the fallback honest: the bot should swing
// with whatever it holds rather than equipping a wrong tool.
func TestUnknownBlocksGetNoTool(t *testing.T) {
	t.Parallel()

	for _, block := range []string{"minecraft:diamond_block", "minecraft:glass", ""} {
		if got := gathering.ToolTypeForBlock(block); got != "" {
			t.Errorf("ToolTypeForBlock(%q) = %q, want no tool", block, got)
		}
	}
}
