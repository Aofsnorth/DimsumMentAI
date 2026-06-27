package bot

import (
	"testing"
)

func TestFormatItemName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"minecraft:oak_planks", "Oak Planks"},
		{"oak_planks", "Oak Planks"},
		{"minecraft:diamond_sword", "Diamond Sword"},
		{"diamond_sword", "Diamond Sword"},
		{"crafting_table", "Crafting Table"},
		{"", ""},
		{"minecraft:", ""},
		{"item_123", "Item 123"},
		{"OAK_LOG", "Oak Log"},
		{"  minecraft:cobblestone  ", "Cobblestone"},
	}
	for _, tc := range tests {
		got := FormatItemName(tc.in)
		if got != tc.want {
			t.Errorf("FormatItemName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
