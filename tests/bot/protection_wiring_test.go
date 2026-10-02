package bot_test

import (
	"testing"

	"bedrock-ai/internal/bot/protect"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The unstick break was the most dangerous break in the bot and the one with the
// least restraint.
//
// It fires exactly when the body is wedged — which is exactly when a player is
// most likely to have built something worth keeping — and the only filter on it
// was the terrain one: bedrock and barriers were spared, and everything else was
// not. A wall of diamond blocks built next to a path was a wall the bot would
// tear through the first time it grazed it.
//
// The policy is consulted in break_obstacle.go now. These tests pin the answer
// the break path gets, which is what the wiring depends on.
//
// Scope honesty: BreakObstacleAt itself is not unit-tested here. It needs a live
// world model, a connection for the entity runtime ID, and it sends on a
// goroutine — so what these tests cover is the policy contract at the call site,
// not the call site's own I/O. The wiring itself needs a live server to confirm.

// TestAProtectedBlockIsRefusedBeforeAnyPacket is the behaviour, stated at the
// policy level the break path uses. It is the same call BreakObstacleAt makes,
// so it fails if the policy's answer ever stops being a refusal.
func TestAProtectedBlockIsRefusedBeforeAnyPacket(t *testing.T) {
	t.Parallel()

	home := protect.HomeZone("home", protocol.BlockPos{0, 60, 0}, protocol.BlockPos{8, 80, 8})
	home.Permissions |= protect.NoBreak
	p := protect.New(protect.Config{Zones: []protect.Zone{home}})

	pos := protocol.BlockPos{4, 64, 4}

	// A diamond block is refused for two independent reasons: it is inside the
	// home zone, and it is valuable wherever it is. The reason names both.
	decision := p.Allowed(protect.Break, pos, "minecraft:diamond_block")
	if decision.OK {
		t.Fatal("a diamond block inside the home zone was cleared for breaking")
	}
	if decision.Reason == "" {
		t.Error("the refusal carries no reason, so the caller cannot tell the user why")
	}

	// The zone refuses ALL breaking, not just valuable blocks — NoBreak is a
	// property of the place, not of the block. An earlier version of this test
	// expected dirt to still be breakable here, which would have been a promise
	// the policy does not make: a player who walls their base off wants it left
	// alone, including the dirt they paved it with.
	if dirt := p.Allowed(protect.Break, pos, "minecraft:dirt"); dirt.OK {
		t.Error("dirt inside a NoBreak zone was cleared; NoBreak refuses every block, not just valuables")
	}

	// Outside the zone, ordinary ground is fine even with the zone configured.
	if outside := p.Allowed(protect.Break, protocol.BlockPos{500, 64, 500}, "minecraft:dirt"); !outside.OK {
		t.Errorf("ordinary dirt outside every zone was refused: %q", outside.Reason)
	}
}

// TestAZoneThatOnlyForidsValuablesLeavesTheGroundAlone is the other shape a zone
// can take: no NoBreak permission at all, so the place is not a no-go area and
// only the valuable list applies inside it. Both shapes have to be reachable, or
// "protects the base" would always cost the bot the ability to clear its own
// floor.
func TestAZoneThatOnlyForidsValuablesLeavesTheGroundAlone(t *testing.T) {
	t.Parallel()

	// A zone with no Permissions set: a claim about where home is, nothing more.
	// HomeZone deliberately defaults to NoBreak|NoBuild, so a permissive zone
	// has to be spelled out — which is right, because "protect this place" and
	// "note that this is my base" are different intentions.
	home := protect.Zone{
		Name:    "home",
		Min:     protocol.BlockPos{0, 60, 0},
		Max:     protocol.BlockPos{8, 80, 8},
		Enabled: true,
	}
	p := protect.New(protect.Config{Zones: []protect.Zone{home}})

	pos := protocol.BlockPos{4, 64, 4}
	if dirt := p.Allowed(protect.Break, pos, "minecraft:dirt"); !dirt.OK {
		t.Errorf("dirt inside a zone that forbids nothing was refused: %q", dirt.Reason)
	}
	if d := p.Allowed(protect.Break, pos, "minecraft:chest"); d.OK {
		t.Error("a chest inside a permissive zone was cleared; the valuable list is position-independent")
	}
}

// TestAValuableBlockIsRefusedAnywhereNotJustAtHome is the half that does not
// depend on knowing where home is. The zone is a claim about position; a
// diamond block is a claim about the block, and the bot should not mine one out
// of a chest it is standing next to in the middle of nowhere.
func TestAValuableBlockIsRefusedAnywhereNotJustAtHome(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	decision := p.Allowed(protect.Break, protocol.BlockPos{9000, 40, 9000}, "minecraft:diamond_block")
	if decision.OK {
		t.Error("a diamond block was cleared for breaking far from any zone")
	}
	if dirt := p.Allowed(protect.Break, protocol.BlockPos{9000, 40, 9000}, "minecraft:dirt"); !dirt.OK {
		t.Errorf("ordinary dirt far from home was refused: %q", dirt.Reason)
	}
}

// TestBuildingIsNotBlockedByValuableBlocks. The valuable list is about breaking.
// A bot that refuses to *place* a diamond block would be unable to build with
// one at all, which is the opposite of what the list is for.
func TestBuildingIsNotBlockedByValuableBlocks(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	if d := p.Allowed(protect.Build, protocol.BlockPos{1, 1, 1}, "minecraft:diamond_block"); !d.OK {
		t.Errorf("placing a diamond block was refused: %q; the valuable list is about breaking", d.Reason)
	}
}

// TestAnUnconfiguredPolicyProtectsNothing, which is the documented default and
// the reason a nil policy and an empty one behave the same way.
func TestAnUnconfiguredPolicyProtectsNothing(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	// No zone was configured, so no *place* is protected — a synthetic default
	// zone would silently freeze a bot that spawns near the origin.
	if d := p.Allowed(protect.Break, protocol.BlockPos{0, 64, 0}, "minecraft:stone"); !d.OK {
		t.Errorf("stone at the origin was refused with no zone configured: %q", d.Reason)
	}
	// But the valuable list still applies, because that is a statement about a
	// block rather than a guess about where home is.
	if d := p.Allowed(protect.Break, protocol.BlockPos{0, 64, 0}, "minecraft:chest"); d.OK {
		t.Error("a chest was cleared for breaking with the default policy; the built-in valuable list should still hold")
	}
}
