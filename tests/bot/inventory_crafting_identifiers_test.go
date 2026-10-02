package bot_test

import (
	"testing"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// TestResolveIngredientIdentityUsesNamespacedName covers the ServerData-era
// change where item descriptors identify items by name instead of network ID.
func TestResolveIngredientIdentityUsesNamespacedName(t *testing.T) {
	t.Parallel()

	itemNames := map[int32]string{
		5:   "stick",
		17:  "oak_log",
		-99: "minecraft:oak_log",
	}

	tests := []struct {
		name       string
		descriptor protocol.ItemDescriptor
		wantName   string
		wantNetID  int32
	}{
		{
			name:       "known identifier resolves to name and lowest network ID",
			descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_log"},
			wantName:   "minecraft:oak_log",
			wantNetID:  -99,
		},
		{
			name:       "unprefixed identifier still matches",
			descriptor: &protocol.DefaultItemDescriptor{Name: "stick"},
			wantName:   "stick",
			wantNetID:  5,
		},
		{
			name:       "unknown identifier falls back to the identifier",
			descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:diamond"},
			wantName:   "minecraft:diamond",
			wantNetID:  0,
		},
		{
			name:       "empty identifier is unresolvable",
			descriptor: &protocol.DefaultItemDescriptor{},
			wantName:   "",
			wantNetID:  0,
		},
		{
			name:       "tag descriptor returns the tag",
			descriptor: &protocol.ItemTagItemDescriptor{Tag: "planks"},
			wantName:   "planks",
			wantNetID:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotName, gotNetID := bot.ResolveIngredientIdentity(tt.descriptor, itemNames)
			if gotName != tt.wantName || gotNetID != tt.wantNetID {
				t.Fatalf("resolveIngredientIdentity() = (%q, %d), want (%q, %d)",
					gotName, gotNetID, tt.wantName, tt.wantNetID)
			}
		})
	}
}

// TestResolveIngredientIdentityIsDeterministic guards against map iteration
// order leaking into behaviour: when two runtime IDs share a name, the resolved
// network ID must stay the same on every call.
func TestResolveIngredientIdentityIsDeterministic(t *testing.T) {
	t.Parallel()

	itemNames := map[int32]string{
		17:  "oak_log",
		-99: "oak_log",
		40:  "oak_log",
	}
	descriptor := &protocol.DefaultItemDescriptor{Name: "minecraft:oak_log"}

	firstName, firstID := bot.ResolveIngredientIdentity(descriptor, itemNames)
	if firstName != "oak_log" || firstID != -99 {
		t.Fatalf("first resolve = (%q, %d), want (%q, %d)", firstName, firstID, "oak_log", int32(-99))
	}
	for i := 0; i < 50; i++ {
		gotName, gotID := bot.ResolveIngredientIdentity(descriptor, itemNames)
		if gotName != firstName || gotID != firstID {
			t.Fatalf("resolve %d = (%q, %d), want stable (%q, %d)", i, gotName, gotID, firstName, firstID)
		}
	}
}

// TestStackRequestItemFromStack guards the craft-result conversion: results are
// now addressed by identifier, so an unresolvable name must be reported instead
// of silently producing an empty identifier the server would reject.
func TestStackRequestItemFromStack(t *testing.T) {
	t.Parallel()

	stack := protocol.ItemStack{
		ItemType:       protocol.ItemType{NetworkID: 5, MetadataValue: 2},
		BlockRuntimeID: 101,
		Count:          4,
		CanBePlacedOn:  []string{"minecraft:stone"},
	}

	got, ok := bot.StackRequestItemFromStack(stack, "stick")
	if !ok {
		t.Fatal("stackRequestItemFromStack rejected a resolvable name")
	}
	if got.Identifier != "minecraft:stick" {
		t.Fatalf("identifier = %q, want %q", got.Identifier, "minecraft:stick")
	}
	if got.Count != 4 || got.MetadataValue != 2 || got.BlockRuntimeID != 101 {
		t.Fatalf("unexpected converted fields: %+v", got)
	}

	if got, ok := bot.StackRequestItemFromStack(stack, "minecraft:stick"); !ok || got.Identifier != "minecraft:stick" {
		t.Fatalf("already-prefixed name mishandled: %+v (ok=%v)", got, ok)
	}

	if _, ok := bot.StackRequestItemFromStack(stack, ""); ok {
		t.Fatal("stackRequestItemFromStack accepted an empty name")
	}
}

// TestBuildCraftActionsRejectsUnknownOutput makes sure a craft is not submitted
// with an unusable output identifier, which would otherwise fail server-side
// with no actionable reason.
func TestBuildCraftActionsRejectsUnknownOutput(t *testing.T) {
	t.Parallel()

	recipe := bot.RecipeInfo{
		Output: protocol.ItemStack{
			ItemType: protocol.ItemType{NetworkID: 5},
			Count:    4,
		},
	}

	actions, err := bot.BuildCraftActions(-3, 414, recipe, 1, nil, 3, "")
	if err == nil {
		t.Fatal("buildCraftActions should reject an unresolvable output name")
	}
	if actions != nil {
		t.Fatalf("expected no actions on error, got %d", len(actions))
	}

	actions, err = bot.BuildCraftActions(-3, 414, recipe, 1, nil, 3, "stick")
	if err != nil {
		t.Fatalf("buildCraftActions() error = %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("buildCraftActions produced no actions for a valid output")
	}
}
