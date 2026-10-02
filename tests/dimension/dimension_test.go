package dimension_test

import (
	"testing"

	"bedrock-ai/internal/bot/dimension"
)

// dimension.Dimension travel leaves almost no evidence behind, so the classification that
// reads it is the whole layer. These tests pin the two judgement calls that
// would otherwise be discovered by walking into things: a stray block must not
// move the bot's map, and an empty obsidian ring must not look like a working
// portal.

// TestServerIDsMapToTheThreeWorlds pins the protocol constants. They are fixed
// by Bedrock, not chosen here, and getting one wrong means a bot that believes
// it is home while standing in lava.
func TestServerIDsMapToTheThreeWorlds(t *testing.T) {
	t.Parallel()

	cases := map[int32]dimension.Dimension{0: dimension.Overworld, 1: dimension.Nether, 2: dimension.TheEnd}
	for id, want := range cases {
		if got := dimension.FromServerID(id); got != want {
			t.Errorf("dimension.FromServerID(%d) = %v, want %v", id, got, want)
		}
	}

	// An ID the protocol does not define is not "probably the overworld". It is
	// a world this client has never seen, and guessing is how a bot ends up
	// planning a trip home from somewhere it is not.
	if got := dimension.FromServerID(7); got != dimension.Unknown {
		t.Errorf("dimension.FromServerID(7) = %v, want dimension.Unknown", got)
	}
}

// TestDetectNeedsAMajorityNotASingleBlock is the false-positive guard. Someone
// storing nether brick in a chest, or building with end stone, must not move the
// bot's map.
func TestDetectNeedsAMajorityNotASingleBlock(t *testing.T) {
	t.Parallel()

	mostlyHome := []string{
		"minecraft:stone", "minecraft:dirt", "minecraft:grass_block",
		"minecraft:oak_log", "minecraft:cobblestone",
		// One of each, as decoration.
		"minecraft:netherrack",
		"minecraft:end_stone",
	}
	if got := dimension.Detect(mostlyHome); got != dimension.Unknown {
		t.Errorf("dimension.Detect = %v, want dimension.Unknown; a netherrack decoration is not a dimension change", got)
	}

	inNether := []string{"minecraft:netherrack", "minecraft:soul_sand", "minecraft:basalt", "minecraft:blackstone"}
	if got := dimension.Detect(inNether); got != dimension.Nether {
		t.Errorf("dimension.Detect = %v, want dimension.Nether", got)
	}

	inTheEnd := []string{"minecraft:end_stone", "minecraft:purpur_block", "minecraft:chorus_flower", "minecraft:end_rod"}
	if got := dimension.Detect(inTheEnd); got != dimension.TheEnd {
		t.Errorf("dimension.Detect = %v, want dimension.TheEnd", got)
	}
}

// TestObsidianIsNotADimensionMarker is the subtle one. Obsidian is the dimension.Nether
// portal frame in one world and the floor of the End in the other, so a bot
// using it as a marker would announce it is in the End every time it walks up
// to its own portal.
func TestObsidianIsNotADimensionMarker(t *testing.T) {
	t.Parallel()

	netherPortalApproach := []string{
		"minecraft:obsidian", "minecraft:obsidian", "minecraft:obsidian",
		"minecraft:obsidian", "minecraft:obsidian",
	}
	if got := dimension.Detect(netherPortalApproach); got == dimension.TheEnd {
		t.Error("standing at a dimension.Nether portal was read as the End; obsidian is not a dimension marker")
	}

	endFloor := []string{
		"minecraft:obsidian", "minecraft:obsidian", "minecraft:obsidian",
		"minecraft:obsidian", "minecraft:obsidian",
	}
	if got := dimension.Detect(endFloor); got == dimension.Nether {
		t.Error("standing on the End's obsidian floor was read as the dimension.Nether")
	}

	// With real markers alongside it, the answer is still right.
	mixed := []string{
		"minecraft:obsidian", "minecraft:end_stone", "minecraft:purpur_block",
		"minecraft:end_stone", "minecraft:chorus_flower",
	}
	if got := dimension.Detect(mixed); got != dimension.TheEnd {
		t.Errorf("dimension.Detect = %v, want dimension.TheEnd", got)
	}
}

// TestDetectOnNoEvidenceIsUnknown keeps a freshly-joined bot from inventing a
// map for itself.
func TestDetectOnNoEvidenceIsUnknown(t *testing.T) {
	t.Parallel()

	if got := dimension.Detect(nil); got != dimension.Unknown {
		t.Errorf("dimension.Detect(nil) = %v, want dimension.Unknown", got)
	}
}

// TestClassifyTellsALitPortalFromAnEmptyFrame is the difference between "walk
// in" and "find flint and steel, then walk in". A bot that conflates them walks
// confidently into an empty obsidian ring and reports that it tried to travel.
func TestClassifyTellsALitPortalFromAnEmptyFrame(t *testing.T) {
	t.Parallel()

	lit := []string{"minecraft:obsidian", "minecraft:obsidian", "minecraft:portal", "minecraft:obsidian"}
	if got := dimension.Classify(lit); got != dimension.Lit {
		t.Errorf("dimension.Classify(lit) = %v, want dimension.Lit", got)
	}

	unlit := []string{"minecraft:obsidian", "minecraft:obsidian", "minecraft:air", "minecraft:obsidian"}
	if got := dimension.Classify(unlit); got != dimension.NeedsLighting {
		t.Errorf("dimension.Classify(unlit) = %v, want dimension.NeedsLighting", got)
	}

	if got := dimension.Classify([]string{"minecraft:stone", "minecraft:dirt", "minecraft:air"}); got != dimension.NotPortal {
		t.Errorf("dimension.Classify(ordinary ground) = %v, want dimension.NotPortal", got)
	}
	if got := dimension.Classify(nil); got != dimension.NotPortal {
		t.Errorf("dimension.Classify(nil) = %v, want dimension.NotPortal", got)
	}
}

// TestClassifySeparatesTheEndPortal is the one that would hang forever. End
// portal frames cannot be lit — they are filled by throwing an eye of ender into
// them — so a bot that treated them as an unlit nether portal would stand in a
// stronghold applying flint and steel to obsidian forever.
func TestClassifySeparatesTheEndPortal(t *testing.T) {
	t.Parallel()

	frames := []string{"minecraft:end_portal_frame", "minecraft:end_portal_frame", "minecraft:air"}
	if got := dimension.Classify(frames); got != dimension.EndPortalFrame {
		t.Errorf("dimension.Classify(end frames) = %v, want dimension.EndPortalFrame", got)
	}

	open := []string{"minecraft:end_portal_frame", "minecraft:end_portal", "minecraft:air"}
	if got := dimension.Classify(open); got != dimension.EndPortalOpen {
		t.Errorf("dimension.Classify(active end portal) = %v, want dimension.EndPortalOpen", got)
	}
}

// TestNormaliseStripsNamespaces keeps the tables working on any server. A bot
// that only recognises "minecraft:netherrack" is a bot that does not recognise
// the dimension.Nether on most of the servers it will actually be pointed at.
func TestNormaliseStripsNamespaces(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"minecraft:Netherrack": "netherrack",
		" Netherrack ":         "netherrack",
		"custom:netherrack":    "netherrack",
		"NETHERRACK":           "netherrack",
	}
	for in, want := range cases {
		if got := dimension.Normalise(in); got != want {
			t.Errorf("dimension.Normalise(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDetectUsesNormalisedNames checks the marker tables go through the same
// normalisation, not a raw string compare.
func TestDetectUsesNormalisedNames(t *testing.T) {
	t.Parallel()

	if got := dimension.Detect([]string{"custom:netherrack", "MINECRAFT:Soul_Sand", "minecraft:basalt"}); got != dimension.Nether {
		t.Errorf("dimension.Detect = %v, want dimension.Nether; namespaced and mixed-case blocks were not recognised", got)
	}
}

// TestStrongholdHintsAreReportedNotActed pins the boundary. These blocks exist
// nowhere else, so one in view is evidence — but an agent that set off hunting
// strongholds from a single block would abandon whatever it was doing every time
// it walked past a ruined portal.
func TestStrongholdHintsAreReportedNotActed(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"minecraft:end_portal_frame", "end_portal", "minecraft:end_gateway", "chiseled_stone_bricks"} {
		if !dimension.IsStrongholdHint(name) {
			t.Errorf("dimension.IsStrongholdHint(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"minecraft:stone", "minecraft:chest", "minecraft:obsidian"} {
		if dimension.IsStrongholdHint(name) {
			t.Errorf("dimension.IsStrongholdHint(%q) = true, want false", name)
		}
	}
}
