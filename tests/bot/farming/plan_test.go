package farming_test

import (
	"testing"

	"bedrock-ai/internal/bot/farming"
)

// TestIsHarvestSafeNeedsAKnownStage is the core of 5.1. A staged crop whose
// stage could not be read is not a crop that is ready; it is a crop about which
// nothing is known. Breaking it destroys seed, which is the single most
// expensive mistake a farming bot can make.
func TestIsHarvestSafeNeedsAKnownStage(t *testing.T) {
	t.Parallel()

	if farming.IsHarvestSafe(farming.CropWheat, 7, false) {
		t.Error("wheat with an unknown stage was marked harvestable")
	}
	if farming.IsHarvestSafe(farming.CropWheat, 0, false) {
		t.Error("a freshly planted seed with an unknown stage was marked harvestable")
	}
}

// TestIsHarvestSafeOnlyAtTheRightStage is the "wheat only at age 7" acceptance
// criterion, and the other three stages must be honest about themselves.
func TestIsHarvestSafeOnlyAtTheRightStage(t *testing.T) {
	t.Parallel()

	if farming.IsHarvestSafe(farming.CropWheat, 0, true) {
		t.Error("stage 0 wheat was marked harvestable; that is the seed the player just planted")
	}
	for stage := 1; stage < 7; stage++ {
		if farming.IsHarvestSafe(farming.CropWheat, stage, true) {
			t.Errorf("wheat at stage %d was marked harvestable; 7 is mature", stage)
		}
	}
	if !farming.IsHarvestSafe(farming.CropWheat, 7, true) {
		t.Error("wheat at stage 7 was not marked harvestable")
	}
}

// TestIsHarvestSafeUsesEachCropsOwnStage is why the table is per crop. A shared
// constant would break three of these.
func TestIsHarvestSafeUsesEachCropsOwnStage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		crop  farming.Crop
		stage int
		safe  bool
	}{
		{farming.CropNetherWart, 2, false},
		{farming.CropNetherWart, 3, true},
		{farming.CropCocoa, 1, false},
		{farming.CropCocoa, 2, true},
		{farming.CropCocoa, 7, true}, // over-stage is still picked
		{farming.CropSweetBerry, 2, false},
		{farming.CropSweetBerry, 3, true},
		{farming.CropCarrot, 6, false},
		{farming.CropCarrot, 7, true},
		{farming.CropPotato, 7, true},
		{farming.CropBeetroot, 7, true},
	}

	for _, tc := range tests {
		if got := farming.IsHarvestSafe(tc.crop, tc.stage, true); got != tc.safe {
			t.Errorf("IsHarvestSafe(%q, %d, true) = %t, want %t", tc.crop, tc.stage, got, tc.safe)
		}
	}
}

// TestStemsAreNeverHarvested is the defect the roadmap records against the old
// code. A stem does have a maturity number, which is exactly why the decision
// has to live in IsHarvestSafe and not in the table.
func TestStemsAreNeverHarvested(t *testing.T) {
	t.Parallel()

	for _, crop := range []farming.Crop{farming.CropPumpkinStem, farming.CropMelonStem} {
		for stage := 0; stage <= 7; stage++ {
			if farming.IsHarvestSafe(crop, stage, true) {
				t.Errorf("IsHarvestSafe(%q, %d, true) = true; breaking this block destroys the crop", crop, stage)
			}
		}
		if got := farming.HarvestRefusal(crop, 7, true); got == "" {
			t.Errorf("HarvestRefusal(%q, 7, true) = \"\"; a refusal is owed even for a fully grown stem", crop)
		}
	}
}

// TestCropsGrownByBreakingNeedNoStage covers 5.3's bamboo and kelp: they are
// taken by breaking, so refusing them for want of a stage would be a stall.
func TestCropsGrownByBreakingNeedNoStage(t *testing.T) {
	t.Parallel()

	for _, crop := range []farming.Crop{
		farming.CropBamboo, farming.CropKelp, farming.CropSugarCane, farming.CropCactus,
	} {
		if !farming.IsHarvestSafe(crop, 0, false) {
			t.Errorf("IsHarvestSafe(%q, 0, false) = false; it has no age and is always breakable", crop)
		}
	}
}

// TestUnknownCropsAreNeverHarvested keeps the default on the safe side.
func TestUnknownCropsAreNeverHarvested(t *testing.T) {
	t.Parallel()

	for _, crop := range []farming.Crop{farming.CropUnknown, farming.Crop("stone"), farming.Crop("wheat_seeds")} {
		if farming.IsHarvestSafe(crop, 7, true) {
			t.Errorf("IsHarvestSafe(%q, 7, true) = true; it is not a crop this package grows", crop)
		}
		if farming.IsHarvestSafe(crop, 0, false) {
			t.Errorf("IsHarvestSafe(%q, 0, false) = true; it is not a crop this package grows", crop)
		}
	}
}

// TestHarvestRefusalExplainsItself. A refusal that returns an empty reason is
// indistinguishable from "allowed", and the log line is the only place a
// maintainer can see why the bot walked past a field.
func TestHarvestRefusalExplainsItself(t *testing.T) {
	t.Parallel()

	if farming.HarvestRefusal(farming.CropWheat, 7, true) != "" {
		t.Error("a fully grown wheat was given a refusal reason")
	}

	immature := farming.HarvestRefusal(farming.CropWheat, 3, true)
	if immature == "" {
		t.Error("stage-3 wheat got no refusal reason")
	}
	unknown := farming.HarvestRefusal(farming.CropWheat, 7, false)
	if unknown == "" {
		t.Fatal("an unreadable stage got no refusal reason")
	}
	if unknown == immature {
		t.Errorf("\"unreadable stage\" and \"not mature\" share the reason %q; they are different failures", unknown)
	}
}

// --- Replant ------------------------------------------------------------

// TestPlanReplantNeedsTheCropsOwnGround is 5.2's "replanting is only valid on
// the crop's own farmland". Planting wheat on sand does not grow wheat.
func TestPlanReplantNeedsTheCropsOwnGround(t *testing.T) {
	t.Parallel()

	onFarmland := farming.PlanReplant(farming.CropWheat, "minecraft:farmland", true, true)
	if !onFarmland.Do {
		t.Fatalf("wheat on farmland with seeds in hand was not replanted: %s", onFarmland.Reason)
	}
	if onFarmland.Seed != "wheat_seeds" {
		t.Errorf("Seed = %q, want wheat_seeds", onFarmland.Seed)
	}
	if onFarmland.Ground != "farmland" {
		t.Errorf("Ground = %q, want farmland", onFarmland.Ground)
	}

	onSand := farming.PlanReplant(farming.CropWheat, "minecraft:sand", true, true)
	if onSand.Do {
		t.Error("wheat was replanted on sand; the seed would not take")
	}
	if onSand.Reason == "" {
		t.Error("a refused replant gave no reason")
	}
}

// TestPlanReplantRefusesAnUnreadableGround. A ground block that was not in the
// world cache is not sand and is not farmland; it is a cell nobody has looked
// at, and no seed goes down into it on a guess.
func TestPlanReplantRefusesAnUnreadableGround(t *testing.T) {
	t.Parallel()

	got := farming.PlanReplant(farming.CropWheat, "", false, true)
	if got.Do {
		t.Error("a seed was planned for a cell the bot could not read")
	}
	if got.Reason == "" {
		t.Error("an unreadable ground gave no refusal reason")
	}
}

// TestPlanReplantNeedsSeedInHand. Planning a plant the bot cannot hold is a
// plan that cannot be executed, and reporting it as a replant would be a lie
// about the inventory.
func TestPlanReplantNeedsSeedInHand(t *testing.T) {
	t.Parallel()

	got := farming.PlanReplant(farming.CropWheat, "minecraft:farmland", true, false)
	if got.Do {
		t.Error("a replant was planned with no seed in the inventory")
	}
	if got.Reason == "" {
		t.Error("a seedless replant gave no refusal reason")
	}
}

// TestPlanReplantCoversTheExtendedCrops is 5.3: each extended crop has to have
// somewhere it can honestly be put back.
func TestPlanReplantCoversTheExtendedCrops(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		crop    farming.Crop
		ground  string
		wantDo  bool
		wantSee string
	}{
		{"nether wart on soul sand", farming.CropNetherWart, "minecraft:soul_sand", true, "nether_wart"},
		{"nether wart on nylium", farming.CropNetherWart, "minecraft:crimson_nylium", true, "nether_wart"},
		{"nether wart not on soul soil", farming.CropNetherWart, "minecraft:stone", false, ""},
		{"cocoa on a jungle log", farming.CropCocoa, "minecraft:jungle_log", true, "cocoa_beans"},
		{"cocoa not on a log", farming.CropCocoa, "minecraft:farmland", false, ""},
		{"sweet berry on farmland", farming.CropSweetBerry, "minecraft:farmland", true, "sweet_berry_bush"},
		{"sweet berry not on farmland", farming.CropSweetBerry, "minecraft:dirt", false, ""},
		{"bamboo on dirt", farming.CropBamboo, "minecraft:dirt", true, "bamboo"},
		{"bamboo on sand", farming.CropBamboo, "minecraft:sand", true, "bamboo"},
		{"bamboo in mid-air", farming.CropBamboo, "minecraft:air", false, ""},
		{"kelp on a sea floor block", farming.CropKelp, "minecraft:sand", true, "kelp"},
		{"kelp on farmland", farming.CropKelp, "minecraft:farmland", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := farming.PlanReplant(tc.crop, tc.ground, true, true)
			if got.Do != tc.wantDo {
				t.Fatalf("PlanReplant(%q, %q) Do = %t, want %t (%s)", tc.crop, tc.ground, got.Do, tc.wantDo, got.Reason)
			}
			if tc.wantDo && got.Seed != tc.wantSee {
				t.Errorf("Seed = %q, want %q", got.Seed, tc.wantSee)
			}
		})
	}
}

// TestPlanReplantRefusesStems. A stem is never broken, so there is never a
// harvested stem cell to replant. If a plan can name one, the stem guard above
// is being bypassed.
func TestPlanReplantRefusesStems(t *testing.T) {
	t.Parallel()

	for _, crop := range []farming.Crop{farming.CropPumpkinStem, farming.CropMelonStem} {
		got := farming.PlanReplant(crop, "minecraft:farmland", true, true)
		if got.Do {
			t.Errorf("PlanReplant(%q) planned a plant; a stem is never harvested so there is nothing to replant", crop)
		}
	}
}

// TestPlanReplantIsFalseForAnUnknownCrop.
func TestPlanReplantIsFalseForAnUnknownCrop(t *testing.T) {
	t.Parallel()

	if farming.PlanReplant(farming.CropUnknown, "minecraft:farmland", true, true).Do {
		t.Error("an unknown crop was replanted")
	}
}

// --- Bone meal ----------------------------------------------------------

// TestPlanBoneMealAcceleratesImmatureCrops is 5.2. Bone meal on a stage-0
// wheat is the whole point; on a stage-7 wheat it is a wasted item.
func TestPlanBoneMealAcceleratesImmatureCrops(t *testing.T) {
	t.Parallel()

	for _, crop := range []farming.Crop{
		farming.CropWheat, farming.CropCarrot, farming.CropPotato, farming.CropBeetroot,
	} {
		got := farming.PlanBoneMeal(crop, 0, true, true)
		if !got.Do {
			t.Errorf("PlanBoneMeal(%q, 0) = no; bone meal on a fresh planting is the point", crop)
		}
		if got.Reason != "" {
			t.Errorf("PlanBoneMeal(%q, 0) gave a refusal alongside Do: %q", crop, got.Reason)
		}

		ripe := farming.PlanBoneMeal(crop, 7, true, true)
		if ripe.Do {
			t.Errorf("PlanBoneMeal(%q, 7) = yes; the crop is already mature and the meal is wasted", crop)
		}
		if ripe.Reason == "" {
			t.Errorf("PlanBoneMeal(%q, 7) gave no reason", crop)
		}
	}
}

// TestPlanBoneMealRefusesWithoutBoneMealInHand.
func TestPlanBoneMealRefusesWithoutBoneMealInHand(t *testing.T) {
	t.Parallel()

	got := farming.PlanBoneMeal(farming.CropWheat, 0, true, false)
	if got.Do {
		t.Error("bone meal was planned with no bone meal in the inventory")
	}
	if got.Reason == "" {
		t.Error("a bone-meal plan with no meal gave no reason")
	}
}

// TestPlanBoneMealRefusesAnUnreadableStage. Spreading bone meal on a crop whose
// age is unknown is a small gamble, and the answer is not worth taking: the
// meal either does nothing or jumps a nearly-ripe crop, and either way the bot
// has spent an item on a guess.
func TestPlanBoneMealRefusesAnUnreadableStage(t *testing.T) {
	t.Parallel()

	got := farming.PlanBoneMeal(farming.CropWheat, 0, false, true)
	if got.Do {
		t.Error("bone meal was planned for a crop whose stage could not be read")
	}
	if got.Reason == "" {
		t.Error("an unreadable stage gave no bone-meal reason")
	}
}

// TestPlanBoneMealIsFalseForCropsThatIgnoreIt. Cocoa and nether wart do not
// respond to bone meal on Bedrock; spending one on them wastes the item and
// reports a growth that never happened.
func TestPlanBoneMealIsFalseForCropsThatIgnoreIt(t *testing.T) {
	t.Parallel()

	for _, crop := range []farming.Crop{farming.CropCocoa, farming.CropNetherWart, farming.CropKelp} {
		got := farming.PlanBoneMeal(crop, 0, true, true)
		if got.Do {
			t.Errorf("PlanBoneMeal(%q, 0) = yes; this crop does not respond to bone meal", crop)
		}
		if got.Reason == "" {
			t.Errorf("PlanBoneMeal(%q, 0) gave no reason", crop)
		}
	}
}

// TestPlanBoneMealIsFalseForCropsWithNoStage. Bamboo and kelp are grown by
// breaking; there is no stage to push and nothing to accelerate.
func TestPlanBoneMealIsFalseForCropsWithNoStage(t *testing.T) {
	t.Parallel()

	for _, crop := range []farming.Crop{farming.CropBamboo, farming.CropKelp, farming.CropSugarCane, farming.CropCactus} {
		if farming.PlanBoneMeal(crop, 0, false, true).Do {
			t.Errorf("PlanBoneMeal(%q) = yes; this crop has no growth stage to accelerate", crop)
		}
	}
}
