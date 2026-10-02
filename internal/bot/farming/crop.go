package farming

import (
	"strconv"
	"strings"
)

// Crop is the kind of plant a cell holds. It is a closed vocabulary: a block
// that is not in it is not a crop, and a harvest routine that sees an unknown
// one has nothing to do.
//
// The old code matched with strings.Contains over a free-form string, which is
// how "pumpkin_stem" came to count as "pumpkin" and why the bot would break the
// one block whose destruction throws the crop away.
type Crop string

// The crops this package knows. CropUnknown is the zero value and is what an
// unrecognised block resolves to.
const (
	CropUnknown Crop = ""
	CropWheat   Crop = "wheat"
	CropCarrot  Crop = "carrot"
	CropPotato  Crop = "potato"
	// CropBeetroot is named after the crop; the block is "beetroots".
	CropBeetroot Crop = "beetroot"
	// CropNetherWart is grown on soul sand in the Nether and tops out at 3.
	CropNetherWart Crop = "nether_wart"
	// CropCocoa is a pod on a jungle log; it grows in three stages, not eight.
	CropCocoa Crop = "cocoa"
	// CropBamboo is harvested by breaking the block, not by reading an age.
	CropBamboo Crop = "bamboo"
	// CropKelp is harvested by breaking, and only while it is in water.
	CropKelp Crop = "kelp"
	// CropSweetBerry is a bush that tops out at stage 3.
	CropSweetBerry Crop = "sweet_berry_bush"
	// CropPumpkinStem and CropMelonStem must never be broken: the stem is the
	// crop, and the fruit above it grows out of it.
	CropPumpkinStem Crop = "pumpkin_stem"
	CropMelonStem   Crop = "melon_stem"
	// CropPumpkin and CropMelon are the fruit blocks. Breaking one is how a
	// melon is picked; the stem below is left alone.
	CropPumpkin Crop = "pumpkin"
	CropMelon   Crop = "melon_block"
	// CropSugarCane and CropCactus stack, and are broken rather than aged.
	CropSugarCane Crop = "sugar_cane"
	CropCactus    Crop = "cactus"
)

// blockToCrop is an exact-match table, not a substring search. Bedrock names
// these blocks "minecraft:wheat", "minecraft:carrots", "minecraft:beetroots",
// and so on; the irregular plural is why a substring rule over generic words
// was never going to hold.
var blockToCrop = map[string]Crop{
	"wheat":            CropWheat,
	"carrots":          CropCarrot,
	"potatoes":         CropPotato,
	"beetroots":        CropBeetroot,
	"nether_wart":      CropNetherWart,
	"cocoa":            CropCocoa,
	"bamboo":           CropBamboo,
	"bamboo_sapling":   CropBamboo,
	"kelp":             CropKelp,
	"kelp_plant":       CropKelp,
	"sweet_berry_bush": CropSweetBerry,
	"pumpkin_stem":     CropPumpkinStem,
	"melon_stem":       CropMelonStem,
	"pumpkin":          CropPumpkin,
	"melon_block":      CropMelon,
	// "reeds" is the Java-edition spelling of Bedrock's "sugar_cane"; it costs
	// one line to stop a cross-name from reading as an unknown block.
	"sugar_cane": CropSugarCane,
	"reeds":      CropSugarCane,
	"cactus":     CropCactus,
}

// String makes a Crop printable in a log line and in a test failure.
func (c Crop) String() string { return string(c) }

// CropOf resolves a block name to the crop it holds.
//
// An unrecognised block is CropUnknown, and the caller must treat that as "not
// a crop" rather than as "some crop I could not name".
func CropOf(blockName string) Crop {
	if blockName == "" {
		return CropUnknown
	}
	if c, ok := blockToCrop[Normalise(blockName)]; ok {
		return c
	}
	return CropUnknown
}

// IsStem reports whether a block is a stem — the block whose destruction
// destroys the crop. A stem is the single most dangerous thing this package
// can touch, so it has its own predicate and its own test.
func IsStem(blockName string) bool {
	switch CropOf(blockName) {
	case CropPumpkinStem, CropMelonStem:
		return true
	default:
		return false
	}
}

// Normalise lower-cases a name and strips the namespace, so that
// "minecraft:Carrots" and "carrots" are the same key. The lower-casing happens
// first: stripping a "minecraft:" prefix off "Minecraft:Wheat" would not match,
// and the remainder would be looked up under the wrong case.
func Normalise(name string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "minecraft:")
}

// --- The maturity table -------------------------------------------------

// stageCrops is the per-crop maturity table: the growth stage at which the crop
// is harvestable.
//
// These numbers are not interchangeable. The four farmland crops run 0..7,
// nether wart runs 0..3, cocoa 0..2, and a sweet berry bush 0..3. A single
// shared "fully grown" constant is wrong for three of them, and wrong in the
// expensive direction for two: at 7 the bot walks past a nether wart that is
// already pickable.
//
// This table states what the game does. It is not a policy — see IsHarvestSafe
// in plan.go, which is the decision boundary and which refuses to break a stem
// even though a stem has a maturity number here.
var stageCrops = map[Crop]int{
	CropWheat:       7,
	CropCarrot:      7,
	CropPotato:      7,
	CropBeetroot:    7,
	CropNetherWart:  3,
	CropCocoa:       2,
	CropSweetBerry:  3,
	CropPumpkinStem: 7,
	CropMelonStem:   7,
}

// MatureStage returns the growth stage at which a block is harvestable, and
// whether the block is a staged crop at all.
//
// ok is false for bamboo, kelp, sugar cane and cactus — crops that are taken by
// breaking the block and carry no age — and for anything that is not a crop.
// A caller that gets false has no maturity information, and the only honest
// thing to do with that is not to harvest on the strength of it.
func MatureStage(blockName string) (int, bool) {
	crop := CropOf(blockName)
	if crop == CropUnknown {
		return 0, false
	}
	stage, ok := stageCrops[crop]
	return stage, ok
}

// stagePropKeys are the block-state property names a growth stage can arrive
// under, most current first. Bedrock spells it "growth"; "age" is the older
// spelling and still turns up in palette dumps. Reading only one of them turns
// every other block into a silent "unknown", which means the bot never harvests
// and never says why.
var stagePropKeys = [...]string{"growth", "age"}

// stageBounds is the widest stage any crop in stageCrops reaches. A property
// value outside it is not a growth stage — it is a different property that
// happens to share a name, or a corrupt read — and guessing at it would let a
// garbage value compare as "mature".
const stageBounds = 7

// ReadStage pulls a growth stage out of a block-state property map.
//
// Every return of ok=false means the same thing: no reading. Not "stage zero",
// not "assume mature". The value inside the map is typed as any because it comes
// straight out of the palette decoder, so this accepts the numeric widths and
// the string form a decoder might hand back and refuses anything it cannot
// account for.
func ReadStage(props map[string]any) (int, bool) {
	if len(props) == 0 {
		return 0, false
	}
	for _, key := range stagePropKeys {
		raw, present := props[key]
		if !present {
			continue
		}
		if stage, ok := stageFrom(raw); ok {
			return stage, true
		}
		// The live key was present but unreadable. Do not fall through to an
		// alias: two spellings of one property disagreeing is a corrupt read,
		// and picking either of them would be a guess.
		return 0, false
	}
	return 0, false
}

// stageFrom converts one property value to a stage.
func stageFrom(raw any) (int, bool) {
	var stage int
	switch v := raw.(type) {
	case int32:
		stage = int(v)
	case int:
		stage = v
	case int8:
		stage = int(v)
	case int16:
		stage = int(v)
	case int64:
		stage = int(v)
	case uint8:
		stage = int(v)
	case uint16:
		stage = int(v)
	case uint32:
		stage = int(v)
	case uint64:
		if v > uint64(stageBounds) {
			return 0, false
		}
		stage = int(v)
	case float32:
		if float32(int(v)) != v {
			return 0, false
		}
		stage = int(v)
	case float64:
		if float64(int(v)) != v {
			return 0, false
		}
		stage = int(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		stage = parsed
	default:
		// bool, nil, structs, slices: not a stage.
		return 0, false
	}
	if stage < 0 || stage > stageBounds {
		return 0, false
	}
	return stage, true
}
