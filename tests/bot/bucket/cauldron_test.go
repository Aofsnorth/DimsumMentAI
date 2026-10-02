package bucket_test

import (
	"testing"

	"bedrock-ai/internal/bot/bucket"
)

// --- The cauldron's level ----------------------------------------------
//
// Bedrock does not give a cauldron a level in its block name. Every state of it
// is named "minecraft:cauldron"; the level and the liquid live in the state's
// properties, and the level is a bitmask:
//
//	fill_level 0  = empty
//	fill_level 2  = 1/3      (bit 1)
//	fill_level 4  = 2/3      (bit 2)
//	fill_level 8  = 3/3      (bit 3)
//	6, 12, 10    = combinations, resolved by the first bit that is set
//
// Getting this wrong is how a cauldron reads as permanently full.

func TestCauldronLevelFromTheFillMask(t *testing.T) {
	t.Parallel()

	cases := map[int]int{
		0: 0,
		2: 1,
		4: 2,
		6: 1,
		8: 3,
		// 10 and 12 are the "partly filled" masks: 10 is bottom+middle,
		// 12 is middle+top, and the lowest set bit is the one that names
		// the level. These are the seven values the Bedrock palette uses;
		// nothing else ever reaches this function.
		10: 1,
		12: 2,
	}
	for fill, want := range cases {
		if got := bucket.CauldronLevelFromFill(fill); got != want {
			t.Errorf("CauldronLevelFromFill(%d) = %d, want %d", fill, got, want)
		}
	}
}

func TestIsCauldronBlock(t *testing.T) {
	t.Parallel()

	for _, n := range []string{"minecraft:cauldron", "cauldron"} {
		if !bucket.IsCauldronBlock(n) {
			t.Errorf("%q was not recognised as a cauldron", n)
		}
	}
	for _, n := range []string{"", "minecraft:water", "minecraft:furnace", "cauldronx"} {
		if bucket.IsCauldronBlock(n) {
			t.Errorf("%q was mistaken for a cauldron", n)
		}
	}
}

func TestAFillRaisesTheLevel(t *testing.T) {
	t.Parallel()

	before := bucket.CauldronObservation{Name: "minecraft:cauldron", Fill: 1, HasFill: true}
	after := bucket.CauldronObservation{Name: "minecraft:cauldron", Fill: 2, HasFill: true, Liquid: "water"}

	change := bucket.CauldronLevelChange(before, after)
	if !change.Changed {
		t.Fatal("a cauldron going from 1/3 to 2/3 was not seen as changed")
	}
	if !change.LevelKnown {
		t.Error("the level was readable on both sides but reported as unknown")
	}
	if change.From != 1 || change.To != 2 {
		t.Errorf("CauldronLevelChange = %d -> %d, want 1 -> 2", change.From, change.To)
	}
	if change.Rose() != 1 {
		t.Errorf("Rose() = %d, want 1", change.Rose())
	}
}

func TestADrainLowersTheLevel(t *testing.T) {
	t.Parallel()

	before := bucket.CauldronObservation{Name: "minecraft:cauldron", Fill: 3, HasFill: true, Liquid: "lava"}
	after := bucket.CauldronObservation{Name: "minecraft:cauldron", Fill: 2, HasFill: true, Liquid: "lava"}

	change := bucket.CauldronLevelChange(before, after)
	if !change.Changed {
		t.Fatal("a cauldron going from full to 2/3 was not seen as changed")
	}
	if change.To != 2 {
		t.Errorf("CauldronLevelChange.To = %d, want 2", change.To)
	}
}

func TestAnUnchangedCauldronIsNotAChange(t *testing.T) {
	t.Parallel()

	// The acceptance criterion is "level changes observed". A cauldron that
	// stayed at 1/3 has not been filled, and reporting that it has is the
	// optimistic-success bug in a new place.
	same := bucket.CauldronObservation{Name: "minecraft:cauldron", Fill: 1, HasFill: true}
	if bucket.CauldronLevelChange(same, same).Changed {
		t.Error("an identical cauldron was reported as changed")
	}
}

func TestABlockStateChangeIsStillAnObservation(t *testing.T) {
	t.Parallel()

	// Without the fill_level property the level cannot be read, but the block
	// state can still be seen to differ. That is a real observation and it is
	// reported as one — as a change whose magnitude is unknown, which is a
	// different claim from "the level went up by one".
	before := bucket.CauldronObservation{Name: "minecraft:cauldron", NetworkID: 100, HasNetworkID: true}
	after := bucket.CauldronObservation{Name: "minecraft:cauldron", NetworkID: 101, HasNetworkID: true}

	change := bucket.CauldronLevelChange(before, after)
	if !change.Changed {
		t.Error("a cauldron whose block state changed was not seen as changed")
	}
	if change.LevelKnown {
		t.Error("a level was reported from a reading that had no fill property")
	}
	if change.Rose() != 0 {
		t.Errorf("Rose() = %d with no readable level, want 0 rather than a guess", change.Rose())
	}
}

func TestAnUnreadableCauldronIsNotAChange(t *testing.T) {
	t.Parallel()

	// Nothing is known on either side. That is not evidence of a change, and
	// it is not evidence against one either — it is nothing.
	var unknown bucket.CauldronObservation
	if bucket.CauldronLevelChange(unknown, unknown).Changed {
		t.Error("two unreadable cauldron readings were called a change")
	}
}

func TestReadCauldronReadsTheProperties(t *testing.T) {
	t.Parallel()

	obs := bucket.ReadCauldron("minecraft:cauldron", map[string]any{
		"fill_level":      int32(12),
		"cauldron_liquid": "lava",
	}, 77, true)

	if !obs.HasFill {
		t.Fatal("fill_level was present in the properties but not read")
	}
	if obs.Fill != 2 {
		t.Errorf("Fill = %d, want 2 for fill_level 12", obs.Fill)
	}
	if obs.Liquid != "lava" {
		t.Errorf("Liquid = %q, want lava", obs.Liquid)
	}
	if !obs.HasNetworkID || obs.NetworkID != 77 {
		t.Errorf("network ID not read: %+v", obs)
	}
}

func TestReadCauldronWithNoPropertiesIsPartial(t *testing.T) {
	t.Parallel()

	// This is the state of the world today: *bot.Bot exposes GetBlockName but
	// no block properties, so a cauldron arrives with a name and nothing else.
	obs := bucket.ReadCauldron("minecraft:cauldron", nil, 77, true)
	if obs.HasFill {
		t.Error("a fill level was invented from a reading that had none")
	}
	if obs.Name != "cauldron" {
		t.Errorf("Name = %q, want the normalised cauldron name", obs.Name)
	}
}
