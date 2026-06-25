package action

import (
	"testing"
	"time"
)

// --- normalizeItemName ---

func TestNormalizeItemName_LowercasesAndTrims(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"  Oak_Log  ", "oak_log"},
		{"CRAFTING TABLE", "crafting_table"},
		{"minecraft:stone", "stone"},
	}
	for _, tc := range tests {
		got := normalizeItemName(tc.in)
		if got != tc.want {
			t.Errorf("normalizeItemName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeItemName_ReplacesSpacesWithUnderscores(t *testing.T) {
	t.Parallel()
	got := normalizeItemName("crafting table")
	if got != "crafting_table" {
		t.Errorf("normalizeItemName(%q) = %q, want %q", "crafting table", got, "crafting_table")
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
		got := normalizeItemName(in)
		if got != want {
			t.Errorf("normalizeItemName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeItemName_Passthrough(t *testing.T) {
	t.Parallel()
	got := normalizeItemName("diamond_sword")
	if got != "diamond_sword" {
		t.Errorf("normalizeItemName(%q) = %q, want passthrough %q", "diamond_sword", got, "diamond_sword")
	}
}

// --- isWoodLike ---

func TestIsWoodLike_Logs(t *testing.T) {
	t.Parallel()
	woods := []string{"oak_log", "birch_log", "spruce_log", "jungle_log", "acacia_log", "dark_oak_log"}
	for _, w := range woods {
		if !isWoodLike(w) {
			t.Errorf("isWoodLike(%q) = false, want true", w)
		}
	}
}

func TestIsWoodLike_Aliases(t *testing.T) {
	t.Parallel()
	aliases := []string{"kayu", "oak", "birch", "spruce", "jungle", "acacia", "dark_oak", "mangrove", "cherry", "crimson_stem", "warped_stem"}
	for _, a := range aliases {
		if !isWoodLike(a) {
			t.Errorf("isWoodLike(%q) = false, want true", a)
		}
	}
}

func TestIsWoodLike_NonWood(t *testing.T) {
	t.Parallel()
	nonWoods := []string{"stone", "dirt", "diamond", "iron", "cobblestone", "sand", "water"}
	for _, nw := range nonWoods {
		if isWoodLike(nw) {
			t.Errorf("isWoodLike(%q) = true, want false", nw)
		}
	}
}

func TestIsWoodLike_CaseInsensitive(t *testing.T) {
	t.Parallel()
	if !isWoodLike("OAK_LOG") {
		t.Error("isWoodLike should be case-insensitive")
	}
}

// --- normalizeCropType ---

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
		got := normalizeCropType(in)
		if got != want {
			t.Errorf("normalizeCropType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeCropType_Empty(t *testing.T) {
	t.Parallel()
	if got := normalizeCropType(""); got != "" {
		t.Errorf("normalizeCropType(%q) = %q, want empty", "", got)
	}
}

func TestNormalizeCropType_Passthrough(t *testing.T) {
	t.Parallel()
	if got := normalizeCropType("carrot"); got != "carrot" {
		t.Errorf("normalizeCropType(%q) = %q, want passthrough", "carrot", got)
	}
}

func TestNormalizeCropType_StripsCountParam(t *testing.T) {
	t.Parallel()
	if got := normalizeCropType("wheat,10"); got != "wheat" {
		t.Errorf("normalizeCropType(%q) = %q, want %q", "wheat,10", got, "wheat")
	}
}

// --- parseCount ---

func TestParseCount_Default(t *testing.T) {
	t.Parallel()
	if got := parseCount("", 10); got != 10 {
		t.Errorf("parseCount(%q, 10) = %d, want 10", "", got)
	}
}

func TestParseCount_ValidNumber(t *testing.T) {
	t.Parallel()
	if got := parseCount("5", 10); got != 5 {
		t.Errorf("parseCount(%q, 10) = %d, want 5", "5", got)
	}
}

func TestParseCount_WithExtraParams(t *testing.T) {
	t.Parallel()
	if got := parseCount("3,oak_log", 10); got != 3 {
		t.Errorf("parseCount(%q, 10) = %d, want 3", "3,oak_log", got)
	}
}

func TestParseCount_InvalidKeepsFallback(t *testing.T) {
	t.Parallel()
	// When Sscanf fails to parse, the fallback value is unchanged.
	// This documents the current behavior: invalid input does not reset to 1.
	if got := parseCount("abc", 10); got != 10 {
		t.Errorf("parseCount(%q, 10) = %d, want 10 (Sscanf failure keeps fallback)", "abc", got)
	}
}

func TestParseCount_ZeroOrNegativeBecomesOne(t *testing.T) {
	t.Parallel()
	if got := parseCount("0", 10); got != 1 {
		t.Errorf("parseCount(%q, 10) = %d, want 1", "0", got)
	}
	if got := parseCount("-5", 10); got != 1 {
		t.Errorf("parseCount(%q, 10) = %d, want 1", "-5", got)
	}
}

// --- durationTicks ---

func TestDurationTicks_Default(t *testing.T) {
	t.Parallel()
	got := durationTicks("", 5*time.Second)
	want := 5 * 20 // 5 seconds × 20 ticks/sec
	if got != want {
		t.Errorf("durationTicks(%q, 5s) = %d, want %d", "", got, want)
	}
}

func TestDurationTicks_ValidSeconds(t *testing.T) {
	t.Parallel()
	got := durationTicks("10", 5*time.Second)
	want := 10 * 20
	if got != want {
		t.Errorf("durationTicks(%q, 5s) = %d, want %d", "10", got, want)
	}
}

func TestDurationTicks_ClampsMinimum(t *testing.T) {
	t.Parallel()
	got := durationTicks("0", 5*time.Second)
	want := 1 * 20 // minimum 1 second
	if got != want {
		t.Errorf("durationTicks(%q, 5s) = %d, want %d (clamped to min)", "0", got, want)
	}
}

func TestDurationTicks_ClampsMaximum(t *testing.T) {
	t.Parallel()
	got := durationTicks("100", 5*time.Second)
	want := 60 * 20 // maximum 60 seconds
	if got != want {
		t.Errorf("durationTicks(%q, 5s) = %d, want %d (clamped to max)", "100", got, want)
	}
}

func TestDurationTicks_WithExtraParams(t *testing.T) {
	t.Parallel()
	got := durationTicks("3,some_param", 5*time.Second)
	want := 3 * 20
	if got != want {
		t.Errorf("durationTicks(%q, 5s) = %d, want %d", "3,some_param", got, want)
	}
}
