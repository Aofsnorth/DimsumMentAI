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

// Regression test for the chain-craft planks bug: when the LLM asks the bot
// to craft sticks and the bot only has oak_log, the stick recipe requires
// the generic "minecraft:planks" ingredient. The resolver must expand that
// into the wood-variant fallback list so the chain-crafter can satisfy it
// from oak_log. Previously the comparison used the un-normalized name
// ("minecraft:planks" vs "planks"), so the fallback never fired and the bot
// gave up with "tidak punya bahan untuk minecraft:planks".
func TestResolveIngredientCandidates_GenericPlanksExpands(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want []string
	}{
		// The actual form Bedrock sends in CraftingData ingredients.
		{"minecraft:planks", ingredientFallbacks["planks"]},
		// Plain alias form, in case a server strips the prefix.
		{"planks", ingredientFallbacks["planks"]},
		// Whitespace + case from a parser upstream. normalize lowercases
		// before stripping the prefix, so "MINECRAFT:Planks" is recognized
		// as the generic tag.
		{"  MINECRAFT:Planks  ", ingredientFallbacks["planks"]},
	}
	for _, tc := range tests {
		got := resolveIngredientCandidates(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("resolveIngredientCandidates(%q) len = %d, want %d", tc.in, len(got), len(tc.want))
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("resolveIngredientCandidates(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

// Specific plank variants (oak_planks, spruce_planks, etc.) must NOT expand
// into the full wood fallback list — otherwise a failed oak_planks craft
// would silently try cherry_planks, making the final error blame the wrong
// wood type. The bug this protects against is the reverse of the generic
// expansion: silently masking a real failure.
func TestResolveIngredientCandidates_SpecificPlankStaysSingle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		// Unprefixed variant.
		{"oak_planks", "oak_planks"},
		// Prefixed variant — same property must hold.
		{"minecraft:oak_planks", "minecraft:oak_planks"},
		// Other variant sanity check.
		{"spruce_planks", "spruce_planks"},
	}
	for _, tc := range tests {
		got := resolveIngredientCandidates(tc.in)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("resolveIngredientCandidates(%q) = %v, want [%q]", tc.in, got, tc.want)
		}
	}
}

func TestResolveIngredientCandidates_NonPlankPassthrough(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"stick", "oak_log", "cobblestone", "diamond", "minecraft:diamond"} {
		got := resolveIngredientCandidates(name)
		if len(got) != 1 || got[0] != name {
			t.Errorf("resolveIngredientCandidates(%q) = %v, want [%q]", name, got, name)
		}
	}
}
