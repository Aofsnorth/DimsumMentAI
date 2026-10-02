package recipe_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/inventory/recipe"
)

// TestBannerPatternStagesTheBannerAndTheDye is acceptance 4.7. The loom is a
// two-input station: a banner to decorate and a dye to colour the pattern.
func TestBannerPatternStagesTheBannerAndTheDye(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 1, Name: "white_banner", Count: 3},
		{Slot: 6, Name: "red_dye", Count: 5},
	}
	plan, err := recipe.PlanBannerPattern(inv, "minecraft:globe")
	if err != nil {
		t.Fatalf("PlanBannerPattern() error = %v", err)
	}
	if plan.Station.Kind != recipe.KindLoom {
		t.Fatalf("plan station = %q, want loom", plan.Station.Kind)
	}
	if plan.Pattern != "minecraft:globe" {
		t.Errorf("Pattern = %q, want minecraft:globe", plan.Pattern)
	}
	if plan.UsesRecipe() {
		t.Error("a loom plan carries a recipe network ID; the loom has no CraftingData recipe")
	}
	if len(plan.Inputs) != 2 {
		t.Fatalf("plan has %d inputs, want banner + dye: %+v", len(plan.Inputs), plan.Inputs)
	}
	banner, ok := plan.Input(recipe.RoleInput)
	if !ok {
		t.Fatal("no banner staged")
	}
	if banner.ItemName != "white_banner" || banner.Count != 1 {
		t.Errorf("banner = %+v, want one white_banner", banner)
	}
	dye, ok := plan.Input(recipe.RoleMaterial)
	if !ok {
		t.Fatal("no dye staged")
	}
	if dye.ItemName != "red_dye" || dye.Count != 1 {
		t.Errorf("dye = %+v, want one red_dye", dye)
	}
}

// TestBannerPatternColoursTheBannerFromTheDye is the naming rule a confirmation
// check depends on. Applying a pattern replaces the banner's colour with the
// dye's, so a red dye on a white banner must be confirmed as a red_banner —
// waiting for a white_banner would time out on a craft that worked.
func TestBannerPatternColoursTheBannerFromTheDye(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "white_banner", Count: 1},
		{Slot: 1, Name: "lime_dye", Count: 1},
	}
	plan, err := recipe.PlanBannerPattern(inv, "flower")
	if err != nil {
		t.Fatalf("PlanBannerPattern() error = %v", err)
	}
	if plan.ResultName != "lime_banner" {
		t.Errorf("ResultName = %q, want lime_banner", plan.ResultName)
	}
	if plan.ResultCount != 1 {
		t.Errorf("ResultCount = %d, want 1", plan.ResultCount)
	}
}

// TestBannerPatternNeedsADye keeps a plan from staging a banner into a station
// that will never produce a result without a dye.
func TestBannerPatternNeedsADye(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{{Slot: 0, Name: "white_banner", Count: 1}}
	_, err := recipe.PlanBannerPattern(inv, "globe")
	if err == nil {
		t.Fatal("a plan was produced with no dye in the bag")
	}
	if !strings.Contains(err.Error(), "dye") {
		t.Errorf("error = %v, want it to name the missing dye", err)
	}
}

// TestBannerPatternNeedsABanner is the mirror guard: a bag full of dye and no
// banner cannot be patterned.
func TestBannerPatternNeedsABanner(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{{Slot: 0, Name: "red_dye", Count: 8}}
	_, err := recipe.PlanBannerPattern(inv, "globe")
	if err == nil {
		t.Fatal("a plan was produced with no banner in the bag")
	}
	if !strings.Contains(err.Error(), "banner") {
		t.Errorf("error = %v, want it to name the missing banner", err)
	}
}

// TestUnknownBannerPatternIsRejected keeps a typo from turning into a pattern
// identifier the server has never heard of. The error has to name the pattern,
// because "loom failed" is not a log line anyone can act on.
func TestUnknownBannerPatternIsRejected(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "white_banner", Count: 1},
		{Slot: 1, Name: "red_dye", Count: 1},
	}
	_, err := recipe.PlanBannerPattern(inv, "globe_of_doom")
	if err == nil {
		t.Fatal("an unknown pattern produced a plan")
	}
	if !strings.Contains(err.Error(), "globe_of_doom") {
		t.Errorf("error = %v, want it to name the unknown pattern", err)
	}
	if _, err := recipe.PlanBannerPattern(inv, "  "); err == nil {
		t.Error("an empty pattern produced a plan")
	}
}

// TestFindBannerPatternResolvesBothSpellings covers how a caller is likely to
// ask for a pattern: the full protocol identifier or the bare suffix.
func TestFindBannerPatternResolvesBothSpellings(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"minecraft:globe", "globe", "MINECRAFT:GLOBE"} {
		got, ok := recipe.FindBannerPattern(id)
		if !ok {
			t.Errorf("FindBannerPattern(%q) not found", id)
			continue
		}
		if got.ID != "minecraft:globe" {
			t.Errorf("FindBannerPattern(%q) = %q, want minecraft:globe", id, got.ID)
		}
	}
	if _, ok := recipe.FindBannerPattern("nope"); ok {
		t.Error("FindBannerPattern invented a pattern")
	}
	if _, ok := recipe.FindBannerPattern(""); ok {
		t.Error("FindBannerPattern matched an empty identifier")
	}
}

// TestBorderPatternNeedsNoDyeOfItsOwn covers the one loom operation that is not
// "banner plus dye". The base border pattern is what a plain dye produces, so
// staging a second dye would throw one away for nothing.
func TestBorderPatternNeedsNoDyeOfItsOwn(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{{Slot: 0, Name: "white_banner", Count: 1}}
	plan, err := recipe.PlanBannerPattern(inv, "minecraft:border")
	if err != nil {
		t.Fatalf("PlanBannerPattern() error = %v", err)
	}
	if len(plan.Inputs) != 1 {
		t.Fatalf("plan staged %d inputs, want only the banner: %+v", len(plan.Inputs), plan.Inputs)
	}
	if plan.Inputs[0].Slot.Role != recipe.RoleInput {
		t.Errorf("staged role %q, want input", plan.Inputs[0].Slot.Role)
	}
}

// TestBannerPatternFindsAColouredBannerWhenTheDyeIsAbsent is a judgement call
// this package makes explicitly. A banner that already carries the dye's colour
// needs no second dye; a banner of a different colour does. The rule is that the
// dye is only staged when one is actually required.
func TestBannerPatternFindsAColouredBannerWhenTheDyeIsAbsent(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{{Slot: 0, Name: "red_banner", Count: 1}}
	plan, err := recipe.PlanBannerPattern(inv, "border")
	if err != nil {
		t.Fatalf("PlanBannerPattern() error = %v", err)
	}
	if plan.ResultName != "red_banner" {
		t.Errorf("ResultName = %q, want the banner to keep its own colour", plan.ResultName)
	}
}

// TestBannerPatternIgnoresBagOrder is the determinism guarantee for the loom
// specifically: the result colour follows the dye, so picking "the first dye in
// the list" would produce a different banner on every call when the bag holds
// two. A hand-built snapshot is deliberately NOT sorted here — resolution has to
// be by lowest slot, not by position in the slice.
func TestBannerPatternIgnoresBagOrder(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 9, Name: "blue_dye", Count: 1},
		{Slot: 2, Name: "red_dye", Count: 1},
		{Slot: 14, Name: "white_banner", Count: 1},
		{Slot: 1, Name: "white_wall_banner", Count: 1},
	}
	plan, err := recipe.PlanBannerPattern(inv, "globe")
	if err != nil {
		t.Fatalf("PlanBannerPattern() error = %v", err)
	}
	if plan.ResultName != "red_banner" {
		t.Errorf("ResultName = %q, want red_banner from the lowest dye slot", plan.ResultName)
	}
	dye, _ := plan.Input(recipe.RoleMaterial)
	if dye.SourceSlot != 2 {
		t.Errorf("dye sourced from slot %d, want the lowest at 2", dye.SourceSlot)
	}
	banner, _ := plan.Input(recipe.RoleInput)
	if banner.SourceSlot != 14 {
		t.Errorf("banner sourced from slot %d, want the standing banner at 14, not the wall banner at 1", banner.SourceSlot)
	}
}

// TestNetheriteUpgradeIgnoresBagOrder is the same guarantee for the smithing
// table, where two smithing templates in the bag would otherwise decide which
// one gets consumed.
func TestNetheriteUpgradeIgnoresBagOrder(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 11, Name: "netherite_upgrade_smithing_template", Count: 1},
		{Slot: 0, Name: "netherite_upgrade", Count: 1},
		{Slot: 6, Name: "diamond_sword", Count: 1},
		{Slot: 8, Name: "netherite_ingot", Count: 3},
	}
	plan, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
	if err != nil {
		t.Fatalf("PlanNetheriteUpgrade() error = %v", err)
	}
	tmpl, _ := plan.Input(recipe.RoleTemplate)
	if tmpl.SourceSlot != 0 {
		t.Errorf("template sourced from slot %d, want the lowest at 0", tmpl.SourceSlot)
	}
}

// TestBannerPatternsCatalogueCoversTheAcceptanceCase keeps 4.7 honest: the
// pattern the roadmap asks for has to exist, and every listed identifier has to
// be namespaced the way the protocol field expects.
func TestBannerPatternsCatalogueCoversTheAcceptanceCase(t *testing.T) {
	t.Parallel()

	patterns := recipe.BannerPatterns()
	if len(patterns) == 0 {
		t.Fatal("the loom pattern catalogue is empty")
	}
	seen := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		if !strings.HasPrefix(p.ID, "minecraft:") {
			t.Errorf("pattern %q is not namespaced", p.ID)
		}
		if seen[p.ID] {
			t.Errorf("pattern %q is listed twice", p.ID)
		}
		seen[p.ID] = true
		if p.ConsumesDye && p.DyeName == "" {
			t.Errorf("pattern %q needs a dye but names none", p.ID)
		}
	}
	if _, ok := recipe.FindBannerPattern("minecraft:creeper"); !ok {
		t.Error("the creeper pattern is missing from the catalogue")
	}
}
