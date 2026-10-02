package station_test

import (
	"context"
	"testing"
	"time"

	"bedrock-ai/internal/bot/inventory/station"
)

// TestBrewPlanHealsGlassBottle. The acceptance criterion is a healing potion
// from nether wart plus a glass bottle, and in vanilla that is two cycles, not
// one: the first nether wart makes an awkward potion, the second upgrades it to
// healing. A planner that returns a single step here reports a success that
// never happens.
func TestBrewPlanHealsGlassBottle(t *testing.T) {
	t.Parallel()

	plan, ok := station.BrewPlan("glass_bottle", "nether_wart", "healing")
	if !ok {
		t.Fatal("BrewPlan(glass_bottle, healing) is not a known recipe")
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("plan has %d steps, want 2 (awkward first, then healing)", len(plan.Steps))
	}
	if plan.Steps[0].Result != "awkward" {
		t.Errorf("step 0 result = %q, want %q", plan.Steps[0].Result, "awkward")
	}
	if plan.Steps[1].Result != "healing" {
		t.Errorf("step 1 result = %q, want %q", plan.Steps[1].Result, "healing")
	}
	for i, step := range plan.Steps {
		if step.Ingredient != "nether_wart" {
			t.Errorf("step %d ingredient = %q, want nether_wart", i, step.Ingredient)
		}
	}
	if plan.Bottles != 3 {
		t.Errorf("plan.Bottles = %d, want 3", plan.Bottles)
	}
}

// TestBrewPlanStartsFromAwkwardPotion. A bot that already holds awkward
// potions is one cycle from healing, not two — over-brewing wastes nether wart
// and the acceptance is about the potion, not the wart count.
func TestBrewPlanStartsFromAwkwardPotion(t *testing.T) {
	t.Parallel()

	plan, ok := station.BrewPlan("potion_awkward", "nether_wart", "healing")
	if !ok {
		t.Fatal("BrewPlan(potion_awkward, healing) is not a known recipe")
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("plan has %d steps, want 1", len(plan.Steps))
	}
	if plan.Steps[0].Result != "healing" {
		t.Errorf("result = %q, want healing", plan.Steps[0].Result)
	}
}

// TestBrewPlanRejectsUnknownCombinations. A plan the server cannot execute must
// be refused, not returned with a best guess — a bogus plan burns ingredients
// and reports a brew that never happened.
func TestBrewPlanRejectsUnknownCombinations(t *testing.T) {
	t.Parallel()

	cases := []struct {
		base, ingredient, want string
	}{
		{"glass_bottle", "healing", "healing"},
		{"dirt", "nether_wart", "healing"},
		{"glass_bottle", "cobblestone", "healing"},
		{"", "nether_wart", "healing"},
	}
	for _, tc := range cases {
		if _, ok := station.BrewPlan(tc.base, tc.ingredient, tc.want); ok {
			t.Errorf("BrewPlan(%q, %q, %q) returned a plan for an impossible brew",
				tc.base, tc.ingredient, tc.want)
		}
	}
}

// TestEffectFromItemName. The runtime name table is flat, so the form and the
// effect both live in the name the server reported. A name the bot does not
// recognise must come back unchanged rather than being folded into whatever
// the caller hoped for.
func TestEffectFromItemName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"potion_healing":           "healing",
		"minecraft:potion_healing": "healing",
		"splash_healing":           "healing",
		"potion_awkward":           "awkward",
		"tipped_arrow_weakness":    "weakness",
		"lingering_harm":           "harm",
		"minecraft:potion":         "potion",
		"":                         "",
	}
	for in, want := range cases {
		if got := station.EffectFromItemName(in); got != want {
			t.Errorf("EffectFromItemName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBrewIngredientItemNames is the bridge from the recipe vocabulary to real
// item names. "nether wart" is called nether_wart in the name table, and a bot
// that looks up the wrong string never fills the ingredient slot.
func TestBrewIngredientItemNames(t *testing.T) {
	t.Parallel()

	if got := station.BrewIngredientItem("nether_wart"); got != "nether_wart" {
		t.Errorf("BrewIngredientItem(nether_wart) = %q", got)
	}
	if got := station.BrewIngredientItem("minecraft:nether_wart"); got != "nether_wart" {
		t.Errorf("BrewIngredientItem(minecraft:nether_wart) = %q, want the bare name", got)
	}
	if got := station.BrewIngredientItem(" Nether_Wart "); got != "nether_wart" {
		t.Errorf("BrewIngredientItem is not normalising case and space, got %q", got)
	}
	if got := station.BrewIngredientItem("gold_ingot"); got != "" {
		t.Errorf("BrewIngredientItem(gold_ingot) = %q, want empty: it is not a brew ingredient", got)
	}
}

// TestBrewFuelItem is the one fuel a brewing stand accepts. Sending the whole
// blaze powder stack would leave 63 of them stuck in the stand forever.
func TestBrewFuelItem(t *testing.T) {
	t.Parallel()

	if got := station.BrewFuelItem(); got != "blaze_powder" {
		t.Errorf("BrewFuelItem() = %q, want blaze_powder", got)
	}
}

// TestBrewTimeoutScalesWithBottles. Three bottles brew in parallel, so the wait
// is one cycle regardless of count — but the budget still has to be a real
// number of seconds, not a single poll.
func TestBrewTimeoutScalesWithBottles(t *testing.T) {
	t.Parallel()

	one := station.BrewTimeout(1)
	three := station.BrewTimeout(3)
	if one <= 0 {
		t.Fatalf("BrewTimeout(1) = %v, want a positive budget", one)
	}
	if three < one {
		t.Errorf("BrewTimeout(3) = %v is shorter than BrewTimeout(1) = %v", three, one)
	}
	if three > 3*time.Minute {
		t.Errorf("BrewTimeout(3) = %v, longer than a brewing stand could ever need", three)
	}
}

// TestBrewPotionBrewsHealingPotion is the acceptance test for 4.1: nether wart
// plus a glass bottle produces a healing potion, confirmed from server state.
func TestBrewPotionBrewsHealingPotion(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(4, 64, 5, "brewing_stand")
	bot.addItem(0, 1, "glass_bottle", 3)
	bot.addItem(10, 2, "nether_wart", 4)
	bot.addItem(20, 3, "blaze_powder", 2)
	// The stand finishes each cycle after a few polls. Healing a glass bottle
	// is two cycles: awkward first, then healing.
	bot.brewAfterReads = 2
	bot.brewSequence = []string{"potion_awkward", "potion_healing"}

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetBrewBudget(2 * time.Second)

	result, ok := m.BrewPotion(context.Background(), 3, "glass_bottle", "nether_wart", "healing")
	if !ok {
		t.Fatal("BrewPotion reported failure for a recipe the stand can brew")
	}
	if result.Effect != "healing" {
		t.Errorf("BrewPotion effect = %q, want healing", result.Effect)
	}
	if result.Potions <= 0 {
		t.Errorf("BrewPotion took %d potions, want at least 1 confirmed from the server", result.Potions)
	}
}

// TestBrewPotionFailsWithoutABrewingStand. The refusal that matters: a bot that
// "brews" against a wall reports a potion it never made.
func TestBrewPotionFailsWithoutABrewingStand(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(4, 64, 5, "stone")
	bot.addItem(0, 1, "glass_bottle", 3)
	bot.addItem(10, 2, "nether_wart", 4)
	bot.addItem(20, 3, "blaze_powder", 2)

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)

	if _, ok := m.BrewPotion(context.Background(), 3, "glass_bottle", "nether_wart", "healing"); ok {
		t.Error("BrewPotion reported success with no brewing stand anywhere near")
	}
	if bot.clicked {
		t.Error("BrewPotion clicked a block that is not a brewing stand")
	}
}

// TestBrewPotionFailsWhenTheServerNeverOpens is the ContainerOpen wait. A click
// that swings the arm is not a window, and assuming one is how the old furnace
// guessed slot 0.
func TestBrewPotionFailsWhenTheServerNeverOpens(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(4, 64, 5, "brewing_stand")
	bot.addItem(0, 1, "glass_bottle", 3)
	bot.addItem(10, 2, "nether_wart", 4)
	bot.addItem(20, 3, "blaze_powder", 2)
	bot.openFails = true

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetBrewBudget(50 * time.Millisecond)

	if _, ok := m.BrewPotion(context.Background(), 3, "glass_bottle", "nether_wart", "healing"); ok {
		t.Error("BrewPotion reported success with no window opened")
	}
	if bot.closedWindow != 0 {
		t.Errorf("BrewPotion closed window %d, want 0: no window was ever opened", bot.closedWindow)
	}
}

// TestBrewPotionClosesTheServerAssignedWindow. The window ID is whatever the
// server said — here 7, not 0 — and it must be closed by that same ID or the
// stand stays open server-side and the next brew desyncs.
func TestBrewPotionClosesTheServerAssignedWindow(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(4, 64, 5, "brewing_stand")
	bot.addItem(0, 1, "glass_bottle", 3)
	bot.addItem(10, 2, "nether_wart", 4)
	bot.addItem(20, 3, "blaze_powder", 2)
	bot.brewAfterReads = 2
	bot.brewSequence = []string{"potion_awkward", "potion_healing"}

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetBrewBudget(2 * time.Second)

	if _, ok := m.BrewPotion(context.Background(), 3, "glass_bottle", "nether_wart", "healing"); !ok {
		t.Fatal("BrewPotion failed; this test is about the window lifecycle")
	}
	if bot.closedWindow != bot.windowID {
		t.Errorf("closed window %d, want the server-assigned %d", bot.closedWindow, bot.windowID)
	}
	if !bot.closedLook {
		t.Error("the look was never reset after the brew")
	}
	if watch, click := bot.eventIndex("watch"), bot.eventIndex("click"); watch < 0 || click < 0 || watch > click {
		t.Errorf("watch at %d and click at %d: the watch must be armed before the click", watch, click)
	}
}

// TestBrewPotionFailsWithoutFuel. A stand with no blaze powder never fires,
// and a bot that reports a potion anyway is the exact lie this package exists
// to prevent.
func TestBrewPotionFailsWithoutFuel(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(4, 64, 5, "brewing_stand")
	bot.addItem(0, 1, "glass_bottle", 3)
	bot.addItem(10, 2, "nether_wart", 4)
	bot.brewAfterReads = 1
	bot.brewSequence = []string{"potion_awkward", "potion_healing"}

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetBrewBudget(50 * time.Millisecond)

	if _, ok := m.BrewPotion(context.Background(), 3, "glass_bottle", "nether_wart", "healing"); ok {
		t.Error("BrewPotion reported a potion from a stand with no fuel")
	}
}

// TestBrewPotionDoesNotBrewIntoNothing. The stand is lit, the ingredients are
// in, and the server simply never delivers a result. The honest answer is
// false — not a potion nobody can find.
func TestBrewPotionDoesNotBrewIntoNothing(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(4, 64, 5, "brewing_stand")
	bot.addItem(0, 1, "glass_bottle", 3)
	bot.addItem(10, 2, "nether_wart", 4)
	bot.addItem(20, 3, "blaze_powder", 2)
	// No brewResult: the stand never finishes.

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetBrewBudget(60 * time.Millisecond)

	result, ok := m.BrewPotion(context.Background(), 3, "glass_bottle", "nether_wart", "healing")
	if ok {
		t.Error("BrewPotion reported success although the server never produced a potion")
	}
	if result.Potions != 0 {
		t.Errorf("BrewPotion reported %d potions from a stand that brewed nothing", result.Potions)
	}
	if bot.closedWindow != bot.windowID {
		t.Error("a failed brew must still close the window it opened")
	}
}

// TestBrewPotionRejectsAnUnknownRecipe before it opens anything. A plan the
// stand cannot execute should not cost a window, a click, or any ingredients.
func TestBrewPotionRejectsAnUnknownRecipe(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(4, 64, 5, "brewing_stand")
	bot.addItem(0, 1, "glass_bottle", 3)
	bot.addItem(10, 2, "nether_wart", 4)
	bot.addItem(20, 3, "blaze_powder", 2)

	m := station.NewManager(bot, discardLogger())
	if _, ok := m.BrewPotion(context.Background(), 3, "dirt", "nether_wart", "healing"); ok {
		t.Error("BrewPotion reported success for a recipe that does not exist")
	}
	if bot.clicked {
		t.Error("BrewPotion opened the stand for a recipe it should have refused up front")
	}
}

// TestBrewPotionFailsWhenTheBottleSlotTransferFails. A rejected ItemStackRequest
// is a failure, not a no-op: the ingredients never entered the stand.
func TestBrewPotionFailsWhenTheBottleSlotTransferFails(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(4, 64, 5, "brewing_stand")
	bot.addItem(0, 1, "glass_bottle", 3)
	bot.addItem(10, 2, "nether_wart", 4)
	bot.addItem(20, 3, "blaze_powder", 2)
	bot.placeErr[station.BrewBottleSlot0] = errFakeNoItem

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetBrewBudget(50 * time.Millisecond)

	if _, ok := m.BrewPotion(context.Background(), 3, "glass_bottle", "nether_wart", "healing"); ok {
		t.Error("BrewPotion reported success although the bottle stack request failed")
	}
	if bot.closedWindow != bot.windowID {
		t.Error("a failed load must still close the window")
	}
}
