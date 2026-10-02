package action_test

import (
	"bedrock-ai/internal/bot/action"
	"testing"

	"bedrock-ai/internal/bot/dimension"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The lightportal handler ends in packets and navigation, but every decision
// it makes — which route a classification implies, and which cell the flint
// and steel is aimed at — is pure. These tests pin those decisions so a
// change that would make the bot click the wrong cell, or walk into an empty
// frame believing it lit, fails here rather than in a live world.

// TestRoutePortalStateMapsClassificationToAction pins the routing contract:
// Lit means walk in without lighting, NeedsLighting means ignite, end portals
// are a different job, and anything else is not a portal at all. A bot that
// tried to light an already-lit portal, or applied flint to end frames, would
// spend its whole timeout standing still.
func TestRoutePortalStateMapsClassificationToAction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state dimension.PortalState
		want  action.PortalPlan
	}{
		{dimension.Lit, action.PlanEnterLit},
		{dimension.NeedsLighting, action.PlanLight},
		{dimension.EndPortalFrame, action.PlanEndPortal},
		{dimension.EndPortalOpen, action.PlanEndPortal},
		{dimension.NotPortal, action.PlanNotPortal},
	}
	for _, tc := range cases {
		if got := action.RoutePortalState(tc.state); got != tc.want {
			t.Errorf("RoutePortalState(%v) = %v, want %v", tc.state, got, tc.want)
		}
	}
}

// TestClassificationRoutesTheFrameCases exercises the routing against real
// block-name sets the way dimension.Classify sees them, closing the loop from
// "what the world looks like" to "what the bot does".
func TestClassificationRoutesTheFrameCases(t *testing.T) {
	t.Parallel()

	lit := []string{"minecraft:obsidian", "minecraft:obsidian", "minecraft:portal", "minecraft:obsidian"}
	if got := action.RoutePortalState(dimension.Classify(lit)); got != action.PlanEnterLit {
		t.Errorf("lit frame routed to %v, want PlanEnterLit", got)
	}

	unlit := []string{"minecraft:obsidian", "minecraft:obsidian", "minecraft:air", "minecraft:obsidian"}
	if got := action.RoutePortalState(dimension.Classify(unlit)); got != action.PlanLight {
		t.Errorf("unlit frame routed to %v, want PlanLight", got)
	}

	frames := []string{"minecraft:end_portal_frame", "minecraft:end_portal_frame", "minecraft:air"}
	if got := action.RoutePortalState(dimension.Classify(frames)); got != action.PlanEndPortal {
		t.Errorf("end frames routed to %v, want PlanEndPortal", got)
	}

	ground := []string{"minecraft:stone", "minecraft:dirt", "minecraft:air"}
	if got := action.RoutePortalState(dimension.Classify(ground)); got != action.PlanNotPortal {
		t.Errorf("ordinary ground routed to %v, want PlanNotPortal", got)
	}
}

// mapLookup builds a BlockLookup over a fixed cell map. Cells absent from the
// map resolve to not-ok, exactly like unloaded cells in the world cache.
func mapLookup(cells map[[3]int32]string) action.BlockLookup {
	return func(x, y, z int32) (string, bool) {
		name, ok := cells[[3]int32{x, y, z}]
		return name, ok
	}
}

// standardFrame builds a classic 4-wide, 5-tall portal frame standing along
// the X axis at z=0, its 2x3 interior filled with air:
//
//	y=4: O O O O
//	y=3: O A A O
//	y=2: O A A O
//	y=1: O A A O
//	y=0: O O O O
func standardFrame() map[[3]int32]string {
	cells := make(map[[3]int32]string)
	for x := int32(0); x <= 3; x++ {
		cells[[3]int32{x, 0, 0}] = "obsidian"
		cells[[3]int32{x, 4, 0}] = "obsidian"
	}
	for y := int32(1); y <= 3; y++ {
		cells[[3]int32{0, y, 0}] = "obsidian"
		cells[[3]int32{3, y, 0}] = "obsidian"
		cells[[3]int32{1, y, 0}] = "air"
		cells[[3]int32{2, y, 0}] = "air"
	}
	return cells
}

func frameObsidianAndAir(cells map[[3]int32]string) (obsidian, air []protocol.BlockPos) {
	for p, name := range cells {
		switch name {
		case "obsidian":
			obsidian = append(obsidian, protocol.BlockPos{p[0], p[1], p[2]})
		case "air":
			air = append(air, protocol.BlockPos{p[0], p[1], p[2]})
		}
	}
	return obsidian, air
}

// TestSelectIgnitionTargetPicksBottomInteriorAir is the lighting rule: the
// flint click goes to an interior air cell touching the frame, on the lowest
// row, so the bot can reach it standing at ground level beside the frame.
func TestSelectIgnitionTargetPicksBottomInteriorAir(t *testing.T) {
	t.Parallel()

	cells := standardFrame()
	obsidian, air := frameObsidianAndAir(cells)
	botPos := mgl32.Vec3{1.5, 0, 3}

	target, ok := action.SelectIgnitionTarget(obsidian, air, mapLookup(cells), botPos)
	if !ok {
		t.Fatal("SelectIgnitionTarget found no target for a valid unlit frame")
	}
	if target.Y() != 1 {
		t.Errorf("ignition target Y = %d, want 1 (bottom interior row)", target.Y())
	}
	if target.Z() != 0 || target.X() < 1 || target.X() > 2 {
		t.Errorf("ignition target %v is not an interior cell of the frame", target)
	}
	if got := cells[[3]int32{target.X(), target.Y(), target.Z()}]; got != "air" {
		t.Errorf("ignition target cell holds %q, want air", got)
	}
}

// TestSelectIgnitionTargetTouchesTheFrame guards against picking a floating
// air cell that happens to be inside the scan window but is nowhere near the
// frame — a click there lights nothing and the bot would still wait for an
// ignition that cannot happen.
func TestSelectIgnitionTargetTouchesTheFrame(t *testing.T) {
	t.Parallel()

	cells := standardFrame()
	// Distant air that the scan window would include but the frame does not
	// touch; it must never win over a real interior cell.
	cells[[3]int32{-3, 1, -3}] = "air"
	obsidian, air := frameObsidianAndAir(cells)

	target, ok := action.SelectIgnitionTarget(obsidian, air, mapLookup(cells), mgl32.Vec3{1.5, 0, 3})
	if !ok {
		t.Fatal("no target selected")
	}
	if target == (protocol.BlockPos{-3, 1, -3}) {
		t.Error("selected distant floating air instead of an interior cell touching the frame")
	}
}

// TestSelectIgnitionTargetFallsBackToLowestObsidian covers the unloaded-interior
// case: with no confirmed air cell, the click goes to the frame's lowest
// obsidian, which vanilla also accepts for ignition. Picking an unloaded air
// cell instead would send a click the host cannot place.
func TestSelectIgnitionTargetFallsBackToLowestObsidian(t *testing.T) {
	t.Parallel()

	obsidian := []protocol.BlockPos{
		{10, 66, 10}, {11, 66, 10}, {10, 67, 10}, {11, 69, 10},
	}
	target, ok := action.SelectIgnitionTarget(obsidian, nil, mapLookup(nil), mgl32.Vec3{10.5, 65, 12})
	if !ok {
		t.Fatal("no target selected despite having obsidian")
	}
	if target != (protocol.BlockPos{10, 66, 10}) {
		t.Errorf("fallback target = %v, want lowest obsidian {10,66,10}", target)
	}
}

// TestSelectIgnitionTargetSkipsAirTheLookupNoLongerConfirms guards the stale-
// scan case: the air list came from an earlier scan, but if the cell is stone
// now (a block placed in between), clicking it would be a silent miss.
func TestSelectIgnitionTargetSkipsAirTheLookupNoLongerConfirms(t *testing.T) {
	t.Parallel()

	cells := standardFrame()
	// The bottom row was filled in after the scan that produced the air list.
	cells[[3]int32{1, 1, 0}] = "stone"
	cells[[3]int32{2, 1, 0}] = "stone"
	obsidian, air := frameObsidianAndAir(standardFrame())

	target, ok := action.SelectIgnitionTarget(obsidian, air, mapLookup(cells), mgl32.Vec3{1.5, 0, 3})
	if !ok {
		t.Fatal("no target selected")
	}
	if target.Y() == 1 {
		t.Errorf("target Y = 1, but both bottom interior cells are stone now; want a higher air cell, got %v", target)
	}
	if got := cells[[3]int32{target.X(), target.Y(), target.Z()}]; got != "air" {
		t.Errorf("target cell holds %q, want air", got)
	}
}

// TestSelectIgnitionTargetWithNoObsidian: no frame, no target. The handler
// must fail with a clear message rather than clicking somewhere arbitrary.
func TestSelectIgnitionTargetWithNoObsidian(t *testing.T) {
	t.Parallel()

	if _, ok := action.SelectIgnitionTarget(nil, nil, mapLookup(nil), mgl32.Vec3{}); ok {
		t.Error("SelectIgnitionTarget returned a target with no obsidian at all")
	}
}

// TestPortalScanCollectsNamesObsidianAndAir checks the scan feeds Classify
// the right evidence and records cell positions: air cells are ignition
// candidates, obsidian cells define the frame, and unloaded cells contribute
// nothing instead of masquerading as evidence.
func TestPortalScanCollectsNamesObsidianAndAir(t *testing.T) {
	t.Parallel()

	cells := standardFrame()
	names, obsidian, air := action.PortalScan(mapLookup(cells), protocol.BlockPos{1, 0, 0}, -1, 5)

	if len(obsidian) != 14 {
		t.Errorf("obsidian cells = %d, want 14", len(obsidian))
	}
	if len(air) != 6 {
		t.Errorf("air cells = %d, want 6", len(air))
	}
	if dimension.Classify(names) != dimension.NeedsLighting {
		t.Errorf("Classify(scan) = %v, want NeedsLighting", dimension.Classify(names))
	}
}

// TestPortalInteriorCellPrefersAir: once a portal is lit, the walk target is
// an interior cell — stepping onto the frame itself only works by luck of
// collision, while the interior cell is what actually triggers the transfer.
func TestPortalInteriorCellPrefersAir(t *testing.T) {
	t.Parallel()

	air := []protocol.BlockPos{{5, 66, 5}, {5, 67, 5}}
	seed := protocol.BlockPos{4, 65, 5}
	if got := action.PortalInteriorCell(air, seed); got != air[0] {
		t.Errorf("PortalInteriorCell = %v, want first air cell %v", got, air[0])
	}
	if got := action.PortalInteriorCell(nil, seed); got != seed {
		t.Errorf("PortalInteriorCell with no air = %v, want seed %v", got, seed)
	}
}
