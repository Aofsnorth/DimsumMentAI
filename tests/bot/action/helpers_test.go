package action_test

import (
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"testing"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// --- NormalizeItemName ---

func TestNormalizeItemName_LowercasesAndTrims(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"  Oak_Log  ", "oak_log"},
		{"CRAFTING TABLE", "crafting_table"},
		{"minecraft:stone", "stone"},
	}
	for _, tc := range tests {
		got := action.NormalizeItemName(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeItemName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeItemName_ReplacesSpacesWithUnderscores(t *testing.T) {
	t.Parallel()
	got := action.NormalizeItemName("crafting table")
	if got != "crafting_table" {
		t.Errorf("NormalizeItemName(%q) = %q, want %q", "crafting table", got, "crafting_table")
	}
}

func TestNormalizeItemName_Aliases(t *testing.T) {
	t.Parallel()
	aliases := map[string]string{
		"craftingtable": "crafting_table",
		"craft_table":   "crafting_table",
		"workbench":     "crafting_table",
		"wood":          "oak_log",
		"kayu":          "oak_log",
		"log":           "oak_log",
		"logs":          "oak_log",
		"plank":         "oak_planks",
		"planks":        "oak_planks",
		"papan":         "oak_planks",
		"tanah":         "dirt",
		"batu":          "stone",
		"pasir":         "sand",
		"gandum":        "wheat",
		"wortel":        "carrot",
		"kentang":       "potato",
		"sapi":          "cow",
		"domba":         "sheep",
		"babi":          "pig",
		"ayam":          "chicken",
		"serigala":      "wolf",
		"dog":           "wolf",
		"kucing":        "cat",
	}
	for in, want := range aliases {
		got := action.NormalizeItemName(in)
		if got != want {
			t.Errorf("NormalizeItemName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeItemName_Passthrough(t *testing.T) {
	t.Parallel()
	got := action.NormalizeItemName("diamond_sword")
	if got != "diamond_sword" {
		t.Errorf("NormalizeItemName(%q) = %q, want passthrough %q", "diamond_sword", got, "diamond_sword")
	}
}

// --- IsWoodLike ---

func TestIsWoodLike_Logs(t *testing.T) {
	t.Parallel()
	woods := []string{"oak_log", "birch_log", "spruce_log", "jungle_log", "acacia_log", "dark_oak_log"}
	for _, w := range woods {
		if !action.IsWoodLike(w) {
			t.Errorf("IsWoodLike(%q) = false, want true", w)
		}
	}
}

func TestIsWoodLike_Aliases(t *testing.T) {
	t.Parallel()
	aliases := []string{"kayu", "oak", "birch", "spruce", "jungle", "acacia", "dark_oak", "mangrove", "cherry", "crimson_stem", "warped_stem"}
	for _, a := range aliases {
		if !action.IsWoodLike(a) {
			t.Errorf("IsWoodLike(%q) = false, want true", a)
		}
	}
}

func TestIsWoodLike_NonWood(t *testing.T) {
	t.Parallel()
	nonWoods := []string{"stone", "dirt", "diamond", "iron", "cobblestone", "sand", "water"}
	for _, nw := range nonWoods {
		if action.IsWoodLike(nw) {
			t.Errorf("IsWoodLike(%q) = true, want false", nw)
		}
	}
}

func TestIsWoodLike_CaseInsensitive(t *testing.T) {
	t.Parallel()
	if !action.IsWoodLike("OAK_LOG") {
		t.Error("IsWoodLike should be case-insensitive")
	}
}

// --- NormalizeCropType ---

func TestNormalizeCropType_Aliases(t *testing.T) {
	t.Parallel()
	aliases := map[string]string{
		"gandum":     "wheat",
		"wheat_crop": "wheat",
		"wortel":     "carrot",
		"kentang":    "potato",
		"bit":        "beetroot",
		"beet":       "beetroot",
		"labu":       "pumpkin",
		"semangka":   "melon",
		"tebu":       "sugar_cane",
		"sugarcane":  "sugar_cane",
	}
	for in, want := range aliases {
		got := action.NormalizeCropType(in)
		if got != want {
			t.Errorf("NormalizeCropType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeCropType_Empty(t *testing.T) {
	t.Parallel()
	if got := action.NormalizeCropType(""); got != "" {
		t.Errorf("NormalizeCropType(%q) = %q, want empty", "", got)
	}
}

func TestNormalizeCropType_Passthrough(t *testing.T) {
	t.Parallel()
	if got := action.NormalizeCropType("carrot"); got != "carrot" {
		t.Errorf("NormalizeCropType(%q) = %q, want passthrough", "carrot", got)
	}
}

func TestNormalizeCropType_StripsCountParam(t *testing.T) {
	t.Parallel()
	if got := action.NormalizeCropType("wheat,10"); got != "wheat" {
		t.Errorf("NormalizeCropType(%q) = %q, want %q", "wheat,10", got, "wheat")
	}
}

// --- ParseCount ---

func TestParseCount_Default(t *testing.T) {
	t.Parallel()
	if got := action.ParseCount("", 10); got != 10 {
		t.Errorf("ParseCount(%q, 10) = %d, want 10", "", got)
	}
}

// --- RecipeNeedsCraftingBench ---

func TestComputeCrafts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desired, perCraft, want int
	}{
		{4, 4, 1},  // 4 oak planks, recipe gives 4 per craft
		{8, 4, 2},  // 8 oak planks, recipe gives 4 per craft
		{1, 4, 1},  // 1 oak plank, still 1 craft
		{4, 1, 4},  // 4 items, recipe gives 1 per craft
		{0, 4, 1},  // default to 1 craft
		{5, 4, 2},  // ceil(5/4)=2
		{16, 4, 4}, // planner example: 16 planks = 4 crafts
		{4, 0, 4},  // bad output per craft, default 1
	}
	for _, tc := range tests {
		got := action.ComputeCrafts(tc.desired, tc.perCraft)
		if got != tc.want {
			t.Errorf("ComputeCrafts(%d, %d) = %d, want %d", tc.desired, tc.perCraft, got, tc.want)
		}
	}
}

func TestRecipeNeedsCraftingBench(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		recipe bot.RecipeInfo
		want   bool
	}{
		{
			name:   "inventory recipe empty block",
			recipe: bot.RecipeInfo{Block: "", Shapeless: true, Ingredients: []protocol.ItemDescriptorCount{{}}},
			want:   false,
		},
		{
			name:   "oak_planks shapeless with table tag",
			recipe: bot.RecipeInfo{Block: "crafting_table", Shapeless: true, Ingredients: []protocol.ItemDescriptorCount{{}}},
			want:   false,
		},
		{
			name:   "stick shapeless 2 ingredients with table tag",
			recipe: bot.RecipeInfo{Block: "crafting_table", Shapeless: true, Ingredients: []protocol.ItemDescriptorCount{{}, {}}},
			want:   false,
		},
		{
			name:   "shapeless 5 ingredients needs bench",
			recipe: bot.RecipeInfo{Block: "crafting_table", Shapeless: true, Ingredients: make([]protocol.ItemDescriptorCount, 5)},
			want:   true,
		},
		{
			name:   "crafting_table shaped 2x2",
			recipe: bot.RecipeInfo{Block: "crafting_table", Shapeless: false, Width: 2, Height: 2},
			want:   false,
		},
		{
			name:   "iron_pickaxe shaped 3x3",
			recipe: bot.RecipeInfo{Block: "crafting_table", Shapeless: false, Width: 3, Height: 3},
			want:   true,
		},
		{
			name:   "furnace recipe needs furnace",
			recipe: bot.RecipeInfo{Block: "furnace", Shapeless: true, Ingredients: []protocol.ItemDescriptorCount{{}}},
			want:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := action.RecipeNeedsCraftingBench(tc.recipe); got != tc.want {
				t.Errorf("RecipeNeedsCraftingBench(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestParseCount_ValidNumber(t *testing.T) {
	t.Parallel()
	if got := action.ParseCount("5", 10); got != 5 {
		t.Errorf("ParseCount(%q, 10) = %d, want 5", "5", got)
	}
}

func TestParseCount_WithExtraParams(t *testing.T) {
	t.Parallel()
	if got := action.ParseCount("3,oak_log", 10); got != 3 {
		t.Errorf("ParseCount(%q, 10) = %d, want 3", "3,oak_log", got)
	}
}

func TestParseCount_InvalidKeepsFallback(t *testing.T) {
	t.Parallel()
	// When Sscanf fails to parse, the fallback value is unchanged.
	// This documents the current behavior: invalid input does not reset to 1.
	if got := action.ParseCount("abc", 10); got != 10 {
		t.Errorf("ParseCount(%q, 10) = %d, want 10 (Sscanf failure keeps fallback)", "abc", got)
	}
}

func TestParseCount_ZeroOrNegativeBecomesOne(t *testing.T) {
	t.Parallel()
	if got := action.ParseCount("0", 10); got != 1 {
		t.Errorf("ParseCount(%q, 10) = %d, want 1", "0", got)
	}
	if got := action.ParseCount("-5", 10); got != 1 {
		t.Errorf("ParseCount(%q, 10) = %d, want 1", "-5", got)
	}
}

// --- DurationTicks ---

func TestDurationTicks_Default(t *testing.T) {
	t.Parallel()
	got := action.DurationTicks("", 5*time.Second)
	want := 5 * 20 // 5 seconds × 20 ticks/sec
	if got != want {
		t.Errorf("DurationTicks(%q, 5s) = %d, want %d", "", got, want)
	}
}

func TestDurationTicks_ValidSeconds(t *testing.T) {
	t.Parallel()
	got := action.DurationTicks("10", 5*time.Second)
	want := 10 * 20
	if got != want {
		t.Errorf("DurationTicks(%q, 5s) = %d, want %d", "10", got, want)
	}
}

func TestDurationTicks_ClampsMinimum(t *testing.T) {
	t.Parallel()
	got := action.DurationTicks("0", 5*time.Second)
	want := 1 * 20 // minimum 1 second
	if got != want {
		t.Errorf("DurationTicks(%q, 5s) = %d, want %d (clamped to min)", "0", got, want)
	}
}

func TestDurationTicks_ClampsMaximum(t *testing.T) {
	t.Parallel()
	got := action.DurationTicks("100", 5*time.Second)
	want := 60 * 20 // maximum 60 seconds
	if got != want {
		t.Errorf("DurationTicks(%q, 5s) = %d, want %d (clamped to max)", "100", got, want)
	}
}

func TestDurationTicks_WithExtraParams(t *testing.T) {
	t.Parallel()
	got := action.DurationTicks("3,some_param", 5*time.Second)
	want := 3 * 20
	if got != want {
		t.Errorf("DurationTicks(%q, 5s) = %d, want %d", "3,some_param", got, want)
	}
}
