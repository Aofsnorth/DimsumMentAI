package farming_test

import (
	"testing"

	"bedrock-ai/internal/bot/farming"
)

// TestMatureStagePerCrop pins the per-crop maturity table. These genuinely
// differ — a bot that assumes one shared "fully grown" number harvests nether
// wart at stage 1 and a cocoa pod at stage 0.
func TestMatureStagePerCrop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		block string
		want  int
	}{
		{"minecraft:wheat", 7},
		{"minecraft:carrots", 7},
		{"minecraft:potatoes", 7},
		{"minecraft:beetroots", 7},
		{"minecraft:nether_wart", 3},
		{"minecraft:cocoa", 2},
		{"minecraft:sweet_berry_bush", 3},
	}

	for _, tc := range tests {
		got, ok := farming.MatureStage(tc.block)
		if !ok {
			t.Errorf("MatureStage(%q) reported the block has no maturity; it is a staged crop", tc.block)
			continue
		}
		if got != tc.want {
			t.Errorf("MatureStage(%q) = %d, want %d", tc.block, got, tc.want)
		}
	}
}

// TestMatureStageIgnoresNamespaceAndCase is the tolerance real palette names
// arrive with. Bedrock spells the block "minecraft:wheat"; a name that is not
// normalised exactly matches nothing.
func TestMatureStageIgnoresNamespaceAndCase(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"minecraft:wheat", "Minecraft:Wheat", "  wheat  "} {
		got, ok := farming.MatureStage(name)
		if !ok || got != 7 {
			t.Errorf("MatureStage(%q) = (%d, %t), want (7, true)", name, got, ok)
		}
	}
}

// TestMatureStageIsFalseForCropsGrownByBreaking is the distinction the roadmap
// cares about: bamboo and kelp have no age. They are harvestable at every
// stage, and a maturity check that returned a number for them would either
// stall forever or invent a stage.
func TestMatureStageIsFalseForCropsGrownByBreaking(t *testing.T) {
	t.Parallel()

	for _, block := range []string{
		"minecraft:bamboo",
		"minecraft:kelp",
		"minecraft:sugar_cane",
		"minecraft:cactus",
		"minecraft:pumpkin",
		"minecraft:melon_block",
	} {
		if stage, ok := farming.MatureStage(block); ok {
			t.Errorf("MatureStage(%q) = %d, true; it is harvested by breaking, not by age", block, stage)
		}
	}
}

// TestMatureStageIsFalseForNonCrops keeps the table from swallowing blocks that
// merely contain a crop name. The old code used strings.Contains, so
// "wheat_seeds" and a hypothetical "wheat_barrel" both counted as wheat.
func TestMatureStageIsFalseForNonCrops(t *testing.T) {
	t.Parallel()

	for _, block := range []string{
		"minecraft:air",
		"minecraft:stone",
		"minecraft:farmland",
		"wheat_seeds",
		"minecraft:wheat_barrel",
		"",
	} {
		if stage, ok := farming.MatureStage(block); ok {
			t.Errorf("MatureStage(%q) = %d, true; it is not a staged crop", block, stage)
		}
	}
}

// TestCropOfCoversTheRoadmapCropList is 5.1 plus 5.3: every crop the roadmap
// names must resolve to its own kind, and an unknown block must resolve to
// nothing rather than to a default.
func TestCropOfCoversTheRoadmapCropList(t *testing.T) {
	t.Parallel()

	tests := map[string]farming.Crop{
		"minecraft:wheat":            farming.CropWheat,
		"minecraft:carrots":          farming.CropCarrot,
		"minecraft:potatoes":         farming.CropPotato,
		"minecraft:beetroots":        farming.CropBeetroot,
		"minecraft:nether_wart":      farming.CropNetherWart,
		"minecraft:cocoa":            farming.CropCocoa,
		"minecraft:bamboo":           farming.CropBamboo,
		"minecraft:kelp":             farming.CropKelp,
		"minecraft:sweet_berry_bush": farming.CropSweetBerry,
		"minecraft:pumpkin_stem":     farming.CropPumpkinStem,
		"minecraft:melon_stem":       farming.CropMelonStem,
		"minecraft:sugar_cane":       farming.CropSugarCane,
		"minecraft:cactus":           farming.CropCactus,
	}

	for block, want := range tests {
		if got := farming.CropOf(block); got != want {
			t.Errorf("CropOf(%q) = %q, want %q", block, got, want)
		}
	}

	for _, block := range []string{"minecraft:stone", "", "minecraft:wheat_seeds"} {
		if got := farming.CropOf(block); got != farming.CropUnknown {
			t.Errorf("CropOf(%q) = %q, want CropUnknown", block, got)
		}
	}
}

// TestReadStageParsesEveryPaletteShape is the real risk in reading a stage. The
// value inside a block-state property map is whatever the palette decoder put
// there, and it is not always one type. A parser that only handles int32 turns
// every other shape into "unknown", which quietly means "never harvest".
func TestReadStageParsesEveryPaletteShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		props map[string]any
		want  int
	}{
		{"int32", map[string]any{"growth": int32(7)}, 7},
		{"int", map[string]any{"growth": int(3)}, 3},
		{"int8", map[string]any{"growth": int8(2)}, 2},
		{"int64", map[string]any{"growth": int64(6)}, 6},
		{"uint8", map[string]any{"growth": uint8(4)}, 4},
		{"uint32", map[string]any{"growth": uint32(1)}, 1},
		{"float32", map[string]any{"growth": float32(5)}, 5},
		{"float64", map[string]any{"growth": float64(2)}, 2},
		{"string", map[string]any{"growth": "7"}, 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := farming.ReadStage(tc.props)
			if !ok || got != tc.want {
				t.Errorf("ReadStage(%v) = (%d, %t), want (%d, true)", tc.props, got, ok, tc.want)
			}
		})
	}
}

// TestReadStageRefusesRatherThanGuesses is the honesty property the whole
// package rests on. An unreadable stage must be "unknown", never a default that
// happens to be 0 or 7.
func TestReadStageRefusesRatherThanGuesses(t *testing.T) {
	t.Parallel()

	for _, props := range []map[string]any{
		nil,
		{},
		{"growth": nil},
		{"growth": "seven"},
		{"growth": "7.5"},
		{"growth": true},
		{"growth": -1},
		{"growth": 99},
		{"stone_type": "granite"},
	} {
		if stage, ok := farming.ReadStage(props); ok {
			t.Errorf("ReadStage(%v) = (%d, true); an unreadable stage must report unknown", props, stage)
		}
	}
}

// TestReadStageFallsBackToAge covers the second spelling Bedrock has used for
// the same property. Refusing to read "age" is a silent "never harvest", so the
// fallback has to exist.
func TestReadStageFallsBackToAge(t *testing.T) {
	t.Parallel()

	got, ok := farming.ReadStage(map[string]any{"age": int32(7)})
	if !ok || got != 7 {
		t.Errorf("ReadStage(age=7) = (%d, %t), want (7, true)", got, ok)
	}

	// "growth" wins when both are present, because it is the live key.
	got, ok = farming.ReadStage(map[string]any{"growth": int32(3), "age": int32(7)})
	if !ok || got != 3 {
		t.Errorf("ReadStage(growth=3, age=7) = (%d, %t), want (3, true)", got, ok)
	}
}

// TestIsStemNamesTheBlocksThatDestroyTheCrop is the specific defect the roadmap
// records: the old auto-detect matched "pumpkin" and would happily break a
// pumpkin stem, which is the one block that must never be broken.
func TestIsStemNamesTheBlocksThatDestroyTheCrop(t *testing.T) {
	t.Parallel()

	for _, block := range []string{"minecraft:pumpkin_stem", "minecraft:melon_stem"} {
		if !farming.IsStem(block) {
			t.Errorf("IsStem(%q) = false; breaking this block destroys the crop", block)
		}
	}
	for _, block := range []string{"minecraft:pumpkin", "minecraft:melon_block", "minecraft:wheat", ""} {
		if farming.IsStem(block) {
			t.Errorf("IsStem(%q) = true; it is not a stem", block)
		}
	}
}
