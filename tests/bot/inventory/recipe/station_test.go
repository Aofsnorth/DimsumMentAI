package recipe_test

import (
	"testing"

	"bedrock-ai/internal/bot/inventory/recipe"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// TestEveryRecipeStationIsReachableByName is the guard for the bug this whole
// package exists to avoid: a station "found" by asking whether a nearby block is
// solid. A wall, a floor, the bot's own feet — all solid, all accepted, and the
// bot then reports having smithed in a wall. The block name is the only honest
// test, and it has to survive the forms a world actually reports.
func TestEveryRecipeStationIsReachableByName(t *testing.T) {
	t.Parallel()

	for _, want := range []recipe.Kind{
		recipe.KindSmithingTable,
		recipe.KindStonecutter,
		recipe.KindLoom,
		recipe.KindCartographyTable,
	} {
		station, ok := recipe.StationByKind(want)
		if !ok {
			t.Fatalf("StationByKind(%q) not found", want)
		}
		for _, form := range []string{
			station.Block,
			"minecraft:" + station.Block,
			"Minecraft:" + station.Block,
		} {
			got, ok := recipe.StationByBlockName(form)
			if !ok {
				t.Errorf("StationByBlockName(%q) not recognised, want kind %q", form, want)
				continue
			}
			if got.Kind != want {
				t.Errorf("StationByBlockName(%q) = kind %q, want %q", form, got.Kind, want)
			}
		}
	}
}

// TestStationByBlockNameRejectsEverythingElse keeps the predicate from drifting
// into a substring match. "chest" must not find a cartography table, and an
// empty name must not find anything at all.
func TestStationByBlockNameRejectsEverythingElse(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"", "   ", "chest", "crafting_table", "furnace", "barrel",
		"minecraft:cartography", "not_a_stonecutter_but_long",
	} {
		if _, ok := recipe.StationByBlockName(name); ok {
			t.Errorf("StationByBlockName(%q) matched a station", name)
		}
		if recipe.IsRecipeStationBlock(name) {
			t.Errorf("IsRecipeStationBlock(%q) = true", name)
		}
	}
}

// TestStationSlotsUseTheProtocolContainerIDs pins the container each role lives
// in against the vendored gophertunnel constants. A wrong container ID compiles,
// runs, and is rejected by the server at runtime, so it has to be pinned here —
// this is the only place the mapping from role to protocol container exists.
func TestStationSlotsUseTheProtocolContainerIDs(t *testing.T) {
	t.Parallel()

	smithing, _ := recipe.StationByKind(recipe.KindSmithingTable)
	assertSlots(t, smithing,
		map[recipe.Role]byte{
			recipe.RoleTemplate: protocol.ContainerSmithingTableTemplate,
			recipe.RoleBase:     protocol.ContainerSmithingTableInput,
			recipe.RoleAddition: protocol.ContainerSmithingTableMaterial,
			recipe.RoleResult:   protocol.ContainerSmithingTableResultPreview,
		})
	if smithing.ContainerType != protocol.ContainerTypeSmithingTable {
		t.Errorf("smithing container type = %d, want %d", smithing.ContainerType, protocol.ContainerTypeSmithingTable)
	}

	stonecutter, _ := recipe.StationByKind(recipe.KindStonecutter)
	assertSlots(t, stonecutter,
		map[recipe.Role]byte{
			recipe.RoleInput:  protocol.ContainerStonecutterInput,
			recipe.RoleResult: protocol.ContainerStonecutterResultPreview,
		})
	if stonecutter.ContainerType != protocol.ContainerTypeStonecutter {
		t.Errorf("stonecutter container type = %d, want %d", stonecutter.ContainerType, protocol.ContainerTypeStonecutter)
	}

	loom, _ := recipe.StationByKind(recipe.KindLoom)
	assertSlots(t, loom,
		map[recipe.Role]byte{
			recipe.RoleInput:    protocol.ContainerLoomInput,
			recipe.RoleMaterial: protocol.ContainerLoomDye,
			recipe.RoleResult:   protocol.ContainerLoomResultPreview,
		})
	if loom.ContainerType != protocol.ContainerTypeLoom {
		t.Errorf("loom container type = %d, want %d", loom.ContainerType, protocol.ContainerTypeLoom)
	}

	cartography, _ := recipe.StationByKind(recipe.KindCartographyTable)
	assertSlots(t, cartography,
		map[recipe.Role]byte{
			recipe.RoleInput:    protocol.ContainerCartographyInput,
			recipe.RoleAddition: protocol.ContainerCartographyAdditional,
			recipe.RoleResult:   protocol.ContainerCartographyResultPreview,
		})
	if cartography.ContainerType != protocol.ContainerTypeCartography {
		t.Errorf("cartography container type = %d, want %d", cartography.ContainerType, protocol.ContainerTypeCartography)
	}
}

// TestEachStationDeclaresExactlyTheRolesItsRecipeHas guards against a station
// gaining or losing a slot. The smithing transform is defined by three
// descriptors and the loom by two; a plan that staged into a role the station
// does not have would be staging into nothing.
func TestEachStationDeclaresExactlyTheRolesItsRecipeHas(t *testing.T) {
	t.Parallel()

	cases := map[recipe.Kind][]recipe.Role{
		recipe.KindSmithingTable:    {recipe.RoleTemplate, recipe.RoleBase, recipe.RoleAddition},
		recipe.KindStonecutter:      {recipe.RoleInput},
		recipe.KindLoom:             {recipe.RoleInput, recipe.RoleMaterial},
		recipe.KindCartographyTable: {recipe.RoleInput, recipe.RoleAddition},
	}
	for kind, want := range cases {
		station, ok := recipe.StationByKind(kind)
		if !ok {
			t.Fatalf("StationByKind(%q) not found", kind)
		}
		if len(station.Inputs) != len(want) {
			t.Errorf("%s has %d input slots, want %d", kind, len(station.Inputs), len(want))
			continue
		}
		for i, role := range want {
			if station.Inputs[i].Role != role {
				t.Errorf("%s input %d has role %q, want %q", kind, i, station.Inputs[i].Role, role)
			}
			got, found := station.Input(role)
			if !found || got != station.Inputs[i] {
				t.Errorf("%s Input(%q) = %+v, %v; want %+v", kind, role, got, found, station.Inputs[i])
			}
		}
		if station.Result.Role != recipe.RoleResult {
			t.Errorf("%s result slot has role %q, want %q", kind, station.Result.Role, recipe.RoleResult)
		}
	}
}

// TestStationInputReportsAMissingRole makes the lookup honest instead of
// returning a zero Slot that would silently address window slot 0 — which is
// the template slot, the worst possible thing to address by accident.
func TestStationInputReportsAMissingRole(t *testing.T) {
	t.Parallel()

	stonecutter, _ := recipe.StationByKind(recipe.KindStonecutter)
	if _, ok := stonecutter.Input(recipe.RoleTemplate); ok {
		t.Error("the stonecutter reported a template slot; it has exactly one input")
	}
	// The container-window stations belong to another package; asking this one
	// for a brewing stand has to be an honest miss rather than a guess.
	if _, ok := recipe.StationByKind(recipe.Kind("brewing_stand")); ok {
		t.Error("a station this package does not drive was returned")
	}
}

// TestStationBlockIsTheUnNamespacedRecipeBlock keeps the block name in the same
// form the server sends in a recipe's Block field. Matching "minecraft:loom"
// against a recipe that says "loom" would find no recipe at all.
func TestStationBlockIsTheUnNamespacedRecipeBlock(t *testing.T) {
	t.Parallel()

	for _, station := range recipe.Stations() {
		if got := recipe.NormalizeName(station.Block); got != station.Block {
			t.Errorf("station %q block %q is not in bare form", station.Kind, station.Block)
		}
	}
}

func assertSlots(t *testing.T, station recipe.Station, want map[recipe.Role]byte) {
	t.Helper()
	all := make(map[recipe.Role]byte, len(want)+1)
	for _, in := range station.Inputs {
		all[in.Role] = in.ContainerID
	}
	all[recipe.RoleResult] = station.Result.ContainerID
	for role, container := range want {
		if all[role] != container {
			t.Errorf("%s role %q container = %d, want %d", station.Kind, role, all[role], container)
		}
	}
}
