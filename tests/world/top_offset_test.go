package world_test

import (
	"testing"

	"bedrock-ai/internal/bot/world"
)

func TestTopOffsetFromState(t *testing.T) {
	t.Parallel()

	half := func(v string) map[string]any { return map[string]any{"minecraft:vertical_half": v} }
	cases := []struct {
		name  string
		block string
		props map[string]any
		want  float32
	}{
		{"bottom slab", "minecraft:normal_stone_slab", half("bottom"), 0.5},
		{"top slab (1.21 string state)", "minecraft:oak_slab", half("top"), 1.0},
		{"top slab (legacy bit)", "minecraft:oak_slab", map[string]any{"top_half_bit": uint8(1)}, 1.0},
		{"double slab is a full block", "minecraft:brick_double_slab", half("bottom"), 1.0},
		{"slab without state", "minecraft:oak_slab", nil, 0.5},
		{"full block", "minecraft:stone", nil, 1.0},
	}
	for _, c := range cases {
		if got := world.TopOffsetFromState(c.block, c.props); got != c.want {
			t.Errorf("%s: top = %v, want %v", c.name, got, c.want)
		}
	}
}
