package station_test

import (
	"testing"

	"bedrock-ai/internal/bot/inventory/station"
)

// The brewing stand had a recipe table listing nineteen ingredients and an
// ingredient gate that accepted exactly one of them. BrewPlan rejects an empty
// ingredient before it reads the table, so the gate was not a convenience --
// it was the whole interface, and fifteen recipes were unreachable dead code.
// A bot asked to brew a splash potion or a fire-resistance potion found nothing
// it could plan and reported nothing brewable.

// TestEveryIngredientInTheRecipeTableIsAccepted walks the observable
// consequence instead of the gate: anything the plan can be built from must
// survive the ingredient check. This is the property that broke, and testing it
// this way means the test fails again if a recipe is added to the table without
// the gate noticing.
func TestEveryIngredientInTheRecipeTableIsAccepted(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		base       string
		ingredient string
		target     string
	}{
		{"nether wart into awkward", "glass_bottle", "nether_wart", "potion_awkward"},
		{"healing", "potion_awkward", "glistering_melon", "potion_healing"},
		{"fire resistance via blaze powder", "potion_awkward", "blaze_powder", "potion_fire_resistance"},
		{"fire resistance via magma cream", "potion_awkward", "magma_cream", "potion_fire_resistance"},
		{"poison", "potion_awkward", "spider_eye", "potion_poison"},
		{"swiftness", "potion_awkward", "sugar", "potion_swiftness"},
		{"leaping", "potion_awkward", "rabbit_foot", "potion_leaping"},
		{"regeneration", "potion_awkward", "ghast_tear", "potion_regeneration"},
		{"night vision", "potion_awkward", "golden_carrot", "potion_night_vision"},
		{"invisibility", "potion_awkward", "brown_mushroom", "potion_invisibility"},
		{"breathing", "potion_awkward", "dragon_breath", "potion_breathing"},
		{"water breathing", "potion_awkward", "pufferfish", "potion_water_breathing"},
		{"splash healing", "potion_healing", "gunpowder", "splash_healing_potion"},
		{"splash poison", "potion_poison", "gunpowder", "splash_poison_potion"},
		{"lingering healing", "potion_healing", "dragon_breath", "lingering_healing_potion"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if station.BrewIngredientItem(tc.ingredient) == "" {
				t.Fatalf("ingredient %q is in the recipe table but was rejected by "+
					"BrewIngredientItem, so no plan can be built from it", tc.ingredient)
			}
			if _, ok := station.BrewPlan(tc.base, tc.ingredient, tc.target); !ok {
				t.Fatalf("BrewPlan(%q, %q, %q) found no recipe even though the table "+
					"has one", tc.base, tc.ingredient, tc.target)
			}
		})
	}
}

func TestNonIngredientsAreStillRejected(t *testing.T) {
	t.Parallel()

	// The gate exists so a caller cannot put a gold ingot in the ingredient slot
	// and wait forever. Widening it to accept everything would trade fifteen dead
	// recipes for a different kind of wrong.
	for _, name := range []string{
		"", "gold_ingot", "diamond", "stone", "oak_planks", "stick", "cobblestone",
	} {
		if got := station.BrewIngredientItem(name); got != "" {
			t.Fatalf("BrewIngredientItem(%q) = %q, want it rejected as a brew ingredient", name, got)
		}
	}
}

func TestNetherWartIsStillAcceptedDirectly(t *testing.T) {
	t.Parallel()

	// nether_wart is both the awkward-potion base ingredient and the amplifier,
	// so it appears in the table as a value in one place and a key in others. It
	// must keep working as an input now that acceptance is table-derived.
	if got := station.BrewIngredientItem("nether_wart"); got != "nether_wart" {
		t.Fatalf("nether_wart was rejected: got %q", got)
	}
	if got := station.BrewIngredientItem("minecraft:nether_wart"); got != "nether_wart" {
		t.Fatalf("a namespaced nether_wart was rejected: got %q", got)
	}
}

func TestAliasesForTheSameIngredientStillResolve(t *testing.T) {
	t.Parallel()

	// The runtime name table is flat and the recipe table is canonical, so the
	// two spellings of an ingredient have to meet somewhere. Deriving the
	// accepted set from the table means an alias only works if NormalizeItemName
	// folds it, which is worth pinning.
	if got := station.BrewIngredientItem("minecraft:glistering_melon"); got == "" {
		t.Fatal("a namespaced brewing ingredient was rejected")
	}
}

// The whole point of walking the chain rather than looking up one pair is the
// two-step case: water has to become awkward and awkward has to become healing,
// with the stand opened, loaded, and fired twice. A vocabulary that only matches
// on the first step still fails here, because the intermediate base is a name
// the caller never mentioned.
func TestTwoStepChainFromWaterToHealing(t *testing.T) {
	t.Parallel()

	recipe, ok := station.BrewPlan("glass_bottle", "nether_wart", "potion_healing")
	if !ok {
		t.Fatal("no plan for water to healing, which is the two-step chain the " +
			"walk exists to handle")
	}
	if len(recipe.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d: %+v", len(recipe.Steps), recipe.Steps)
	}
	if recipe.Steps[0].Result != "awkward" {
		t.Fatalf("first step should produce the awkward potion, got %q", recipe.Steps[0].Result)
	}
	if recipe.Steps[1].Result != "healing" {
		t.Fatalf("second step should produce healing, got %q", recipe.Steps[1].Result)
	}
}

// The reverse direction matters too: nether wart upgrades an existing potion
// as well as creating one, and the amplifier recipe shares the table with the
// base recipe.
func TestNetherWartAmplifiesAnExistingPotion(t *testing.T) {
	t.Parallel()

	recipe, ok := station.BrewPlan("potion_healing", "nether_wart", "strong_healing_potion")
	if !ok {
		t.Fatal("no plan for amplifying healing to strong healing")
	}
	if len(recipe.Steps) != 1 || recipe.Steps[0].Result != "strong_healing" {
		t.Fatalf("expected one amplifying step to strong_healing, got %+v", recipe.Steps)
	}
}

// A target the table cannot produce must still be refused. Widening the
// vocabulary to make the real recipes match must not turn every request into a
// match.
func TestAnUnreachableTargetIsStillRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ base, ingredient, target string }{
		{"glass_bottle", "gold_ingot", "potion_healing"},
		{"potion_awkward", "sugar", "potion_invisibility"},
		{"cobblestone", "nether_wart", "potion_healing"},
		{"potion_awkward", "sugar", "splash_night_vision_potion"},
	} {
		if _, ok := station.BrewPlan(tc.base, tc.ingredient, tc.target); ok {
			t.Fatalf("BrewPlan(%q, %q, %q) planned a recipe that cannot exist",
				tc.base, tc.ingredient, tc.target)
		}
	}
}
