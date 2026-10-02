package protect_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/protect"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// homeZone is the "home" AABB used by most of the tests below: a 9x9 plot
// around the origin, which is the shape a player actually builds.
func homeZone(min, max protocol.BlockPos) protect.Zone {
	return protect.HomeZone("home", min, max)
}

// positionInsideHome is a cell comfortably inside the homeZone bounds above.
func positionInsideHome() protocol.BlockPos {
	return protocol.BlockPos{0, 64, 0}
}

// positionFarFromAnyZone is a cell no zone in any test claims, for the tests
// that are about the valuable list rather than about geometry.
func positionFarFromAnyZone() protocol.BlockPos {
	return protocol.BlockPos{8_000_000, 64, -8_000_000}
}

// homePolicy is a bot that has been told where home is and nothing else, so
// every assertion is about the policy and not about incidental configuration.
func homePolicy(t *testing.T) *protect.Policy {
	t.Helper()
	return protect.New(protect.Config{
		Zones: []protect.Zone{homeZone(protocol.BlockPos{-4, 60, -4}, protocol.BlockPos{4, 70, 4})},
	})
}

// --- Roadmap 9.4 acceptance criterion ---

// TestRefusesToBreakDiamondBlockInHomeZone is the acceptance criterion for
// phase 9.4, word for word: the bot must refuse to break a diamond block that
// sits inside its home zone.
func TestRefusesToBreakDiamondBlockInHomeZone(t *testing.T) {
	t.Parallel()

	p := homePolicy(t)

	got := p.Allowed(protect.Break, protocol.BlockPos{0, 61, 0}, "diamond_block")
	if got.OK {
		t.Fatal("the bot agreed to break a diamond block inside its own home zone")
	}
}

// TestRefusalNamesEveryRuleThatFired is the honesty half of the criterion. A
// refusal that names only the zone leaves the caller to discover the valuable
// block list on the next attempt, after "fixing" a zone that was never the
// problem; a refusal that names only the block is worse, because the zone is
// the thing that is easy to get wrong.
func TestRefusalNamesEveryRuleThatFired(t *testing.T) {
	t.Parallel()

	got := homePolicy(t).Allowed(protect.Break, protocol.BlockPos{0, 61, 0}, "diamond_block")
	if got.OK {
		t.Fatal("the diamond block was allowed; there is nothing to report")
	}
	for _, want := range []string{"home zone", "valuable block: diamond_block"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not mention %q", got.Reason, want)
		}
	}
}

// TestDirtInsideTheHomeZoneIsBreakableWhenConfiguredThatWay is the inverse of
// the acceptance criterion. A zone that protects building but not breaking is a
// legitimate thing to want — "keep the walls, let me dig the floor" — and a
// policy that cannot express it is a policy nobody will actually configure.
func TestDirtInsideTheHomeZoneIsBreakableWhenConfiguredThatWay(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{
		Zones: []protect.Zone{{
			Name:        "home",
			Min:         protocol.BlockPos{-4, 60, -4},
			Max:         protocol.BlockPos{4, 70, 4},
			Enabled:     true,
			Permissions: protect.NoBuild,
		}},
	})

	got := p.Allowed(protect.Break, protocol.BlockPos{1, 60, 1}, "dirt")
	if !got.OK {
		t.Errorf("breaking dirt inside a build-only home zone was refused: %s", got.Reason)
	}

	// The same zone still refuses to build, which is the half that was asked for.
	build := p.Allowed(protect.Build, protocol.BlockPos{1, 61, 1}, "cobblestone")
	if build.OK {
		t.Error("building inside a build-protected home zone was allowed")
	}
}

// TestDiamondBlockOutsideEveryZoneIsStillRefused is the second inverse. The
// valuable list is not a zone rule: a diamond block stashed in a bot's storage
// room at the far end of the map is still the bot's, and a policy that only
// protected the home plot would let the miner chew through it.
func TestDiamondBlockOutsideEveryZoneIsStillRefused(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{
		Zones: []protect.Zone{homeZone(protocol.BlockPos{-4, 60, -4}, protocol.BlockPos{4, 70, 4})},
	})

	got := p.Allowed(protect.Break, protocol.BlockPos{5000, 61, -9000}, "diamond_block")
	if got.OK {
		t.Fatal("a diamond block 9000 blocks from home was allowed to be broken")
	}
	if !strings.Contains(got.Reason, "valuable block: diamond_block") {
		t.Errorf("reason %q does not name the valuable block", got.Reason)
	}
	if strings.Contains(got.Reason, "home zone") {
		t.Errorf("reason %q claims a zone that is 9000 blocks away", got.Reason)
	}
}

// TestDirtFarFromHomeIsBreakable is the control for the test above: the
// valuable list is a short list, not a "refuse everything" switch.
func TestDirtFarFromHomeIsBreakable(t *testing.T) {
	t.Parallel()

	got := homePolicy(t).Allowed(protect.Break, protocol.BlockPos{5000, 61, -9000}, "dirt")
	if !got.OK {
		t.Errorf("breaking dirt in open field was refused: %s", got.Reason)
	}
}

// --- The unconfigured default ---

// TestUnconfiguredPolicyProtectsTheBotsMaterial is the decision that a policy
// nobody configured is still worth having. An empty zone list protects no
// place, but the valuable list still applies everywhere, because the thing most
// likely to be destroyed by a misconfigured bot is the bot's own stockpile.
func TestUnconfiguredPolicyProtectsTheBotsMaterial(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	if _, inside := p.ZoneAt(protocol.BlockPos{0, 64, 0}); inside {
		t.Error("an unconfigured policy claimed a zone at the origin")
	}
	if got := p.Allowed(protect.Break, protocol.BlockPos{0, 64, 0}, "diamond_block"); got.OK {
		t.Error("an unconfigured policy broke a diamond block")
	}
	if got := p.Allowed(protect.Break, protocol.BlockPos{0, 64, 0}, "dirt"); !got.OK {
		t.Errorf("an unconfigured policy refused to break dirt: %s", got.Reason)
	}
}

// TestUnconfiguredPolicyAllowsBuilding pins the other half of the default. A
// policy that refused to build as well as to break would stop a bot dead the
// moment it was switched on, and "the bot stood still" is a much worse failure
// than "the bot built one block too many".
func TestUnconfiguredPolicyAllowsBuilding(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	got := p.Allowed(protect.Build, protocol.BlockPos{0, 64, 0}, "cobblestone")
	if !got.OK {
		t.Errorf("an unconfigured policy refused to build: %s", got.Reason)
	}
}

// TestAnExplicitlyEmptyValuableListDisablesTheBuiltInList is what makes the
// default above a default rather than a rule. nil means "you did not say", and
// gets the built-in list; an empty non-nil slice means "I say nothing is
// valuable", and is obeyed.
func TestAnExplicitlyEmptyValuableListDisablesTheBuiltInList(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{ValuableBlocks: []string{}})

	if got := p.Allowed(protect.Break, protocol.BlockPos{0, 64, 0}, "diamond_block"); !got.OK {
		t.Errorf("a caller who cleared the valuable list was still refused: %s", got.Reason)
	}
}

// TestCallerValuableListReplacesTheDefaultRatherThanAddingToIt. A caller who
// names the blocks they care about should not have to re-type the whole vanilla
// list to override one entry.
func TestCallerValuableListReplacesTheDefaultRatherThanAddingToIt(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{ValuableBlocks: []string{"dirt"}})

	if got := p.Allowed(protect.Break, protocol.BlockPos{0, 64, 0}, "dirt"); got.OK {
		t.Error("dirt was not on the caller's valuable list")
	}
	if got := p.Allowed(protect.Break, protocol.BlockPos{0, 64, 0}, "diamond_block"); !got.OK {
		t.Errorf("the built-in list survived an explicit caller list: %s", got.Reason)
	}
}

// --- Decisions ---

// TestAllowedCarriesAReasonOnBothOutcomes. A Decision whose Reason is empty
// when it says yes is a Decision that produces an empty line in a log, which
// reads as a bug in whatever wrote the log.
func TestAllowedCarriesAReasonOnBothOutcomes(t *testing.T) {
	t.Parallel()

	p := homePolicy(t)

	yes := p.Allowed(protect.Build, protocol.BlockPos{9, 64, 9}, "cobblestone")
	if !yes.OK {
		t.Fatalf("building outside the home zone was refused: %s", yes.Reason)
	}
	if yes.Reason == "" {
		t.Error("an allowed Decision carried no reason")
	}

	no := p.Allowed(protect.Build, protocol.BlockPos{0, 64, 0}, "cobblestone")
	if no.OK {
		t.Fatal("building inside the home zone was allowed")
	}
	if no.Reason == "" {
		t.Error("a refused Decision carried no reason")
	}
}

// TestUnknownActionIsRefused is fail-closed. An action the policy has never
// heard of is not an action it has decided to permit, and a protection layer
// that answers "yes, I suppose" to a value it does not understand has a hole
// exactly the shape of the value nobody thought to add.
func TestUnknownActionIsRefused(t *testing.T) {
	t.Parallel()

	got := homePolicy(t).Allowed(protect.Action(0), protocol.BlockPos{9000, 64, 9000}, "dirt")
	if got.OK {
		t.Fatal("an action the policy does not know was allowed")
	}
	if !strings.Contains(got.Reason, "unknown action") {
		t.Errorf("reason %q does not say the action was unknown", got.Reason)
	}
}

// TestUnknownBlockNameIsRefusedOnBreak is the other fail-closed edge, and it is
// the one that actually bites. A caller holding a position in a chunk the world
// model has not seen passes "" — and a policy that reads "" as "not valuable"
// will happily let the bot swing at a cell it knows nothing about.
func TestUnknownBlockNameIsRefusedOnBreak(t *testing.T) {
	t.Parallel()

	got := homePolicy(t).Allowed(protect.Break, protocol.BlockPos{9000, 64, 9000}, "")
	if got.OK {
		t.Fatal("breaking was allowed with no knowledge of the block")
	}
	if !strings.Contains(got.Reason, "block name unknown") {
		t.Errorf("reason %q does not say the block name was unknown", got.Reason)
	}
}

// TestUnknownBlockNameDoesNotBlockBuilding is the mirror image, and it is
// deliberate. Placing into a cell the world model has not seen is how a
// schematic gets built, and refusing it would make the policy unusable for the
// one job building has.
func TestUnknownBlockNameDoesNotBlockBuilding(t *testing.T) {
	t.Parallel()

	if got := homePolicy(t).Allowed(protect.Build, protocol.BlockPos{9000, 64, 9000}, ""); !got.OK {
		t.Errorf("building with no knowledge of the block was refused: %s", got.Reason)
	}
}

// --- Consulting a live bot ---

// fakeBot is the whole of the Bot interface: a world read, nothing more.
type fakeBot struct {
	name string
	ok   bool
	seen [3]int32
}

func (f *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	f.seen = [3]int32{x, y, z}
	return f.name, f.ok
}

// TestCheckReadsTheBlockNameFromTheBot. Check exists so a caller holding a
// position does not have to remember to resolve the name; this pins that it
// asks the bot about the cell it was given.
func TestCheckReadsTheBlockNameFromTheBot(t *testing.T) {
	t.Parallel()

	bot := &fakeBot{name: "diamond_block", ok: true}
	got := homePolicy(t).Check(bot, protect.Break, protocol.BlockPos{2, 63, -1})

	if got.OK {
		t.Fatal("Check allowed a break the bot's own world data forbids")
	}
	if want := (protocol.BlockPos{2, 63, -1}); bot.seen != [3]int32{want.X(), want.Y(), want.Z()} {
		t.Errorf("bot was asked about %v, want %v", bot.seen, want)
	}
}

// TestCheckRefusesWhenTheWorldDoesNotKnowTheCell. The bool half of
// GetBlockName is the "not loaded" signal, and it must not be read as an empty
// name that happens to be breakable.
func TestCheckRefusesWhenTheWorldDoesNotKnowTheCell(t *testing.T) {
	t.Parallel()

	got := homePolicy(t).Check(&fakeBot{name: "diamond_block", ok: false}, protect.Break, protocol.BlockPos{9000, 64, 9000})
	if got.OK {
		t.Fatal("Check allowed a break in a cell the world model has not loaded")
	}
	if !strings.Contains(got.Reason, "block name unknown") {
		t.Errorf("reason %q does not say the block name was unknown", got.Reason)
	}
}

// TestCheckWithANilBotRefusesRatherThanPanicking. A guard that crashes the bot
// protects nothing at all.
func TestCheckWithANilBotRefusesRatherThanPanicking(t *testing.T) {
	t.Parallel()

	got := homePolicy(t).Check(nil, protect.Break, protocol.BlockPos{0, 64, 0})
	if got.OK {
		t.Fatal("Check allowed a break with no bot to consult")
	}
	if !strings.Contains(got.Reason, "no world") {
		t.Errorf("reason %q does not explain the missing world", got.Reason)
	}
}

// --- Isolation from the caller's config ---

// TestPolicyCopiesTheCallersConfig. Config is a value the caller is likely to
// build once and keep around — a parsed YAML slice, say. If the policy aliased
// it, editing that slice at runtime would silently change what the bot is
// allowed to destroy, with no code change to point at.
func TestPolicyCopiesTheCallersConfig(t *testing.T) {
	t.Parallel()

	zones := []protect.Zone{homeZone(protocol.BlockPos{-4, 60, -4}, protocol.BlockPos{4, 70, 4})}
	valuable := []string{"moss_block"}
	p := protect.New(protect.Config{Zones: zones, ValuableBlocks: valuable})

	zones[0].Enabled = false
	zones[0].Permissions = 0
	valuable[0] = "dirt"

	if _, inside := p.ZoneAt(protocol.BlockPos{0, 64, 0}); !inside {
		t.Error("disabling the caller's zone slice disabled the policy's zone too")
	}
	if got := p.Allowed(protect.Break, positionFarFromAnyZone(), "moss_block"); got.OK {
		t.Error("rewriting the caller's valuable slice changed the policy's list")
	}
}

// TestNilPolicyRefusesRatherThanAllows. A nil *Policy is a caller that forgot to
// build one, which is a programming error rather than a configuration — and a
// protection layer whose failure mode is "assume the worst is fine" turns one
// missing constructor into a bot that mines its own house.
func TestNilPolicyRefusesRatherThanAllows(t *testing.T) {
	t.Parallel()

	var p *protect.Policy

	if got := p.Allowed(protect.Break, positionFarFromAnyZone(), "dirt"); got.OK {
		t.Error("a nil policy allowed a break")
	}
	if p.IsValuable("dirt") {
		t.Error("a nil policy reported a block as valuable")
	}
	if _, ok := p.ZoneAt(positionFarFromAnyZone()); ok {
		t.Error("a nil policy claimed a zone")
	}
}
