// Package recipe drives the four Bedrock stations that are recipe-driven rather
// than slot-window driven: the smithing table, the stonecutter, the loom and the
// cartography table.
//
// They differ from the container-window stations (brewing, anvil, grindstone) in
// that there is no state to wait for. The server advertises a recipe, the bot
// stages one item into each recipe role, and taking the result is a single
// server-validated request. So the interesting work is *choosing* — which recipe
// the bot's bag can actually satisfy — and that is what the Plan functions here
// do. They are pure: they take a snapshot of the bag plus the recipes the server
// advertised, and return either a fully staged Plan or an error. No connection,
// no window, no clock.
//
// The Manager executes a plan with the same discipline the furnace uses: find
// the station by block NAME rather than solidity, arm the container watch
// before the click, use the window ID the server assigned, and report success
// only once the result is actually in the bag.
//
// Every protocol constant used below is taken from the vendored gophertunnel
// (protocol.ContainerSmithingTableInput, protocol.ContainerLoomInput and so
// on). The one thing the vendored protocol does not publish is the slot index
// of a station window; the layouts below are therefore declared once here, in
// role order, and pinned by tests. If a host numbers a window differently, this
// table is the only place that has to change.
package recipe

import (
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Kind identifies one recipe-driven station.
type Kind string

const (
	KindSmithingTable    Kind = "smithing_table"
	KindStonecutter      Kind = "stonecutter"
	KindLoom             Kind = "loom"
	KindCartographyTable Kind = "cartography_table"
)

// Role names what a station slot is for. A recipe does not address slots by
// number, it addresses them by role: the smithing transform has a template, a
// base and an addition; the loom has a banner and a dye. Planning in roles and
// translating to wire numbers once is what keeps the four stations from
// scattering slot arithmetic.
type Role string

const (
	RoleTemplate Role = "template"
	RoleBase     Role = "base"
	RoleAddition Role = "addition"
	RoleInput    Role = "input"
	RoleMaterial Role = "material"
	RoleResult   Role = "result"
)

// Slot addresses one slot of an open station window.
type Slot struct {
	// Role is what the slot is for within the recipe.
	Role Role
	// ContainerID is the protocol container the slot lives in, taken from the
	// vendored protocol constants. The bot-side stack-request helper uses it to
	// build the StackRequestSlotInfo; a plan never hardcodes a container.
	ContainerID byte
	// Index is the slot index within the window the server assigned.
	Index uint32
}

// Station describes one recipe-driven station: which block it is, which
// container type the server reports for it, and the slots a recipe fills.
type Station struct {
	Kind Kind
	// Block is the un-namespaced block name. The server sends recipe entries
	// with exactly this form ("smithing_table", never "minecraft:smithing_table"),
	// so the same string matches both a block in the world and a recipe's Block
	// field.
	Block string
	// ContainerType is the ContainerType the server reports in ContainerOpen.
	ContainerType byte
	// Inputs are the slots a recipe is staged into, in the order the recipe
	// lists them.
	Inputs []Slot
	// Result is the slot the crafted item is taken from.
	Result Slot
}

// Input returns the station slot with the given role.
func (s Station) Input(role Role) (Slot, bool) {
	for _, slot := range s.Inputs {
		if slot.Role == role {
			return slot, true
		}
	}
	return Slot{}, false
}

// smithingTable is the three-input transform station. A SmithingTransformRecipe
// has exactly three ingredient descriptors — Template, Base, Addition — and the
// vendored protocol gives each its own container.
var smithingTable = Station{
	Kind:          KindSmithingTable,
	Block:         "smithing_table",
	ContainerType: protocol.ContainerTypeSmithingTable,
	Inputs: []Slot{
		{Role: RoleTemplate, ContainerID: protocol.ContainerSmithingTableTemplate, Index: 0},
		{Role: RoleBase, ContainerID: protocol.ContainerSmithingTableInput, Index: 1},
		{Role: RoleAddition, ContainerID: protocol.ContainerSmithingTableMaterial, Index: 2},
	},
	Result: Slot{Role: RoleResult, ContainerID: protocol.ContainerSmithingTableResultPreview, Index: 3},
}

// stonecutter is a one-input station: a single block in, one cut variant out.
var stonecutter = Station{
	Kind:          KindStonecutter,
	Block:         "stonecutter",
	ContainerType: protocol.ContainerTypeStonecutter,
	Inputs: []Slot{
		{Role: RoleInput, ContainerID: protocol.ContainerStonecutterInput, Index: 0},
	},
	Result: Slot{Role: RoleResult, ContainerID: protocol.ContainerStonecutterResultPreview, Index: 1},
}

// loom is a two-input station: a banner and a dye. It has no CraftingData recipe
// at all — the client sends a pattern identifier — which is why its plan carries
// a Pattern instead of a recipe network ID.
var loom = Station{
	Kind:          KindLoom,
	Block:         "loom",
	ContainerType: protocol.ContainerTypeLoom,
	Inputs: []Slot{
		{Role: RoleInput, ContainerID: protocol.ContainerLoomInput, Index: 0},
		{Role: RoleMaterial, ContainerID: protocol.ContainerLoomDye, Index: 1},
	},
	Result: Slot{Role: RoleResult, ContainerID: protocol.ContainerLoomResultPreview, Index: 2},
}

// cartographyTable is a two-input station: a map and the reagent that changes it.
var cartographyTable = Station{
	Kind:          KindCartographyTable,
	Block:         "cartography_table",
	ContainerType: protocol.ContainerTypeCartography,
	Inputs: []Slot{
		{Role: RoleInput, ContainerID: protocol.ContainerCartographyInput, Index: 0},
		{Role: RoleAddition, ContainerID: protocol.ContainerCartographyAdditional, Index: 1},
	},
	Result: Slot{Role: RoleResult, ContainerID: protocol.ContainerCartographyResultPreview, Index: 2},
}

// stations lists every recipe-driven station this package drives.
var stations = []Station{smithingTable, stonecutter, loom, cartographyTable}

// Stations returns every recipe-driven station this package drives.
func Stations() []Station {
	out := make([]Station, len(stations))
	copy(out, stations)
	return out
}

// StationByKind returns the station of a kind.
func StationByKind(kind Kind) (Station, bool) {
	for _, s := range stations {
		if s.Kind == kind {
			return s, true
		}
	}
	return Station{}, false
}

// StationByBlockName reports whether a block name is one of these stations.
//
// It is the reason this package never "finds a station" by asking whether a
// nearby block is solid: any wall, any floor and the bot's own feet would
// satisfy that, and the bot would then report having smithed in a wall. Names
// are normalised the same way item names are, so a world that reports
// "minecraft:cartography_table" or "Cartography_Table" still matches.
func StationByBlockName(name string) (Station, bool) {
	want := NormalizeName(name)
	if want == "" {
		return Station{}, false
	}
	for _, s := range stations {
		if s.Block == want {
			return s, true
		}
	}
	return Station{}, false
}

// IsRecipeStationBlock reports whether a block name is a recipe-driven station,
// without returning the descriptor. It is the predicate a block scan wants.
func IsRecipeStationBlock(name string) bool {
	_, ok := StationByBlockName(name)
	return ok
}

// NormalizeName reduces an item or block name to the bare lower-case identifier
// both the recipe descriptors and the world use: no namespace, no case. The
// server sends item names as "minecraft:diamond_sword" and block names as
// "diamond_sword", and a plan that compared the raw strings would find neither.
func NormalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "_")
	return strings.TrimPrefix(name, "minecraft:")
}
