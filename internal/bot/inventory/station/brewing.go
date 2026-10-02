package station

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BrewTimeoutPerCycle is how long one brewing stand cycle is allowed to take.
// Vanilla brews in 20 seconds per ingredient step, and the wait has to cover
// the slowest step plus the server's packet round trip.
const BrewTimeoutPerCycle = 20 * time.Second

// BrewTimeout budgets the wait for a brew of the given size. Bottles brew in
// parallel, so the time depends on the number of bottle slots in play rather
// than a per-bottle sum; a size of zero still gets a real budget, because
// reporting a timeout in a microsecond is a bug report, not a diagnosis.
func BrewTimeout(bottles int) time.Duration {
	if bottles < 1 {
		bottles = 1
	}
	if bottles > int(BrewBottleSlotCount) {
		bottles = int(BrewBottleSlotCount)
	}
	return time.Duration(bottles)*BrewTimeoutPerCycle + 5*time.Second
}

// BrewFuelItem is the one item a brewing stand accepts as fuel.
func BrewFuelItem() string { return "blaze_powder" }

// BrewIngredientItem normalises a recipe ingredient to the name the runtime
// table uses, and returns empty for anything that is not a brew ingredient, so
// a caller cannot send a gold ingot to a brewing stand and wait forever.
func BrewIngredientItem(name string) string {
	if NormalizeItemName(name) == "nether_wart" {
		return "nether_wart"
	}
	return ""
}

// BrewStep is one pass of the stand: the ingredient that goes in, and the
// effect the bottles reach from it.
type BrewStep struct {
	Ingredient string
	Result     string
}

// BrewRecipe is an ordered list of steps that turns a base item into a target
// potion, together with how many bottle slots the plan fills.
type BrewRecipe struct {
	// Base is the item the bottles start as.
	Base string
	// Target is the effect the caller asked for.
	Target string
	// Steps are the brew passes, in order.
	Steps []BrewStep
	// Bottles is how many bottle slots the plan fills.
	Bottles int
}

// brewRecipes maps (base, ingredient) to the resulting effect, for the chains
// this bot actually walks. It is deliberately a small explicit table: a
// half-remembered recipe list is how a bot burns nether wart and reports a
// potion it never made.
var brewRecipes = map[string]map[string]string{
	"glass_bottle": {
		"nether_wart": "awkward",
	},
	"potion_awkward": {
		"nether_wart":            "healing",
		"glistering_melon":       "healing",
		"glistering_melon_slice": "healing",
		"blaze_powder":           "fire_resistance",
		"magma_cream":            "fire_resistance",
		"spider_eye":             "poison",
		"sugar":                  "swiftness",
		"rabbit_foot":            "leaping",
		"ghast_tear":             "regeneration",
		"golden_carrot":          "night_vision",
		"brown_mushroom":         "invisibility",
		"dragon_breath":          "breathing",
		"nether_star":            "healing",
		"turtle_scute":           "turtle_master",
		"pufferfish":             "water_breathing",
		"redstone":               "long_mending",
		"phantom_membrane":       "long_invisibility",
		"fermented_spider_eye":   "venom",
	},
	"potion_healing": {
		"gunpowder":     "splash_healing",
		"nether_wart":   "strong_healing",
		"dragon_breath": "long_healing",
	},
	"potion_poison": {
		"gunpowder":     "splash_poison",
		"nether_wart":   "strong_poison",
		"dragon_breath": "long_poison",
	},
	"potion_fire_resistance": {"gunpowder": "splash_fire_resistance"},
	"potion_swiftness":       {"gunpowder": "splash_swiftness"},
	"potion_night_vision":    {"gunpowder": "splash_night_vision"},
	"potion_invisibility":    {"gunpowder": "splash_invisibility"},
	"potion_breathing":       {"gunpowder": "splash_breathing"},
	"potion_leaping":         {"gunpowder": "splash_leaping"},
	"potion_regeneration":    {"gunpowder": "splash_regeneration"},
}

// brewBaseAliases folds the several runtime names a bottle can have onto the
// one key the recipe table uses. The runtime name table is flat, so the effect
// and the form both live in the name the server reported, and a bot that only
// knew one spelling would refuse to brew an "awkward_potion" it was handed.
var brewBaseAliases = map[string]string{
	"glass_bottle":           "glass_bottle",
	"bottle":                 "glass_bottle",
	"potion":                 "potion_awkward",
	"potion_awkward":         "potion_awkward",
	"awkward_potion":         "potion_awkward",
	"splash_potion":          "potion_awkward",
	"potion_healing":         "potion_healing",
	"healing_potion":         "potion_healing",
	"splash_healing_potion":  "potion_healing",
	"potion_poison":          "potion_poison",
	"poison_potion":          "potion_poison",
	"potion_fire_resistance": "potion_fire_resistance",
	"potion_swiftness":       "potion_swiftness",
	"potion_night_vision":    "potion_night_vision",
	"potion_invisibility":    "potion_invisibility",
	"potion_breathing":       "potion_breathing",
	"potion_leaping":         "potion_leaping",
	"potion_regeneration":    "potion_regeneration",
}

// BrewPlan builds the step list that turns base into target using the given
// ingredient.
//
// It walks the recipe chain one step at a time rather than looking up a single
// (base, ingredient) pair, which is what makes the two-step glass bottle to
// healing case come out right: the first nether wart makes an awkward potion
// and the second upgrades it. The walk is capped so a malformed table cannot
// spin forever.
func BrewPlan(base, ingredient, target string) (BrewRecipe, bool) {
	current, known := brewBaseAliases[NormalizeItemName(base)]
	if !known {
		return BrewRecipe{}, false
	}
	ing := BrewIngredientItem(ingredient)
	if ing == "" {
		return BrewRecipe{}, false
	}
	want := NormalizeItemName(target)

	recipe := BrewRecipe{
		Base:    NormalizeItemName(base),
		Target:  target,
		Bottles: int(BrewBottleSlotCount),
	}
	// At most two steps is the longest chain in the table (glass bottle to
	// healing); the cap is generous enough for a future three-step recipe and
	// still bounded.
	for range 4 {
		recipes, known := brewRecipes[current]
		if !known {
			return BrewRecipe{}, false
		}
		result, known := recipes[ing]
		if !known {
			return BrewRecipe{}, false
		}
		recipe.Steps = append(recipe.Steps, BrewStep{Ingredient: ing, Result: result})
		if NormalizeItemName(result) == want {
			return recipe, true
		}
		current = "potion_" + result
	}
	return BrewRecipe{}, false
}

// BrewResult is what a brew actually produced, as read back from the server.
type BrewResult struct {
	// Effect is the effect the bottles reached, taken from the item name the
	// server delivered rather than from the plan.
	Effect string
	// Potions is how many potions were confirmed taken out of the stand.
	Potions int
}

// BrewPotion brews bottles of itemName at a nearby brewing stand until they
// reach the requested effect, and reports the confirmed result.
//
// The contract is the furnace's: true only when the server delivered the
// potions and the bot took them into its inventory. No stand, no window, no
// fuel, a rejected transfer, a stand that never finishes — all of them are a
// false with the reason logged. An action layer that reports a successful brew
// that never happened is worse than one that admits failure.
func (m *Manager) BrewPotion(ctx context.Context, bottles int, itemName, ingredient, target string) (BrewResult, bool) {
	recipe, ok := BrewPlan(itemName, ingredient, target)
	if !ok {
		m.logger.Warn("BrewPotion: no such recipe", "base", itemName, "ingredient", ingredient, "target", target)
		return BrewResult{}, false
	}
	if bottles <= 0 {
		bottles = recipe.Bottles
	}
	if bottles > int(BrewBottleSlotCount) {
		bottles = int(BrewBottleSlotCount)
	}

	base, ok := m.findInInventory(itemName)
	if !ok {
		m.logger.Warn("BrewPotion: base item not in inventory", "item", itemName)
		return BrewResult{}, false
	}
	ing, ok := m.findInInventory(recipe.Steps[0].Ingredient)
	if !ok {
		m.logger.Warn("BrewPotion: ingredient not in inventory", "ingredient", recipe.Steps[0].Ingredient)
		return BrewResult{}, false
	}
	fuel, ok := m.findInInventory(BrewFuelItem())
	if !ok {
		m.logger.Warn("BrewPotion: no brewing fuel in inventory", "fuel", BrewFuelItem())
		return BrewResult{}, false
	}

	pos, ok := m.FindNearbyStation(IsBrewingStandBlock)
	if !ok {
		m.logger.Warn("BrewPotion: no brewing stand nearby")
		return BrewResult{}, false
	}

	session, err := m.openStation(ctx, pos)
	if err != nil {
		m.logger.Warn("BrewPotion: could not open the brewing stand", "err", err)
		return BrewResult{}, false
	}
	defer m.closeStation(session)

	if err := m.loadStand(session, base, bottles, ing, fuel); err != nil {
		m.logger.Warn("BrewPotion: could not load the stand", "err", err)
		return BrewResult{}, false
	}

	effect, ok := m.runPlan(ctx, session, recipe, bottles)
	if !ok {
		m.logger.Warn("BrewPotion: the stand never produced the requested potion",
			"base", itemName, "target", target)
		return BrewResult{}, false
	}

	taken := 0
	for index := range bottles {
		slot, valid := BrewBottleSlot(index)
		if !valid {
			break
		}
		view, err := m.takeSlot(session, BrewContainerID, slot)
		if err != nil {
			m.logger.Warn("BrewPotion: could not take a brewed potion", "slot", slot, "err", err)
			return BrewResult{Effect: effect, Potions: taken}, false
		}
		m.logger.Info("brewed potion", "effect", effect, "item", view.name, "count", view.count)
		taken++
	}
	if taken == 0 {
		return BrewResult{Effect: effect}, false
	}
	return BrewResult{Effect: effect, Potions: taken}, true
}

// loadStand moves the bottles, the first ingredient, and the fuel into the
// window. Every placement is a server-validated ItemStackRequest; nothing is
// assumed to have moved.
func (m *Manager) loadStand(session stationSession, base invStack, bottles int, ingredient, fuel invStack) error {
	for index := range bottles {
		slot, ok := BrewBottleSlot(index)
		if !ok {
			return fmt.Errorf("bottle index %d is not a bottle slot", index)
		}
		// destStackNetID 0 is the empty-slot convention the container session
		// already uses: the server cross-checks it, and naming a stack that
		// does not exist yet is rejected.
		if err := m.bot.PlaceIntoContainerSlotIn(BrewContainerID, slot, 0, base.slot, 1); err != nil {
			return fmt.Errorf("place %s in bottle slot %d: %w", base.name, slot, err)
		}
	}
	if err := m.bot.PlaceIntoContainerSlotIn(BrewContainerID, BrewIngredientSlot, 0, ingredient.slot, 1); err != nil {
		return fmt.Errorf("place %s in the ingredient slot: %w", ingredient.name, err)
	}
	// A stand needs one blaze powder to light. Sending the whole stack would
	// leave the remainder sitting in the fuel slot.
	if err := m.bot.PlaceIntoContainerSlotIn(BrewFuelContainerID, BrewFuelSlot, 0, fuel.slot, 1); err != nil {
		return fmt.Errorf("place %s in the fuel slot: %w", fuel.name, err)
	}
	return nil
}

// runPlan waits out the recipe's cycles, topping the ingredient slot back up
// between them, and confirms the result from the server's own bottle slots.
func (m *Manager) runPlan(ctx context.Context, session stationSession, recipe BrewRecipe, bottles int) (string, bool) {
	budget := m.brewBudget
	if budget <= 0 {
		budget = BrewTimeout(bottles)
	}

	// The stand consumes the ingredient when a cycle completes, so a cycle is
	// finished exactly when the ingredient slot has been emptied by the
	// server. Waiting on that rather than on a fixed sleep is what makes this
	// confirm from server state instead of guessing.
	for step, brew := range recipe.Steps {
		if step > 0 {
			ing, ok := m.findInInventory(brew.Ingredient)
			if !ok {
				m.logger.Warn("brew: ran out of ingredient", "ingredient", brew.Ingredient)
				return "", false
			}
			if err := m.bot.PlaceIntoContainerSlotIn(BrewContainerID, BrewIngredientSlot, 0, ing.slot, 1); err != nil {
				m.logger.Warn("brew: could not refill the ingredient slot", "err", err)
				return "", false
			}
		}
		if !m.waitForSlotEmpty(ctx, BrewIngredientSlot, budget) {
			m.logger.Debug("brew: the stand never consumed the ingredient", "step", step)
			return "", false
		}
		if brewed, ok := m.readSlot(BrewBottleSlot0); !ok || brewed.count == 0 {
			m.logger.Warn("brew: the stand consumed the ingredient but brewed nothing")
			return "", false
		}
	}

	// Report the effect the server actually delivered, not the one the plan
	// intended, so a stand that brewed something else is not reported as the
	// requested potion.
	final, ok := m.readSlot(BrewBottleSlot0)
	if !ok || final.count == 0 {
		return "", false
	}
	return EffectFromItemName(final.name), true
}

// EffectFromItemName recovers the effect a potion item name carries. The
// runtime name table is flat, so the form and the effect both live in the name
// the server reported. An unrecognised name is returned unchanged rather than
// being folded into whatever the caller hoped for.
func EffectFromItemName(name string) string {
	n := NormalizeItemName(name)
	for _, prefix := range []string{"splash_", "lingering_", "tipped_arrow_", "potion_"} {
		if rest, ok := strings.CutPrefix(n, prefix); ok && rest != "" {
			return rest
		}
	}
	return n
}
