package protect_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/protect"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// --- Zones ---

// TestZoneBoundsAreInclusiveOnEveryFace. A home base drawn from a block
// selection is off by one either way constantly, and the classic symptom is a
// bot that protects the walls and not the floor, or leaves a one-block moat
// around the thing it was told to protect.
func TestZoneBoundsAreInclusiveOnEveryFace(t *testing.T) {
	t.Parallel()

	z := homeZone(protocol.BlockPos{-4, 60, -4}, protocol.BlockPos{4, 70, 4})

	for _, tc := range []struct {
		name string
		pos  protocol.BlockPos
		want bool
	}{
		{"min corner", protocol.BlockPos{-4, 60, -4}, true},
		{"max corner", protocol.BlockPos{4, 70, 4}, true},
		{"interior", protocol.BlockPos{0, 65, 0}, true},
		{"one past min x", protocol.BlockPos{-5, 60, -4}, false},
		{"one past max x", protocol.BlockPos{5, 70, 4}, false},
		{"below the floor", protocol.BlockPos{0, 59, 0}, false},
		{"above the roof", protocol.BlockPos{0, 71, 0}, false},
		{"far away", protocol.BlockPos{1000, 65, 1000}, false},
	} {
		if got := z.Contains(tc.pos); got != tc.want {
			t.Errorf("%s: Contains(%v) = %v, want %v", tc.name, tc.pos, got, tc.want)
		}
	}
}

// TestInvertedZoneBoundsAreNormalised. A config that lists the far corner
// first is a typo, not a request for an empty zone, and silently producing an
// empty zone is the one interpretation of it that nobody wants.
func TestInvertedZoneBoundsAreNormalised(t *testing.T) {
	t.Parallel()

	z := homeZone(protocol.BlockPos{4, 70, 4}, protocol.BlockPos{-4, 60, -4})

	if !z.Contains(protocol.BlockPos{0, 65, 0}) {
		t.Error("a zone with its corners the wrong way round contained nothing")
	}
	if z.Contains(protocol.BlockPos{5, 65, 5}) {
		t.Error("a zone with its corners the wrong way round contained the outside")
	}
}

// TestHomeZoneProtectsBothDirections. HomeZone is the helper that answers
// "what does a normal home zone look like", so both permissions are pinned
// here rather than left to whichever test happens to run first.
func TestHomeZoneProtectsBothDirections(t *testing.T) {
	t.Parallel()

	z := homeZone(protocol.BlockPos{0, 60, 0}, protocol.BlockPos{1, 61, 1})

	if !z.Enabled {
		t.Error("HomeZone returned a disabled zone")
	}
	if !z.Refuses(protect.Break) {
		t.Error("HomeZone does not refuse breaking")
	}
	if !z.Refuses(protect.Build) {
		t.Error("HomeZone does not refuse building")
	}
}

// TestBuildProtectionIsACallerSetField. Whether a zone also protects building is
// a judgement about the player's base, and this package has no way to know it: a
// bot that is allowed to place torches in its own shed has a legitimate reason,
// and hard-coding the answer would refuse it.
func TestBuildProtectionIsACallerSetField(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{Zones: []protect.Zone{
		{Name: "mine", Min: protocol.BlockPos{0, 0, 0}, Max: protocol.BlockPos{9, 9, 9},
			Enabled: true, Permissions: protect.NoBreak},
		{Name: "shed", Min: protocol.BlockPos{100, 0, 0}, Max: protocol.BlockPos{109, 9, 9},
			Enabled: true, Permissions: protect.NoBuild},
		{Name: "notice", Min: protocol.BlockPos{200, 0, 0}, Max: protocol.BlockPos{209, 9, 9},
			Enabled: true},
	}})

	cases := []struct {
		name         string
		pos          protocol.BlockPos
		action       protect.Action
		allowed      bool
		zoneInReason bool
	}{
		{"mine", protocol.BlockPos{4, 4, 4}, protect.Break, false, true},
		{"mine", protocol.BlockPos{4, 4, 4}, protect.Build, true, false},
		{"shed", protocol.BlockPos{104, 4, 4}, protect.Build, false, true},
		{"shed", protocol.BlockPos{104, 4, 4}, protect.Break, true, false},
		{"notice", protocol.BlockPos{204, 4, 4}, protect.Break, true, false},
		{"notice", protocol.BlockPos{204, 4, 4}, protect.Build, true, false},
	}
	for i, tc := range cases {
		got := p.Allowed(tc.action, tc.pos, "dirt")
		if got.OK != tc.allowed {
			t.Errorf("case %d (%s, %s): OK = %v, want %v (%s)", i, tc.name, tc.action, got.OK, tc.allowed, got.Reason)
		}
		if tc.zoneInReason && !strings.Contains(got.Reason, tc.name) {
			t.Errorf("case %d: reason %q does not name zone %q", i, got.Reason, tc.name)
		}
	}
}

// TestDisabledZoneClaimsNothing. The enabled flag exists so a base can be
// parked without deleting its config, which is the difference between "I am
// renovating, do not protect anything" and a comment nobody reads.
func TestDisabledZoneClaimsNothing(t *testing.T) {
	t.Parallel()

	on := protect.New(protect.Config{
		Zones:          []protect.Zone{homeZone(protocol.BlockPos{-4, 60, -4}, protocol.BlockPos{4, 70, 4})},
		ValuableBlocks: []string{},
	})
	off := protect.New(protect.Config{
		Zones: []protect.Zone{{
			Name: "home", Min: protocol.BlockPos{-4, 60, -4}, Max: protocol.BlockPos{4, 70, 4},
			Permissions: protect.NoBreak | protect.NoBuild,
		}},
		ValuableBlocks: []string{},
	})

	if _, inside := on.ZoneAt(protocol.BlockPos{0, 64, 0}); !inside {
		t.Error("an enabled zone did not claim a cell inside it")
	}
	if _, inside := off.ZoneAt(protocol.BlockPos{0, 64, 0}); inside {
		t.Error("a disabled zone claimed a cell inside it")
	}
	if got := off.Allowed(protect.Build, protocol.BlockPos{0, 64, 0}, "cobblestone"); !got.OK {
		t.Errorf("a disabled zone still refused a build: %s", got.Reason)
	}
}

// TestZoneAtReportsTheFirstEnabledZoneContainingTheCell. Overlapping zones are
// normal — a base inside a walled plot inside a city — and the answer has to be
// deterministic, because a policy that picks a different zone depending on map
// iteration order is a policy nobody can debug.
func TestZoneAtReportsTheFirstEnabledZoneContainingTheCell(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{Zones: []protect.Zone{
		homeZone(protocol.BlockPos{-4, 0, -4}, protocol.BlockPos{4, 9, 4}),
		{Name: "inner", Min: protocol.BlockPos{-1, 0, -1}, Max: protocol.BlockPos{1, 9, 1},
			Enabled: true, Permissions: protect.NoBuild},
		{Name: "outer", Min: protocol.BlockPos{-2, 0, -2}, Max: protocol.BlockPos{2, 9, 2},
			Enabled: true, Permissions: protect.NoBreak},
	}})

	z, ok := p.ZoneAt(protocol.BlockPos{0, 4, 0})
	if !ok {
		t.Fatal("no zone claimed a cell inside all three")
	}
	if z.Name != "home" {
		t.Errorf("ZoneAt returned %q, want the first configured zone %q", z.Name, "home")
	}

	if _, ok := p.ZoneAt(protocol.BlockPos{400, 4, 400}); ok {
		t.Error("a cell outside every zone was claimed")
	}
}

// TestEveryFiredZoneIsNamedInTheRefusal. With overlapping zones both the base
// and the city can have something to say about one cell, and reporting only the
// first turns the second into a mystery on the next attempt.
func TestEveryFiredZoneIsNamedInTheRefusal(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{
		Zones: []protect.Zone{
			{Name: "base", Min: protocol.BlockPos{-4, 0, -4}, Max: protocol.BlockPos{4, 9, 4}, Enabled: true, Permissions: protect.NoBreak | protect.NoBuild},
			{Name: "city", Min: protocol.BlockPos{-1, 0, -1}, Max: protocol.BlockPos{1, 9, 1}, Enabled: true, Permissions: protect.NoBreak},
		},
		ValuableBlocks: []string{},
	})

	got := p.Allowed(protect.Break, protocol.BlockPos{0, 4, 0}, "dirt")
	if got.OK {
		t.Fatal("a cell inside a no-break zone was allowed to break")
	}
	if !strings.Contains(got.Reason, "base") || !strings.Contains(got.Reason, "city") {
		t.Errorf("reason %q does not name both zones", got.Reason)
	}
}

// TestAnEmptyZoneListProtectsNoPlace. The other half of the unconfigured
// default: no zones means no spatial protection at all, and it must not degrade
// into a rule that refuses everything.
func TestAnEmptyZoneListProtectsNoPlace(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{Zones: []protect.Zone{}, ValuableBlocks: []string{}})

	for _, pos := range []protocol.BlockPos{
		{0, 64, 0}, {8_000_000, 64, -8_000_000}, {-1, 0, 0},
	} {
		if _, inside := p.ZoneAt(pos); inside {
			t.Errorf("ZoneAt(%v) claimed a cell with no zones configured", pos)
		}
	}
	if got := p.Allowed(protect.Break, protocol.BlockPos{0, 64, 0}, "dirt"); !got.OK {
		t.Errorf("an empty zone list refused a break: %s", got.Reason)
	}
}
