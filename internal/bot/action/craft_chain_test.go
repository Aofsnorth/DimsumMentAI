package action

import "testing"

func TestNormalizeIngredientKey(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"minecraft:planks": "planks",
		"Planks":           "planks",
		"oak_planks":       "planks",
		"warped_planks":    "planks",
		" oak_planks ":     "planks",
	}
	for in, want := range cases {
		if got := normalizeIngredientKey(in); got != want {
			t.Errorf("normalizeIngredientKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIngredientCraftCountsUseCraftsNotOutput(t *testing.T) {
	t.Parallel()
	// "stick,4": one stick craft consumes 2 planks and yields 4 sticks. The old
	// planner passed the requested output (4) into ingredient planning, making
	// the bot demand 8 planks and fail with 5 oak logs.
	outputPerCraft := 4
	crafts := computeCrafts(4, outputPerCraft)
	if crafts != 1 {
		t.Fatalf("computeCrafts(4, %d) = %d, want 1", outputPerCraft, crafts)
	}
	plankNeed := 2 * crafts
	if plankNeed != 2 {
		t.Errorf("plank need = %d, want 2", plankNeed)
	}
}

func TestIngredientFallbacksHasPlanks(t *testing.T) {
	t.Parallel()
	fb, ok := ingredientFallbacks["planks"]
	if !ok || len(fb) == 0 {
		t.Fatal("expected a non-empty planks fallback list")
	}
	if fb[0] != "oak_planks" {
		t.Errorf("first planks fallback = %q, want oak_planks", fb[0])
	}
}
