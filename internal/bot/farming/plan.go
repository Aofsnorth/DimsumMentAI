package farming

import "strconv"

// --- Per-crop harvest rules ---------------------------------------------

// CropRule is everything this package knows about how one crop is taken, kept
// in one place so that the harvest, replant and bone-meal decisions cannot
// disagree with each other about the same crop.
type CropRule struct {
	// Crop is the crop this rule describes.
	Crop Crop
	// Staged is true when the crop grows through a readable stage and must be
	// mature before it is taken. False means it is harvested by breaking the
	// block, whatever its state.
	Staged bool
	// MatureStage is the stage at which a Staged crop may be taken.
	MatureStage int
	// NeverBreak is true for the blocks whose destruction destroys the crop.
	// A stem is Staged and MatureStage is a real number, and it is still never
	// broken; this flag is why a maturity table is not a harvest policy.
	NeverBreak bool
	// NeedsWater is true for a crop that only survives in water. Kelp broken in
	// the open air is not a harvest, it is litter.
	NeedsWater bool
	// GrowsFromSupport is true for a crop attached to the side of another
	// block. The support is checked before and after the break, because losing
	// it takes the pod with it.
	GrowsFromSupport bool
	// Stackable is true for a crop that grows in a column, so only the top
	// block is taken and the rest is left standing.
	Stackable bool
	// GroundBelow is true when the crop sits on top of the ground it is
	// replanted into — the usual case, where the farmland is one block under
	// the wheat. Nether wart is the exception: the wart replaces the block it
	// is planted on, so the ground and the planting cell are the same one.
	GroundBelow bool
	// Ground is the set of block names a seed of this crop may be planted on,
	// normalised. A replant is planned only when the cell under the harvested
	// crop is one of these.
	Ground []string
	// Seeds are the inventory item names that plant this crop, most specific
	// first. Cocoa is planted from cocoa beans, nether wart from the wart item.
	Seeds []string
	// BoneMeal is true when bone meal advances this crop's stage.
	BoneMeal bool
}

// cropRules is the per-crop table the three decisions read.
//
// The Ground entries are the honest part: a replant is only planned when the
// ground is one this crop actually grows on. Wheat on sand, cocoa on farmland
// and kelp on farmland are all refused rather than attempted, because a
// "replanted" crop that never grows is worse than no replant — it looks like
// the cycle works.
var cropRules = map[Crop]CropRule{
	CropWheat: {
		Crop: CropWheat, Staged: true, MatureStage: 7, BoneMeal: true, GroundBelow: true,
		Ground: []string{"farmland"},
		Seeds:  []string{"wheat_seeds", "wheat_seed"},
	},
	CropCarrot: {
		Crop: CropCarrot, Staged: true, MatureStage: 7, BoneMeal: true, GroundBelow: true,
		Ground: []string{"farmland"},
		Seeds:  []string{"carrot", "carrots"},
	},
	CropPotato: {
		Crop: CropPotato, Staged: true, MatureStage: 7, BoneMeal: true, GroundBelow: true,
		Ground: []string{"farmland"},
		Seeds:  []string{"potato", "potatoes"},
	},
	CropBeetroot: {
		Crop: CropBeetroot, Staged: true, MatureStage: 7, BoneMeal: true, GroundBelow: true,
		Ground: []string{"farmland"},
		Seeds:  []string{"beetroot_seeds", "beetroot_seed", "beetroots"},
	},
	CropNetherWart: {
		Crop: CropNetherWart, Staged: true, MatureStage: 3, BoneMeal: false,
		// Nether wart replaces the block it sits on, so the harvest happens in
		// place and the ground for a replant is whatever the wart was on.
		Ground: []string{"soul_sand", "soul_soil", "warped_nylium", "crimson_nylium", "nether_wart_block"},
		Seeds:  []string{"nether_wart"},
	},
	CropCocoa: {
		Crop: CropCocoa, Staged: true, MatureStage: 2, GrowsFromSupport: true, BoneMeal: false, GroundBelow: true,
		Ground: []string{"jungle_log", "stripped_jungle_log"},
		Seeds:  []string{"cocoa_beans"},
	},
	CropSweetBerry: {
		Crop: CropSweetBerry, Staged: true, MatureStage: 3, BoneMeal: true, GroundBelow: true,
		Ground: []string{"farmland"},
		Seeds:  []string{"sweet_berry_bush", "sweet_berries"},
	},
	CropBamboo: {
		Crop: CropBamboo, Staged: false, Stackable: true,
		// Bamboo takes on dirt, grass, sand, gravel and podzol. Anything not in
		// this list is refused rather than planted and watched not to grow.
		Ground: []string{"dirt", "grass_block", "grass", "sand", "red_sand", "gravel", "podzol", "mycelium"},
		Seeds:  []string{"bamboo"},
	},
	CropKelp: {
		Crop: CropKelp, Staged: false, NeedsWater: true, Stackable: true,
		Ground: []string{"sand", "red_sand", "gravel", "dirt", "stone", "dirt_with_roots", "mud", "clay", "moss_block"},
		Seeds:  []string{"kelp"},
	},
	CropSugarCane: {
		Crop: CropSugarCane, Staged: false, Stackable: true, BoneMeal: true,
		Ground: []string{"sand", "red_sand", "dirt", "grass_block", "grass", "sugar_cane", "mud", "coarse_dirt"},
		Seeds:  []string{"sugar_cane"},
	},
	CropCactus: {
		Crop: CropCactus, Staged: false, Stackable: true, BoneMeal: true,
		Ground: []string{"sand", "red_sand", "cactus"},
		Seeds:  []string{"cactus"},
	},
	CropPumpkin: {
		Crop: CropPumpkin, Staged: false,
		// A ripe pumpkin is picked; its stem is one to three blocks below and is
		// never touched, so there is nothing to replant. The stem is still
		// growing fruit.
	},
	CropMelon: {
		Crop: CropMelon, Staged: false,
	},
	CropPumpkinStem: {
		Crop: CropPumpkinStem, Staged: true, MatureStage: 7, NeverBreak: true, BoneMeal: true,
	},
	CropMelonStem: {
		Crop: CropMelonStem, Staged: true, MatureStage: 7, NeverBreak: true, BoneMeal: true,
	},
}

// RuleOf returns the harvest rule for a crop, and whether the crop is one this
// package grows. The second return is false for CropUnknown, which is the only
// value that must never be acted on.
func RuleOf(crop Crop) (CropRule, bool) {
	rule, ok := cropRules[crop]
	return rule, ok
}

// --- Harvest safety -----------------------------------------------------

// IsHarvestSafe reports whether a cell holding this crop may be broken.
//
// stageKnown is the honest answer to "did we read this crop's age". Passing
// false with a stage of 7 is exactly the situation this package is built
// around: the age is not available, and an unavailable age is not a mature one.
func IsHarvestSafe(crop Crop, stage int, stageKnown bool) bool {
	return HarvestRefusal(crop, stage, stageKnown) == ""
}

// HarvestRefusal returns why a cell may not be harvested, or the empty string
// when it may.
//
// The reason exists because a refusal is otherwise invisible. A bot that
// silently skips every crop it cannot age is indistinguishable from a bot that
// is working, and the log line is the only place the difference shows up.
func HarvestRefusal(crop Crop, stage int, stageKnown bool) string {
	if crop == CropUnknown {
		return "not a crop this package grows"
	}
	rule, ok := RuleOf(crop)
	if !ok {
		return "no harvest rule for " + string(crop)
	}
	// Checked before maturity, and not by accident: a fully grown pumpkin stem
	// has a real stage and a real maturity, and breaking it is the one action
	// in this package that throws the crop away.
	if rule.NeverBreak {
		return string(crop) + " is the crop itself; breaking it destroys the plant"
	}
	if !rule.Staged {
		// Grown by breaking: bamboo, kelp, sugar cane, cactus, the melon and
		// pumpkin fruits. There is no age to read, so there is nothing to refuse.
		return ""
	}
	if !stageKnown {
		return string(crop) + " growth stage unreadable; refusing to harvest on a guess"
	}
	if stage < rule.MatureStage {
		return string(crop) + " is at stage " + strconv.Itoa(stage) + ", mature is " + strconv.Itoa(rule.MatureStage)
	}
	return ""
}

// --- Replant ------------------------------------------------------------

// ReplantPlan is the decision to put a crop back into the cell it came out of.
type ReplantPlan struct {
	// Do is true only when every condition for a replant was met.
	Do bool
	// Crop is what will be replanted.
	Crop Crop
	// Seed is the inventory item to plant, from the crop's own seed list.
	Seed string
	// Ground is the normalised name of the block the seed will be used on.
	Ground string
	// Reason explains a refusal, and is empty when Do is true.
	Reason string
}

// PlanReplant decides whether a harvested cell should be planted again.
//
// groundName is the block under the harvested crop and groundKnown says whether
// that cell could be read at all. A replant is planned only when the ground is
// the crop's own, because a seed on the wrong block is a spent item and a
// field that will never come back.
func PlanReplant(crop Crop, groundName string, groundKnown bool, hasSeed bool) ReplantPlan {
	plan := ReplantPlan{Crop: crop}

	if crop == CropUnknown {
		plan.Reason = "not a crop this package grows"
		return plan
	}
	rule, ok := RuleOf(crop)
	if !ok {
		plan.Reason = "no replant rule for " + string(crop)
		return plan
	}
	// A stem is never broken, so its cell was never harvested. A plan that can
	// name one is being built from a path that bypassed the stem guard.
	if rule.NeverBreak {
		plan.Reason = string(crop) + " is never harvested, so there is nothing to replant"
		return plan
	}
	if len(rule.Seeds) == 0 {
		// A ripe pumpkin or melon: the stem below is untouched and still
		// growing. Replanting would be planting a second one.
		plan.Reason = string(crop) + " is picked from a stem that is still growing; no replant"
		return plan
	}
	if !groundKnown {
		plan.Reason = "ground block unreadable; not planting on a guess"
		return plan
	}

	ground := Normalise(groundName)
	plan.Ground = ground
	if !containsName(rule.Ground, ground) {
		plan.Reason = string(crop) + " does not grow on " + ground
		return plan
	}
	if !hasSeed {
		plan.Reason = "no " + string(crop) + " seed in inventory"
		return plan
	}

	plan.Do = true
	plan.Seed = rule.Seeds[0]
	plan.Reason = ""
	return plan
}

// --- Bone meal ----------------------------------------------------------

// BoneMealPlan is the decision to apply one bone meal to a crop.
type BoneMealPlan struct {
	// Do is true only when the meal is expected to do something.
	Do bool
	// Crop is what the meal would be applied to.
	Crop Crop
	// Stage is the stage the crop was last read at, for the log line.
	Stage int
	// Reason explains a refusal, and is empty when Do is true.
	Reason string
}

// PlanBoneMeal decides whether one bone meal should be applied to a crop.
//
// The meal is only spent on a crop that responds to it and is not already
// mature. Bone meal on cocoa or nether wart does nothing on Bedrock, so
// spending one there wastes the item and, worse, invites a report of growth
// that never happened.
func PlanBoneMeal(crop Crop, stage int, stageKnown bool, hasBoneMeal bool) BoneMealPlan {
	plan := BoneMealPlan{Crop: crop, Stage: stage}

	if crop == CropUnknown {
		plan.Reason = "not a crop this package grows"
		return plan
	}
	rule, ok := RuleOf(crop)
	if !ok {
		plan.Reason = "no bone meal rule for " + string(crop)
		return plan
	}
	if !rule.BoneMeal {
		plan.Reason = string(crop) + " does not respond to bone meal"
		return plan
	}
	if !rule.Staged {
		plan.Reason = string(crop) + " has no growth stage to accelerate"
		return plan
	}
	if !stageKnown {
		plan.Reason = string(crop) + " growth stage unreadable; not spending a meal on a guess"
		return plan
	}
	if stage >= rule.MatureStage {
		plan.Reason = string(crop) + " is already mature at stage " + strconv.Itoa(stage)
		return plan
	}
	if !hasBoneMeal {
		plan.Reason = "no bone meal in inventory"
		return plan
	}

	plan.Do = true
	plan.Reason = ""
	return plan
}

// --- Shared helpers -----------------------------------------------------

// containsName reports whether want is in list, both already normalised.
func containsName(list []string, want string) bool {
	for _, name := range list {
		if name == want {
			return true
		}
	}
	return false
}
