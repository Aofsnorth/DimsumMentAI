package station_test

import (
	"testing"

	"bedrock-ai/internal/bot/inventory/station"
)

// TestBrewingStandSlotLayout pins the five-slot brewing stand window. Writing
// the nether wart into a bottle slot does nothing at all, so the layout is a
// named contract rather than a number typed at the call site.
func TestBrewingStandSlotLayout(t *testing.T) {
	t.Parallel()

	if got := station.BrewBottleSlot0; got != 0 {
		t.Errorf("BrewBottleSlot0 = %d, want 0", got)
	}
	if got := station.BrewBottleSlotCount; got != 3 {
		t.Errorf("BrewBottleSlotCount = %d, want 3", got)
	}
	if got := station.BrewIngredientSlot; got != 3 {
		t.Errorf("BrewIngredientSlot = %d, want 3", got)
	}
	if got := station.BrewFuelSlot; got != 4 {
		t.Errorf("BrewFuelSlot = %d, want 4", got)
	}
	if got := station.BrewSlotCount; got != 5 {
		t.Errorf("BrewSlotCount = %d, want 5", got)
	}
}

// TestBrewingStandSlotsAreContiguous is the invariant the bottle fan-out relies
// on: the three bottle slots are 0,1,2 and the ingredient sits immediately
// after them.
func TestBrewingStandSlotsAreContiguous(t *testing.T) {
	t.Parallel()

	if station.BrewIngredientSlot != station.BrewBottleSlot0+station.BrewBottleSlotCount {
		t.Errorf("ingredient slot %d does not follow the %d bottle slots",
			station.BrewIngredientSlot, station.BrewBottleSlotCount)
	}
	if station.BrewFuelSlot != station.BrewIngredientSlot+1 {
		t.Errorf("fuel slot %d does not follow the ingredient slot", station.BrewFuelSlot)
	}
	if station.BrewSlotCount != station.BrewFuelSlot+1 {
		t.Errorf("BrewSlotCount = %d, want %d for a five-slot window",
			station.BrewSlotCount, station.BrewFuelSlot+1)
	}
}

// TestBrewBottleSlotRejectsOutOfRange is the guard that keeps a caller from
// addressing a slot that is not a bottle slot — the ingredient and fuel slots
// must never be handed out as bottles.
func TestBrewBottleSlotRejectsOutOfRange(t *testing.T) {
	t.Parallel()

	for _, index := range []int{0, 1, 2} {
		got, ok := station.BrewBottleSlot(index)
		if !ok {
			t.Fatalf("BrewBottleSlot(%d) rejected a valid bottle index", index)
		}
		if want := station.BrewBottleSlot0 + uint32(index); got != want {
			t.Errorf("BrewBottleSlot(%d) = %d, want %d", index, got, want)
		}
	}
	for _, index := range []int{-1, 3, 4, 99} {
		if _, ok := station.BrewBottleSlot(index); ok {
			t.Errorf("BrewBottleSlot(%d) accepted an index that is not a bottle slot", index)
		}
	}
}

// TestAnvilSlotLayout pins input 1, input 2, output.
func TestAnvilSlotLayout(t *testing.T) {
	t.Parallel()

	if got := station.AnvilInputSlot; got != 0 {
		t.Errorf("AnvilInputSlot = %d, want 0", got)
	}
	if got := station.AnvilMaterialSlot; got != 1 {
		t.Errorf("AnvilMaterialSlot = %d, want 1", got)
	}
	if got := station.AnvilOutputSlot; got != 2 {
		t.Errorf("AnvilOutputSlot = %d, want 2", got)
	}
	if got := station.AnvilSlotCount; got != 3 {
		t.Errorf("AnvilSlotCount = %d, want 3", got)
	}
}

// TestGrindstoneSlotLayout pins input, output.
func TestGrindstoneSlotLayout(t *testing.T) {
	t.Parallel()

	if got := station.GrindstoneInputSlot; got != 0 {
		t.Errorf("GrindstoneInputSlot = %d, want 0", got)
	}
	if got := station.GrindstoneOutputSlot; got != 1 {
		t.Errorf("GrindstoneOutputSlot = %d, want 1", got)
	}
	if got := station.GrindstoneSlotCount; got != 2 {
		t.Errorf("GrindstoneSlotCount = %d, want 2", got)
	}
}

// TestEnchantingTableSlotLayout. The enchanting table does not open a normal
// window, but the two special containers it does use are still addressed by
// slot, and putting the tool anywhere but slot 0 silently enchants nothing.
func TestEnchantingTableSlotLayout(t *testing.T) {
	t.Parallel()

	if got := station.EnchantInputSlot; got != 0 {
		t.Errorf("EnchantInputSlot = %d, want 0", got)
	}
	if got := station.EnchantMaterialSlot; got != 1 {
		t.Errorf("EnchantMaterialSlot = %d, want 1", got)
	}
}

// TestStationBlocksAreMatchedByName. This is the whole reason the search does
// not ask "is this block solid": a wall, a chest, and a barrel are all solid,
// and none of them brew, enchant, repair, or grind.
func TestStationBlocksAreMatchedByName(t *testing.T) {
	t.Parallel()

	brewing := []string{"brewing_stand", "minecraft:brewing_stand", "BREWING_STAND", " brewing_stand "}
	for _, name := range brewing {
		if !station.IsBrewingStandBlock(name) {
			t.Errorf("IsBrewingStandBlock(%q) = false, want true", name)
		}
	}
	notBrewing := []string{"furnace", "cauldron", "brewing", "minecraft:barrel", "", "wood"}
	for _, name := range notBrewing {
		if station.IsBrewingStandBlock(name) {
			t.Errorf("IsBrewingStandBlock(%q) = true, want false", name)
		}
	}

	for _, name := range []string{"enchanting_table", "minecraft:enchanting_table"} {
		if !station.IsEnchantingTableBlock(name) {
			t.Errorf("IsEnchantingTableBlock(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"enchantment_table", "table", ""} {
		if station.IsEnchantingTableBlock(name) {
			t.Errorf("IsEnchantingTableBlock(%q) = true, want false", name)
		}
	}

	// Every damage level of an anvil is still an anvil; a bot that only knows
	// "anvil" will skip the half-repaired ones sitting on the ground.
	for _, name := range []string{"anvil", "chipped_anvil", "damaged_anvil", "minecraft:chipped_anvil"} {
		if !station.IsAnvilBlock(name) {
			t.Errorf("IsAnvilBlock(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"enchanting_table", "grindstone", "anvil_block", ""} {
		if station.IsAnvilBlock(name) {
			t.Errorf("IsAnvilBlock(%q) = true, want false", name)
		}
	}

	for _, name := range []string{"grindstone", "minecraft:grindstone"} {
		if !station.IsGrindstoneBlock(name) {
			t.Errorf("IsGrindstoneBlock(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"stone", "grind_stone", ""} {
		if station.IsGrindstoneBlock(name) {
			t.Errorf("IsGrindstoneBlock(%q) = true, want false", name)
		}
	}
}
