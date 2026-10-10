package gathering_test

import (
	"testing"

	"bedrock-ai/internal/bot/gathering"
)

// The generic axe tier used a bare substring test, and "pickaxe" contains
// "axe" — a stone pickaxe sat in the axe slot and the trunk was chopped with
// the wrong tool while the bot believed it had equipped its best axe.
func TestIsAxeName(t *testing.T) {
	t.Parallel()

	axes := []string{
		"minecraft:netherite_axe", "diamond_axe", "iron_axe", "stone_axe",
		"wooden_axe", "golden_axe", "Kapak_Besi", "custom_server_axe",
	}
	for _, name := range axes {
		if !gathering.IsAxeName(name) {
			t.Errorf("IsAxeName(%q) = false, want true", name)
		}
	}

	notAxes := []string{
		"stone_pickaxe", "netherite_pickaxe", "wooden_pickaxe",
		"iron_shovel", "stone_sword", "diamond_hoe", "",
	}
	for _, name := range notAxes {
		if gathering.IsAxeName(name) {
			t.Errorf("IsAxeName(%q) = true, want false", name)
		}
	}
}

// The tool-type mapping decides which tool class digs/chops/mines a block.
// grass_block joined the shovel set after the bot dug dirt with a hoe on a
// grass field; a hoe is never an acceptable stand-in for anything.
func TestToolTypeForBlock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		block string
		want  string
	}{
		{"dirt", "shovel"},
		{"grass_block", "shovel"},
		{"coarse_dirt", "shovel"},
		{"sand", "shovel"},
		{"oak_log", "axe"},
		{"oak_planks", "axe"},
		{"stone", "pickaxe"},
		{"cobblestone", "pickaxe"},
		{"iron_ore", "pickaxe"},
		{"cookie", ""},
	}

	for _, tc := range cases {
		if got := gathering.ToolTypeForBlock(tc.block); got != tc.want {
			t.Errorf("ToolTypeForBlock(%q) = %q, want %q", tc.block, got, tc.want)
		}
	}
}
