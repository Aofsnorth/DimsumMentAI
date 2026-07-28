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
