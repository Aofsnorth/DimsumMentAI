package durability_test

import (
	"testing"

	"bedrock-ai/internal/bot/durability"
)

// --- Durability ---

// TestDurabilityIsSwappedBeforeItBreaks is the failure this prevents: mining a
// stone vein with a wooden pickaxe until it breaks mid-swing, and every block
// after that coming out as nothing while the gather reports success.
func TestDurabilityIsSwappedBeforeItBreaks(t *testing.T) {
	t.Parallel()

	d := durability.NewTracker()
	if d.ShouldReplace(0, "minecraft:wooden_pickaxe") {
		t.Error("a fresh tool was marked for replacement")
	}

	for i := 0; i < 40; i++ {
		d.Record(0)
	}
	if d.ShouldReplace(0, "minecraft:wooden_pickaxe") {
		t.Error("a wooden pickaxe at 40/59 uses was marked for replacement; 80% is the line")
	}

	for i := 0; i < 8; i++ {
		d.Record(0)
	}
	if !d.ShouldReplace(0, "minecraft:wooden_pickaxe") {
		t.Error("a wooden pickaxe at 48/59 uses was not marked for replacement; it breaks mid-vein")
	}
}

// TestTiersHaveTheirOwnBudget keeps the decision honest. An iron pickaxe (250
// uses) and a wooden one (59) do not break at the same moment, and the tracker
// must not pretend they do: a shared 132-use budget marked a diamond pickaxe at
// 105 uses and left a golden one to shatter at 32.
func TestTiersHaveTheirOwnBudget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		uses    int
		replace bool
	}{
		{"minecraft:wooden_pickaxe", 48, true},    // 59 uses: 80% = 47
		{"minecraft:golden_pickaxe", 26, true},    // 32 uses: 80% = 25
		{"minecraft:stone_pickaxe", 105, true},    // 131 uses: 80% = 104
		{"minecraft:iron_pickaxe", 110, false},    // 250 uses: 80% = 200
		{"minecraft:iron_pickaxe", 205, true},     //
		{"minecraft:diamond_pickaxe", 110, false}, // 1561 uses
	}
	for _, c := range cases {
		d := durability.NewTracker()
		for i := 0; i < c.uses; i++ {
			d.Record(0)
		}
		if got := d.ShouldReplace(0, c.name); got != c.replace {
			t.Errorf("%s at %d uses: ShouldReplace = %v, want %v", c.name, c.uses, got, c.replace)
		}
	}
}

// TestItemsWithoutDurabilityAreNeverReplaced is the false-positive guard. A bot
// that swaps a cobblestone block because it has been held a thousand times is a
// bot that never finishes anything.
func TestItemsWithoutDurabilityAreNeverReplaced(t *testing.T) {
	t.Parallel()

	d := durability.NewTracker()
	for i := 0; i < 10000; i++ {
		d.Record(0)
	}
	for _, name := range []string{"minecraft:stone", "minecraft:bread", "minecraft:oak_log", ""} {
		if d.ShouldReplace(0, name) {
			t.Errorf("%q was marked for replacement; it has no durability", name)
		}
		if got := d.Remaining(0, name); got != -1 {
			t.Errorf("Remaining(%q) = %d, want -1 for unknown", name, got)
		}
	}
}

// TestRemainingNeverGoesNegative. "Negative uses remaining" is a number that
// only confuses whoever reads it in a log.
func TestRemainingNeverGoesNegative(t *testing.T) {
	t.Parallel()

	d := durability.NewTracker()
	for i := 0; i < 500; i++ {
		d.Record(0)
	}
	if got := d.Remaining(0, "minecraft:wooden_sword"); got != 0 {
		t.Errorf("Remaining = %d after overusing a sword, want 0", got)
	}
}

// TestSlotsHaveSeparateLives is why the tracker is keyed by slot and not by
// name. Two wooden pickaxes have separate lives, and merging them would let a
// freshly replaced pickaxe inherit the old one's near-death count.
func TestSlotsHaveSeparateLives(t *testing.T) {
	t.Parallel()

	d := durability.NewTracker()
	for i := 0; i < 200; i++ {
		d.Record(0)
	}
	if d.ShouldReplace(1, "minecraft:wooden_pickaxe") {
		t.Error("slot 1 inherited slot 0's near-death count")
	}
	if d.Used(1) != 0 {
		t.Errorf("slot 1 has %d recorded uses, want 0", d.Used(1))
	}

	// Moving a different item into the slot clears the history.
	d.Forget(0)
	if d.Used(0) != 0 {
		t.Error("Forget did not clear the slot's history")
	}
}
