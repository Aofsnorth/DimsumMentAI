package farming

import (
	"strings"

	"bedrock-ai/internal/event"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// BlockStateSource exposes a block's full state, not just its name.
//
// The shape is deliberately identical to bucket.BlockStateSource: one method on
// *bot.Bot — GetBlockState(x, y, z) (string, map[string]any, bool), backed by
// chunk.RuntimeIDToState in the world cache — satisfies both packages, so the
// wiring is one method and not two.
//
// *bot.Bot does not have it yet. GetBlockName reads the name out of the world
// cache, and world.WorldCache.BlockName deliberately throws the property map
// away: it calls chunk.RuntimeIDToState and keeps only the name, because the
// tree scanner asks for a block name over a hundred thousand times a gather and
// the allocation is the expensive part. So the age of a crop is present in the
// process and simply not exposed.
//
// Until that method lands, NewFarmer leaves this nil, every staged crop reads
// as an unknown stage, and HarvestCrops harvests nothing. That is the intended
// behaviour, not a defect: an unreadable age must not be guessed into a mature
// one. See the wiring note on SetBlockStateSource.
type BlockStateSource interface {
	GetBlockState(x, y, z int32) (name string, props map[string]any, ok bool)
}

// --- Inventory ----------------------------------------------------------

// Inventory is the bot's inventory flattened to a total count per item name.
//
// Totals rather than slots, for the reason husbandry.Inventory gives: a wheat
// harvest can land in any free slot and merge into an existing stack, so a
// slot-by-slot diff reports nothing when the thing actually happened.
type Inventory map[string]int

// NewInventory flattens a raw slot map plus its network-ID-to-name table.
func NewInventory(slots map[uint32]protocol.ItemStack, names map[int32]string) Inventory {
	inv := Inventory{}
	for _, s := range slots {
		if s.Count == 0 {
			continue
		}
		name := Normalise(names[s.NetworkID])
		if name == "" {
			continue
		}
		inv[name] += int(s.Count)
	}
	return inv
}

// CountOf returns the total number of one item.
func (inv Inventory) CountOf(name string) int { return inv[Normalise(name)] }

// Gained returns how many items matching predicate appeared between two
// readings.
func (inv Inventory) Gained(before Inventory, predicate func(name string) bool) int {
	gained := 0
	for name, n := range inv {
		if !predicate(name) {
			continue
		}
		if d := n - before[name]; d > 0 {
			gained += d
		}
	}
	return gained
}

// hasItem reports whether any of names is in the inventory.
func (inv Inventory) hasItem(names ...string) bool {
	for _, want := range names {
		if inv.CountOf(want) > 0 {
			return true
		}
	}
	return false
}

// --- Confirmation -------------------------------------------------------

// report sends a success status line, and says nothing when there is nothing
// to report. A zero count with Success set is how a caller learns a count of
// nothing happened, which is a different fact from a failure.
func (f *Farmer) report(action, item string, count int) {
	if count == 0 {
		return
	}
	f.bot.ReportActionStatus("", event.ActionStatus{
		Action: action, Item: item, Count: count, Success: true,
	})
}

// CellCleared reports whether a cell no longer holds the crop it was broken
// for.
//
// It is a change, not a blank: after a wheat harvest the cell holds farmland,
// after a kelp harvest it holds water, after a nether wart harvest it holds
// soul sand. Only "something that is not this crop" counts, and a cell that
// cannot be read does not — an unreadable cell is not an empty one.
func CellCleared(crop Crop, name string, known bool) bool {
	if !known {
		return false
	}
	return CropOf(name) != crop
}

// StageAdvanced reports whether a crop's stage moved forward between two
// readings.
//
// Both readings must be real. A stage that was unknown and is now known has not
// been shown to advance, and treating the first readable value as progress is
// how a bot ends up spreading bone meal on a mature field forever.
func StageAdvanced(before, after int, beforeKnown, afterKnown bool) bool {
	if !beforeKnown || !afterKnown {
		return false
	}
	return after > before
}

// HarvestResult is what one harvest attempt actually achieved.
//
// The flags are kept apart because they fail apart, and each failure means
// something different. A cell that was never readable is a bot that could not
// see the field; a cell that is readable and still full is a server that
// refused the break. Collapsing them into one bool loses the second, and the
// second is the one worth a log line.
type HarvestResult struct {
	// Crop is what the attempt was about.
	Crop Crop
	// CellReadable is false when the target cell was never in the world cache.
	CellReadable bool
	// CellCleared is true when the crop is gone from the cell.
	CellCleared bool
	// SupportIntact is true when a support-bearing crop's support survived the
	// break. Always true for a crop that does not grow from a support.
	SupportIntact bool
	// DropsGained is how many of the crop's produce appeared in the inventory.
	DropsGained int
	// Replanted is true when a seed went down and the new crop was observed in
	// the cell. Planning a plant is not replanting.
	Replanted bool
	// BoneMealApplied is true when a meal was used and the stage was then seen
	// to advance. A meal that was spent is not an applied one.
	BoneMealApplied bool
	// ReplantReason explains a skipped or refused replant.
	ReplantReason string
	// BoneMealReason explains a skipped or refused meal.
	BoneMealReason string
}

// Confirmed reports whether the harvest is honestly a success: the crop is gone
// from a cell that could be read.
//
// Replant and bone meal are not part of it. A harvest that happened and a
// replant that did not are two different facts, and HarvestCrops counts the
// first while this field says why the second did not happen.
func (r HarvestResult) Confirmed() bool {
	return r.CellReadable && r.CellCleared
}

// Harvest is what one sweep of a field achieved: the tally the caller plans
// with, and the per-cell detail behind it.
//
// Count and len(Results) are not the same number, and the difference is the
// point. Results holds every cell the scan looked at, including the ones it
// refused to touch, while Count holds only the harvests the world confirmed —
// so a sweep that walked a field of unripe wheat and correctly took nothing
// returns a full Results and a zero Count rather than looking like a no-op.
type Harvest struct {
	// Count is how many harvests the world confirmed.
	Count int
	// Results is one entry per cell the scan considered, in scan order.
	Results []HarvestResult
}

// Confirmed returns the per-cell results that the world confirmed, which is the
// subset a caller can report as work actually done.
func (h Harvest) Confirmed() []HarvestResult {
	out := make([]HarvestResult, 0, len(h.Results))
	for _, r := range h.Results {
		if r.Confirmed() {
			out = append(out, r)
		}
	}
	return out
}

// Planting is what one sowing pass achieved: the tally plus the per-crop
// detail, with the refusals kept rather than dropped.
type Planting struct {
	// Count is how many seeds the world confirmed in the ground.
	Count int
	// Plants is one entry per crop the pass considered.
	Plants []Plant
}

// Confirmed returns only the plantings that were observed in the ground.
func (p Planting) Confirmed() []Plant {
	out := make([]Plant, 0, len(p.Plants))
	for _, pl := range p.Plants {
		if pl.Planted {
			out = append(out, pl)
		}
	}
	return out
}

// waterNames are the blocks that count as "this cell is in water" for kelp.
var waterNames = map[string]bool{
	"water":         true,
	"flowing_water": true,
}

// IsWater reports whether a block name is water.
func IsWater(blockName string) bool {
	return waterNames[Normalise(blockName)]
}

// supportNames are the blocks a cocoa pod may hang from.
var supportNames = map[string]bool{
	"jungle_log":          true,
	"stripped_jungle_log": true,
}

// IsCocoaSupport reports whether a block can hold a cocoa pod.
func IsCocoaSupport(blockName string) bool {
	return supportNames[Normalise(blockName)]
}

// produceNames maps a crop to the item names harvesting it puts in the
// inventory. It is the produce list, not the seed list: a replant needs seeds,
// a harvest confirmation needs wheat.
var produceNames = map[Crop][]string{
	CropWheat:       {"wheat"},
	CropCarrot:      {"carrot", "carrots"},
	CropPotato:      {"potato", "potatoes"},
	CropBeetroot:    {"beetroots", "beetroot"},
	CropNetherWart:  {"nether_wart"},
	CropCocoa:       {"cocoa_beans"},
	CropBamboo:      {"bamboo"},
	CropKelp:        {"kelp"},
	CropSweetBerry:  {"sweet_berries", "sweet_berry"},
	CropSugarCane:   {"sugar_cane"},
	CropCactus:      {"cactus"},
	CropPumpkin:     {"pumpkin"},
	CropMelon:       {"melon", "melon_block"},
	CropPumpkinStem: {"pumpkin_seeds"},
	CropMelonStem:   {"melon_seeds"},
}

// produceMatches reports whether an item name is a crop's produce.
func produceMatches(crop Crop, name string) bool {
	for _, want := range produceNames[crop] {
		if name == want {
			return true
		}
	}
	return false
}

// boneMealName is the item that accelerates a crop's stage.
const boneMealName = "bone_meal"

// hasBoneMeal reports whether the inventory holds bone meal.
func hasBoneMeal(inv Inventory) bool { return inv.hasItem(boneMealName) }

// cropsInFilterOrder is every crop this package acts on, in a fixed order, so
// that an unscoped scan is deterministic rather than map-ordered.
var cropsInFilterOrder = []Crop{
	CropWheat, CropCarrot, CropPotato, CropBeetroot,
	CropNetherWart, CropCocoa, CropSweetBerry,
	CropBamboo, CropKelp, CropSugarCane, CropCactus,
	CropPumpkin, CropMelon,
}

// cropAliases maps a user-facing crop name onto the blocks it covers. The
// action layer normalises chat input through NormalizeCropType, which produces
// "pumpkin" and "melon" for the two fruit crops; those names have to reach both
// the fruit and the stem so a "harvest melon" can pick the melon off a stem it
// must not touch.
var cropAliases = map[string][]Crop{
	"wheat":            {CropWheat},
	"carrot":           {CropCarrot},
	"potato":           {CropPotato},
	"beetroot":         {CropBeetroot},
	"nether_wart":      {CropNetherWart},
	"wart":             {CropNetherWart},
	"cocoa":            {CropCocoa},
	"bamboo":           {CropBamboo},
	"kelp":             {CropKelp},
	"sweet_berry":      {CropSweetBerry},
	"berry":            {CropSweetBerry},
	"berries":          {CropSweetBerry},
	"sugar_cane":       {CropSugarCane},
	"sugarcane":        {CropSugarCane},
	"cactus":           {CropCactus},
	"pumpkin":          {CropPumpkin, CropPumpkinStem},
	"melon":            {CropMelon, CropMelonStem},
	"melon_block":      {CropMelon, CropMelonStem},
	"pumpkin_stem":     {CropPumpkinStem},
	"melon_stem":       {CropMelonStem},
	"sweet_berry_bush": {CropSweetBerry},
}

// CropsFor resolves a user-facing crop name into the set of crops it covers.
//
// An empty name means every crop this package grows. An unrecognised name
// yields nothing, and a caller that gets nothing harvests nothing — the old
// code fell through to "auto-detect any mature crop", which is how a typo in
// chat turned into a harvest of a field nobody asked about.
func CropsFor(name string) []Crop {
	key := Normalise(name)
	if key == "" {
		return append([]Crop(nil), cropsInFilterOrder...)
	}
	if crops, ok := cropAliases[key]; ok {
		return append([]Crop(nil), crops...)
	}
	return nil
}

// cropIn reports whether a crop is in a filter set.
func cropIn(set []Crop, crop Crop) bool {
	for _, c := range set {
		if c == crop {
			return true
		}
	}
	return false
}

// isFarmland reports whether a block is tilled soil a crop can be sown on.
func isFarmland(blockName string) bool { return strings.Contains(Normalise(blockName), "farmland") }
