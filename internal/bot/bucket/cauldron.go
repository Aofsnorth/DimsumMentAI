package bucket

import "strings"

// fillLevelBits are the bit positions Bedrock uses in a cauldron's fill_level.
//
// The property is a bitmask, not a count, which is why a cauldron can hold
// "the middle third" and "the bottom third" as separate block states. The
// values below are the ones that appear in the palette: 0, 2, 4, 6, 8, 10, 12,
// 14. The order matters — bit 1 is tested before bit 2 before bit 3, because a
// mask can have more than one bit set and the lowest one wins.
var fillLevelBits = []struct {
	bit   int
	level int
}{
	{1, 1},
	{2, 2},
	{3, 3},
}

// CauldronLevelFromFill turns a cauldron's fill_level bitmask into 0-3.
//
// Getting this wrong is not a cosmetic bug: read 8 as 3 and every half-full
// cauldron looks full, and a bot that believes the cauldron is full stops
// filling it.
func CauldronLevelFromFill(fill int) int {
	for _, b := range fillLevelBits {
		if fill&(1<<b.bit) != 0 {
			return b.level
		}
	}
	return 0
}

// IsCauldronBlock reports whether a block name is a cauldron.
//
// Exact after normalisation, not a substring: "cauldron" must not match a
// modded block that merely starts with the same letters.
func IsCauldronBlock(name string) bool {
	return isCauldronName(normalise(name))
}

func isCauldronName(n string) bool { return n == "cauldron" }

// CauldronObservation is everything that could be read about a cauldron at one
// moment in time.
//
// The two halves are different in kind. The network ID is always available
// through *bot.Bot and changes whenever the block state changes, so it answers
// "did anything happen". The fill level lives in the block state's properties,
// which nothing in this repository currently exposes, so it answers "how much
// is in it" only when a BlockStateSource is wired.
type CauldronObservation struct {
	// Name is the block name. It is always "minecraft:cauldron" in Bedrock,
	// for every level and every liquid.
	Name string
	// NetworkID is the block's wire-format ID, which covers the properties.
	NetworkID uint32
	// HasNetworkID is false when the block is not in the world cache at all.
	HasNetworkID bool
	// Fill is the decoded 0-3 level.
	Fill int
	// HasFill is false when the fill_level property was not readable.
	HasFill bool
	// Liquid is "water", "lava", "powder_snow", or "".
	Liquid string
}

// ReadCauldron builds an observation from a block name and, when they are
// available, its state properties.
//
// A missing or unrecognised fill_level yields HasFill=false rather than a zero
// level. Reporting 0 for "not read" would make every cauldron look empty, and
// a bot that thinks the cauldron is empty will happily pour a full one over.
func ReadCauldron(name string, props map[string]any, networkID uint32, hasNetworkID bool) CauldronObservation {
	obs := CauldronObservation{
		Name:         normalise(name),
		NetworkID:    networkID,
		HasNetworkID: hasNetworkID,
	}
	if props == nil {
		return obs
	}
	if v, ok := props["fill_level"]; ok {
		if fill, ok := toInt(v); ok {
			obs.Fill = CauldronLevelFromFill(fill)
			obs.HasFill = true
		}
	}
	if v, ok := props["cauldron_liquid"]; ok {
		if s, ok := v.(string); ok {
			obs.Liquid = normalise(s)
		}
	}
	return obs
}

// toInt accepts the integer shapes an NBT decoder can hand back.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// CauldronChange is what a cauldron did between two readings.
type CauldronChange struct {
	// Changed means the cauldron's block state is demonstrably different from
	// before. This is a real observation even when the level is not readable.
	Changed bool
	// LevelKnown means both readings carried a fill_level, so From and To are
	// real numbers rather than an absence of information.
	LevelKnown bool
	// From and To are the levels, valid only when LevelKnown.
	From int
	To   int
	// FromLiquid and ToLiquid are the liquids, possibly empty.
	FromLiquid string
	ToLiquid   string
	// StateChanged names the field that proved the change, for the log.
	StateChanged string
}

// Rose returns how many levels the cauldron gained, or 0 when the level could
// not be read.
//
// It returns 0 rather than a guess for an unreadable level on purpose. A
// caller that needs a direction and gets one invented is exactly the failure
// this package is here to stop.
func (c CauldronChange) Rose() int {
	if !c.LevelKnown || c.To <= c.From {
		return 0
	}
	return c.To - c.From
}

// Fell returns how many levels the cauldron lost, or 0 when unknown.
func (c CauldronChange) Fell() int {
	if !c.LevelKnown || c.From <= c.To {
		return 0
	}
	return c.From - c.To
}

// CauldronLevelChange compares two readings of the same cauldron.
//
// The three sources of evidence are consulted in order of strength: a readable
// level first, then the network ID, then the name. Two readings with nothing at
// all in them are not a change — that is the difference between "the cauldron
// did not fill" and "I could not tell", and only the first is a real answer.
func CauldronLevelChange(before, after CauldronObservation) CauldronChange {
	change := CauldronChange{
		FromLiquid: before.Liquid,
		ToLiquid:   after.Liquid,
	}
	if !IsCauldronBlock(before.Name) || !IsCauldronBlock(after.Name) {
		return change
	}

	if before.HasFill && after.HasFill {
		change.From = before.Fill
		change.To = after.Fill
		change.LevelKnown = true
		if before.Fill != after.Fill {
			change.Changed = true
			change.StateChanged = "fill_level"
		}
	}

	if change.Changed {
		return change
	}
	if before.HasNetworkID && after.HasNetworkID && before.NetworkID != after.NetworkID {
		change.Changed = true
		change.StateChanged = "network_id"
		return change
	}
	if before.Name != after.Name {
		change.Changed = true
		change.StateChanged = "name"
	}
	return change
}

// blockBecameFluid reports whether a block name change turned a cell into the
// given fluid.
func blockBecameFluid(before, after string, kind BucketKind) bool {
	tokens := fluidTokens(kind)
	if len(tokens) == 0 {
		return false
	}
	b := normalise(before)
	a := normalise(after)
	if containsAny(a, tokens) {
		// Water poured into water is not an observed change, and neither is
		// any no-op the server might report.
		return b != a
	}
	return false
}

// fluidTokens are the block names a poured liquid shows up as.
func fluidTokens(kind BucketKind) []string {
	switch kind {
	case BucketWater:
		return []string{"water", "bubble_column"}
	case BucketLava:
		return []string{"lava"}
	case BucketPowderSnow:
		return []string{"powder_snow"}
	default:
		return nil
	}
}

func containsAny(name string, tokens []string) bool {
	for _, t := range tokens {
		if strings.Contains(name, t) {
			return true
		}
	}
	return false
}
