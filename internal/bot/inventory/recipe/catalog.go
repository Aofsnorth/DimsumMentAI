package recipe

import "strings"

// This file is the station catalogue: what these four stations actually make,
// as data. No logic lives here, so the acceptance criteria for 4.5-4.8 can be
// read straight off the table instead of being reverse-engineered from a
// planner.

// Upgrade is one smithing-table transform.
//
// Base is the item being upgraded, Template is the smithing template the
// upgrade needs, and Result is what comes out. Since the netherite update
// (1.19.4) a SmithingTransformRecipe has exactly three ingredient descriptors —
// Template, Base, Addition — and the template is consumed: one template per
// craft, never returned.
type Upgrade struct {
	Base     string
	Template string
	Result   string
	// TemplateConsumed is how many templates one craft eats. It is data rather
	// than a constant so the rule is visible at the call site that depends on
	// it, and so a future non-consumed transform cannot be silently wrong.
	TemplateConsumed int
}

// NetheriteUpgradeTemplateNames lists every item name a server has used for the
// netherite upgrade template.
//
// 1.19.4 - 1.20.0 called it "minecraft:netherite_upgrade"; 1.20.1 renamed it
// "minecraft:netherite_upgrade_smithing_template". The vendored gophertunnel
// carries no item palette at all — Bedrock item names arrive at runtime from
// the server's item registry — so both spellings are matched and neither is
// invented here. The shared prefix is what TemplateMatches keys on.
var NetheriteUpgradeTemplateNames = []string{
	"netherite_upgrade_smithing_template",
	"netherite_upgrade",
}

// netheriteTemplatePrefix is the substring both spellings share. Matching on the
// prefix rather than on an exact name is what makes one catalogue entry serve
// both the 1.19.4 and the 1.20.1+ server.
const netheriteTemplatePrefix = "netherite_upgrade"

// NetheriteUpgrades returns the smithing transforms that turn diamond gear into
// netherite gear, in a fixed order so a plan is reproducible.
func NetheriteUpgrades() []Upgrade {
	return []Upgrade{
		{Base: "diamond_sword", Result: "netherite_sword", TemplateConsumed: 1},
		{Base: "diamond_pickaxe", Result: "netherite_pickaxe", TemplateConsumed: 1},
		{Base: "diamond_axe", Result: "netherite_axe", TemplateConsumed: 1},
		{Base: "diamond_shovel", Result: "netherite_shovel", TemplateConsumed: 1},
		{Base: "diamond_hoe", Result: "netherite_hoe", TemplateConsumed: 1},
		{Base: "diamond_helmet", Result: "netherite_helmet", TemplateConsumed: 1},
		{Base: "diamond_chestplate", Result: "netherite_chestplate", TemplateConsumed: 1},
		{Base: "diamond_leggings", Result: "netherite_leggings", TemplateConsumed: 1},
		{Base: "diamond_boots", Result: "netherite_boots", TemplateConsumed: 1},
	}
}

// TemplateMatches reports whether an item name is a smithing upgrade template.
func TemplateMatches(name string) bool {
	name = NormalizeName(name)
	return name != "" && strings.Contains(name, netheriteTemplatePrefix)
}

// Stonecut is one stonecutter transform: a single block in, one cut variant out.
type Stonecut struct {
	Input  string
	Result string
}

// Stonecuts returns the stonecutter transforms from plain stone.
//
// This is the catalogue behind acceptance 4.6 ("stone → stone bricks"). Every
// entry is a real vanilla stonecutter output of stone; the list is deliberately
// short rather than exhaustive, because every extra entry is a claim this
// package cannot verify without a live server.
func Stonecuts() []Stonecut {
	return []Stonecut{
		{Input: "stone", Result: "stone_bricks"},
		{Input: "stone", Result: "stone_brick_slab"},
		{Input: "stone", Result: "stone_brick_stairs"},
		{Input: "stone", Result: "stone_brick_wall"},
		{Input: "stone", Result: "stone_brick_slab_2"},
		{Input: "stone", Result: "chiseled_stone_bricks"},
		{Input: "stone", Result: "polished_stone"},
		{Input: "stone", Result: "polished_stone_bricks"},
		{Input: "stone", Result: "polished_stone_brick_slab"},
		{Input: "stone", Result: "polished_stone_brick_stairs"},
		{Input: "stone", Result: "polished_stone_brick_wall"},
		{Input: "stone", Result: "smooth_stone"},
		{Input: "stone", Result: "stone_slab"},
		{Input: "stone", Result: "stone_stairs"},
		{Input: "stone", Result: "stone_wall"},
		{Input: "cobblestone", Result: "cobblestone_stairs"},
		{Input: "cobblestone", Result: "cobblestone_slab"},
		{Input: "cobblestone", Result: "smooth_stone"},
	}
}

// FindStonecut returns the stonecutter transform that turns input into result.
func FindStonecut(input, result string) (Stonecut, bool) {
	in := NormalizeName(input)
	out := NormalizeName(result)
	for _, sc := range Stonecuts() {
		if sc.Input == in && sc.Result == out {
			return sc, true
		}
	}
	return Stonecut{}, false
}

// BannerPattern is one loom operation.
//
// ID is the identifier a client puts in CraftLoomRecipeStackRequestAction.Pattern
// — the loom has no CraftingData recipe at all, which is what separates it from
// the other three stations.
//
// The pattern takes its colour from a dye. Applying a pattern replaces the
// banner's colour with the dye's, so a red dye on a white banner yields a red
// patterned banner, and the result item name follows the dye. The banner's own
// stored colour lives in item NBT, which the vendored protocol cannot express,
// so the dye is the only source this package has for the result name.
type BannerPattern struct {
	// ID is the protocol pattern identifier, e.g. "minecraft:stripe_bottom".
	ID string
	// DyeName names the kind of dye the pattern needs. Every Bedrock dye colour
	// is a valid one, so the catalogue records a single representative.
	DyeName string
	// ConsumesDye is false for the plain base-border pattern, which a banner
	// gets from dyeing without a pattern and which therefore needs no dye
	// beyond what produced the colour.
	ConsumesDye bool
}

// bannerColourFromDye turns a dye name into the banner item name that comes out
// of the loom: "red_dye" -> "red_banner". Every Bedrock dye colour has a banner
// of the same name, so the mapping is a suffix swap rather than a table.
func bannerColourFromDye(dye string) string {
	dye = NormalizeName(dye)
	if dye == "" {
		return ""
	}
	const suffix = "_dye"
	if len(dye) <= len(suffix) || dye[len(dye)-len(suffix):] != suffix {
		return ""
	}
	return dye[:len(dye)-len(suffix)] + "_banner"
}

// BannerPatterns returns every loom pattern this package can apply.
//
// The IDs are the vanilla pattern identifiers. The vendored protocol types the
// field as a plain string and publishes no list, so these are the vanilla set
// and are treated as data, not as protocol constants.
func BannerPatterns() []BannerPattern {
	dye := func(id string) BannerPattern {
		return BannerPattern{ID: "minecraft:" + id, DyeName: "dye", ConsumesDye: true}
	}
	return []BannerPattern{
		dye("stripe_downbase"),
		dye("stripe_upbase"),
		dye("stripe_bottom"),
		dye("stripe_top"),
		dye("stripe_left"),
		dye("stripe_right"),
		dye("stripe_middle"),
		dye("stripe_cross"),
		dye("stripe_bottombase"),
		dye("stripe_topbase"),
		dye("curly_border"),
		dye("creeper_border"),
		dye("skull_border"),
		dye("flow"),
		dye("creeper"),
		dye("skull"),
		dye("flower"),
		dye("mojang"),
		dye("globe"),
		dye("piglin"),
		{ID: "minecraft:border", ConsumesDye: false},
	}
}

// FindBannerPattern resolves a pattern by its full protocol ID or by the bare
// suffix, so a caller can say "globe" or "minecraft:globe" and get the same
// answer.
func FindBannerPattern(id string) (BannerPattern, bool) {
	want := NormalizeName(id)
	if want == "" {
		return BannerPattern{}, false
	}
	for _, p := range BannerPatterns() {
		if NormalizeName(p.ID) == want {
			return p, true
		}
	}
	return BannerPattern{}, false
}

// CartographyAction names what a cartography table is being asked to do.
//
// The four operations below all end in a map, and the difference between them
// lives in the map's own data — the copy and the extend differ by the scale and
// origin coordinates written into the result. That data rides in the map item's
// NBT, which the vendored protocol cannot build, so the action is carried here
// as a value for the caller to log and to choose between, not as something this
// package claims to encode on the wire.
type CartographyAction string

const (
	// CartographyCopy is map + paper -> a copy of the map.
	CartographyCopy CartographyAction = "copy"
	// CartographyExtend is map + paper -> the same map at the same scale, with
	// the new paper tiles added.
	CartographyExtend CartographyAction = "extend"
	// CartographyClone is filled_map + redstone -> a locked copy.
	CartographyClone CartographyAction = "clone"
	// CartographyLock is filled_map + glass_pane -> a locked map.
	CartographyLock CartographyAction = "lock"
	// CartographyZoomOut is filled_map + paper -> a larger-scale map.
	CartographyZoomOut CartographyAction = "zoom_out"
	// CartographyExplore is map + compass -> an explorer map.
	CartographyExplore CartographyAction = "explore"
)

// CartographyOp is one cartography transform: what goes in, what comes out.
type CartographyOp struct {
	Action CartographyAction
	Inputs []string
	Result string
}

// CartographyOps returns every cartography transform this package can drive.
func CartographyOps() []CartographyOp {
	return []CartographyOp{
		{Action: CartographyCopy, Inputs: []string{"map", "paper"}, Result: "map"},
		{Action: CartographyExtend, Inputs: []string{"map", "paper"}, Result: "map"},
		{Action: CartographyClone, Inputs: []string{"filled_map", "redstone"}, Result: "map"},
		{Action: CartographyLock, Inputs: []string{"filled_map", "glass_pane"}, Result: "map"},
		{Action: CartographyZoomOut, Inputs: []string{"filled_map", "paper"}, Result: "map"},
		{Action: CartographyExplore, Inputs: []string{"map", "compass"}, Result: "map"},
	}
}

// FindCartographyOp returns the transform for an action.
func FindCartographyOp(action CartographyAction) (CartographyOp, bool) {
	for _, op := range CartographyOps() {
		if op.Action == action {
			return op, true
		}
	}
	return CartographyOp{}, false
}
