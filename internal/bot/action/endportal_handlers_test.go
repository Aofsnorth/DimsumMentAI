package action

import (
	"reflect"
	"sort"
	"testing"

	"bedrock-ai/internal/bot/dimension"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The End portal is the one portal the bot cannot light, so every decision in
// these handlers is a geometry decision: which loaded frames belong to one
// portal, which of them are still empty, and which cell to stand in to go
// through. The acts on top of those — walk, click, wait — are the parts that
// need a server, and they are kept thin so the policy underneath them can be
// wrong in a test instead of in a stronghold.

// --- registration ---

func TestEndPortalActions_Registered(t *testing.T) {
	t.Parallel()

	fill, ok := actionHandlers["fillframe"]
	if !ok {
		t.Fatal("actionHandlers has no \"fillframe\" entry")
	}
	for _, name := range []string{"fillendportal", "activateendportal", "fill_frame"} {
		alias, ok := actionHandlers[name]
		if !ok {
			t.Errorf("actionHandlers has no %q alias", name)
			continue
		}
		if reflect.ValueOf(alias).Pointer() != reflect.ValueOf(fill).Pointer() {
			t.Errorf("actionHandlers[%q] is a different handler than fillframe", name)
		}
	}
	if _, ok := actionHandlers["enterendportal"]; !ok {
		t.Error("actionHandlers has no \"enterendportal\" entry")
	}
}

// --- routeEndPortalState ---

// TestRouteEndPortalState pins the one split that matters: an end portal frame
// still needs filling, an active end portal only needs walking into, and
// anything else is not this handler's business. Sending the "walk in" path at
// an unlit frame would have the bot stand inside a sealed ring believing it
// travelled to the End.
func TestRouteEndPortalState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state dimension.PortalState
		want  endPortalPlan
	}{
		{dimension.EndPortalFrame, planEndFill},
		{dimension.EndPortalOpen, planEndEnter},
		{dimension.Lit, planEndNotPortal},
		{dimension.NeedsLighting, planEndNotPortal},
		{dimension.NotPortal, planEndNotPortal},
	}
	for _, tc := range cases {
		if got := routeEndPortalState(tc.state); got != tc.want {
			t.Errorf("routeEndPortalState(%v) = %v, want %v", tc.state, got, tc.want)
		}
	}
}

// --- fixtures ---

// endPortalRing builds the canonical stronghold portal — twelve frames forming a
// 3-wide, 5-tall ring, with a 1x3 interior — in the plane named by dir: "x"
// stands it up in the XY plane, "z" stands it up in the ZY plane, and "y" lays
// it flat in the XZ plane. Strongholds generate all three, so a handler that
// hardcodes the Overworld layout would fill ten of twelve frames on two out of
// three worlds.
func endPortalRing(origin [3]int32, dir string) map[[3]int32]string {
	at := func(across, up int32) [3]int32 {
		switch dir {
		case "z":
			return [3]int32{origin[0], origin[1] + up, origin[2] + across}
		case "y":
			return [3]int32{origin[0] + across, origin[1], origin[2] + up}
		default:
			return [3]int32{origin[0] + across, origin[1] + up, origin[2]}
		}
	}

	cells := make(map[[3]int32]string)
	for across := int32(0); across < 3; across++ {
		cells[at(across, 0)] = "minecraft:end_portal_frame"
		cells[at(across, 4)] = "minecraft:end_portal_frame"
	}
	for up := int32(1); up <= 3; up++ {
		cells[at(0, up)] = "minecraft:end_portal_frame"
		cells[at(2, up)] = "minecraft:end_portal_frame"
		cells[at(1, up)] = "minecraft:air"
	}
	return cells
}

// activatedEndPortal returns a portal whose frames have all been replaced by
// end_portal blocks, which is what a filled portal actually reads as: the frame
// cells become the portal cells, and the interior fills in behind them.
func activatedEndPortal(origin [3]int32, dir string) map[[3]int32]string {
	cells := endPortalRing(origin, dir)
	for key := range cells {
		cells[key] = "minecraft:end_portal"
	}
	return cells
}

// filledPositions returns the first want frame cells of a ring, promoted to
// end_portal so they read as already filled.
func filledPositions(cells map[[3]int32]string, want int) map[[3]int32]bool {
	keys := make([][3]int32, 0, len(cells))
	for key, name := range cells {
		if name == "minecraft:end_portal_frame" {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		return posLess(
			protocol.BlockPos{keys[i][0], keys[i][1], keys[i][2]},
			protocol.BlockPos{keys[j][0], keys[j][1], keys[j][2]})
	})

	filled := make(map[[3]int32]bool, want)
	for _, key := range keys[:want] {
		cells[key] = "minecraft:end_portal"
		filled[key] = true
	}
	return filled
}

// ringFrames returns only the twelve frame cells of a ring, which is what the
// handler's world-cache lookup can ever produce: it does not report air, so the
// hole inside the ring never reaches the geometry helpers.
func ringFrames(origin [3]int32, dir string) []protocol.BlockPos {
	cells := endPortalRing(origin, dir)
	frames := make([]protocol.BlockPos, 0, 12)
	for key, name := range cells {
		if name == "minecraft:end_portal_frame" {
			frames = append(frames, protocol.BlockPos{key[0], key[1], key[2]})
		}
	}
	sort.Slice(frames, func(i, j int) bool { return posLess(frames[i], frames[j]) })
	return frames
}

// cellsOf turns a fixture map into the position list the pure geometry helpers
// take, in a deterministic order.
func cellsOf(cells map[[3]int32]string) []protocol.BlockPos {
	pos := make([]protocol.BlockPos, 0, len(cells))
	for key := range cells {
		pos = append(pos, protocol.BlockPos{key[0], key[1], key[2]})
	}
	sort.Slice(pos, func(i, j int) bool { return posLess(pos[i], pos[j]) })
	return pos
}

// --- scanEndPortal ---

// TestScanEndPortalFindsEveryFrameInAnyOrientation is the orientation contract:
// the whole ring must come back whichever way it faces, because the bot walks
// in on one frame and has to click the other eleven.
func TestScanEndPortalFindsEveryFrameInAnyOrientation(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"x", "z", "y"} {
		dir := dir
		t.Run(dir, func(t *testing.T) {
			t.Parallel()

			cells := endPortalRing([3]int32{100, 64, -20}, dir)
			// Seed from an arbitrary frame, the way findNearestBlock would hand
			// one over: the bot has no idea it is the "top left" one.
			var seed protocol.BlockPos
			for key := range cells {
				if cells[key] == "minecraft:end_portal_frame" {
					seed = protocol.BlockPos{key[0], key[1], key[2]}
					break
				}
			}

			scanned := scanEndPortal(mapLookup(cells), seed, endPortalScanRadius, -6, 6)
			rect, frames, open := portalParts(scanned)
			if len(frames) != 12 {
				t.Errorf("orientation %q: found %d frames, want 12", dir, len(frames))
			}
			if len(rect) != 12 {
				t.Errorf("orientation %q: rect has %d cells, want 12", dir, len(rect))
			}
			if len(open) != 0 {
				t.Errorf("orientation %q: unactivated portal reported %d open cells, want 0", dir, len(open))
			}
			if dimension.Classify(cellNames(scanned, rect)) != dimension.EndPortalFrame {
				t.Errorf("orientation %q: classified as %v, want EndPortalFrame",
					dir, dimension.Classify(cellNames(scanned, rect)))
			}
		})
	}
}

// TestScanEndPortalIgnoresUnrelatedBlocks keeps the scan narrow: a Nether portal
// or a chiseled brick in the same room is not an end portal, and treating it as
// one would send the bot off filling frames that do not exist.
func TestScanEndPortalIgnoresUnrelatedBlocks(t *testing.T) {
	t.Parallel()

	cells := map[[3]int32]string{
		{10, 64, 10}: "minecraft:obsidian",
		{10, 65, 10}: "minecraft:nether_portal",
		{11, 64, 10}: "minecraft:chiseled_stone_bricks",
		{12, 64, 10}: "minecraft:end_portal_frame",
	}

	scanned := scanEndPortal(mapLookup(cells), protocol.BlockPos{10, 64, 10}, 4, -2, 2)
	if len(scanned) != 1 {
		t.Errorf("scan collected %d cells, want only the single end_portal_frame: %v", len(scanned), scanned)
	}
	rect, frames, _ := portalParts(scanned)
	if len(frames) != 1 || len(rect) != 1 {
		t.Errorf("portalParts = %d frames / %d rect, want 1 / 1", len(frames), len(rect))
	}
}

// TestScanEndPortalSeesNothingOutsideItsBox bounds the work: a portal room
// three rooms away must not be dragged into this one, or every ruin on the
// planet would collapse into a single ring.
func TestScanEndPortalSeesNothingOutsideItsBox(t *testing.T) {
	t.Parallel()

	cells := endPortalRing([3]int32{0, 64, 0}, "x")
	cells[[3]int32{40, 64, 40}] = "minecraft:end_portal_frame"

	scanned := scanEndPortal(mapLookup(cells), protocol.BlockPos{1, 64, 0}, 6, -6, 6)
	rect, _, _ := portalParts(scanned)
	if len(rect) != 12 {
		t.Errorf("rect has %d cells, want 12 — the distant stray frame was absorbed", len(rect))
	}
}

// --- portalParts ---

// TestPortalPartsSplitsFilledFromEmpty is the accounting the report depends on:
// a frame that has already been filled reads as an end_portal block, and
// counting it as still-empty would send the bot to spend an eye of ender on a
// cell that cannot take one.
func TestPortalPartsSplitsFilledFromEmpty(t *testing.T) {
	t.Parallel()

	cells := endPortalRing([3]int32{0, 64, 0}, "x")
	filledPositions(cells, 5)

	scanned := scanEndPortal(mapLookup(cells), protocol.BlockPos{0, 64, 0}, endPortalScanRadius, -6, 6)
	rect, frames, open := portalParts(scanned)
	if len(rect) != 12 {
		t.Errorf("rect = %d cells, want 12 (12 frames, interior is air)", len(rect))
	}
	if len(frames) != 7 {
		t.Errorf("empty frames = %d, want 7", len(frames))
	}
	if len(open) != 5 {
		t.Errorf("filled frames = %d, want 5", len(open))
	}
}

// TestPortalPartsOnActivatedPortal checks the other end state: once the portal
// is active every cell — frames and interior alike — is an end_portal block, so
// there is nothing left to fill and the whole ring becomes the walk target.
func TestPortalPartsOnActivatedPortal(t *testing.T) {
	t.Parallel()

	cells := activatedEndPortal([3]int32{0, 64, 0}, "x")
	scanned := scanEndPortal(mapLookup(cells), protocol.BlockPos{1, 66, 0}, endPortalScanRadius, -6, 6)
	rect, frames, open := portalParts(scanned)
	if len(rect) != 15 {
		t.Errorf("rect = %d cells, want 15 (12 frames plus a 1x3 interior)", len(rect))
	}
	if len(frames) != 0 {
		t.Errorf("activated portal still reports %d empty frames", len(frames))
	}
	if len(open) != 15 {
		t.Errorf("open cells = %d, want 15", len(open))
	}
	if got := dimension.Classify(cellNames(scanned, rect)); got != dimension.EndPortalOpen {
		t.Errorf("Classify(activated) = %v, want EndPortalOpen", got)
	}
}

// TestPortalPartsWithNoFrames: nothing found means nothing to do, and the
// handler must be able to say so rather than clicking at an empty room.
func TestPortalPartsWithNoFrames(t *testing.T) {
	t.Parallel()

	rect, frames, open := portalParts(scanEndPortal(mapLookup(nil), protocol.BlockPos{}, 4, -2, 2))
	if len(rect) != 0 || len(frames) != 0 || len(open) != 0 {
		t.Errorf("portalParts on an empty scan = %v/%v/%v, want all empty", rect, frames, open)
	}
}

// --- largestFrameCluster ---

// TestLargestFrameClusterPicksTheBiggestGroup: a stronghold can hold a ruined
// portal and scattered remains, and the ring is the group worth filling. Picking
// the first group found instead would depend on scan order.
func TestLargestFrameClusterPicksTheBiggestGroup(t *testing.T) {
	t.Parallel()

	cells := endPortalRing([3]int32{0, 64, 0}, "x")
	cells[[3]int32{40, 70, 40}] = "minecraft:end_portal_frame"
	cells[[3]int32{41, 70, 40}] = "minecraft:end_portal_frame"
	cells[[3]int32{42, 70, 40}] = "minecraft:end_portal_frame"

	rect, frames, _ := portalParts(scanEndPortal(mapLookup(cells), protocol.BlockPos{0, 64, 0}, 60, -8, 8))
	if len(rect) != 12 {
		t.Fatalf("rect = %d cells, want the 12-cell ring, got %v", len(rect), rect)
	}
	if len(frames) != 12 {
		t.Errorf("frames = %d, want 12", len(frames))
	}
	for _, p := range rect {
		if abs32(int(p.X())) > 5 {
			t.Errorf("cell %v is from the stray three, not the ring", p)
		}
	}
}

// TestLargestFrameClusterChainsThroughGaps covers the ruined case: frames are
// missing, but the survivors are still within a portal's width of each other,
// so they must land in one cluster rather than three singletons the bot would
// treat as three unrelated ruins.
func TestLargestFrameClusterChainsThroughGaps(t *testing.T) {
	t.Parallel()

	frames := []protocol.BlockPos{
		{0, 64, 0}, {2, 64, 0},
		{0, 66, 0}, {2, 66, 0},
		{0, 68, 0},
	}
	if got := largestFrameCluster(frames); len(got) != 5 {
		t.Errorf("largestFrameCluster = %d cells, want 5 chained survivors: %v", len(got), got)
	}
	if got := largestFrameCluster(nil); got != nil {
		t.Errorf("largestFrameCluster(nil) = %v, want nil", got)
	}
}

// --- endPortalInteriorCell ---

// TestEndPortalInteriorCellLandsInsideTheRing is the walk target. The middle of
// the ring is walled on all six sides by the structure; a wall cell is not, and
// standing on one of those is standing next to the portal rather than in it.
func TestEndPortalInteriorCellLandsInsideTheRing(t *testing.T) {
	t.Parallel()

	origin := [3]int32{0, 64, 0}
	rect := cellsOf(activatedEndPortal(origin, "x"))
	cell, ok := endPortalInteriorCell(rect, rect, mgl32.Vec3{1.5, 64, 3})
	if !ok {
		t.Fatal("endPortalInteriorCell found no interior cell in an intact ring")
	}
	// The standard ring is three across with a one-by-three column inside it.
	if cell.X() != origin[0]+1 {
		t.Errorf("interior cell %v is not the middle column of the ring", cell)
	}
	if cell.Y() < origin[1]+1 || cell.Y() > origin[1]+3 {
		t.Errorf("interior cell %v is outside the ring's own hole", cell)
	}
}

// TestEndPortalInteriorCellPrefersTheCellTheBotCanReach breaks the tie between
// the ring's three interior cells by proximity: a bot standing at the bottom of
// the frame should be sent to the bottom of the hole, not the top.
func TestEndPortalInteriorCellPrefersTheCellTheBotCanReach(t *testing.T) {
	t.Parallel()

	rect := cellsOf(activatedEndPortal([3]int32{0, 64, 0}, "x"))
	cell, ok := endPortalInteriorCell(rect, rect, mgl32.Vec3{1.5, 64, 3})
	if !ok {
		t.Fatal("no interior cell selected")
	}
	if cell != (protocol.BlockPos{1, 65, 0}) {
		t.Errorf("interior cell = %v, want the lowest interior cell {1,65,0} next to the bot", cell)
	}
}

// TestEndPortalInteriorCellPrefersTheFilledPart: on a ring the world cache can
// only give as frames, every cell is equally enclosed and the choice falls to
// the tie-break. Only a filled cell is a way through, so the bot has to be sent
// there even when it is standing at the far end of the ring.
func TestEndPortalInteriorCellPrefersTheFilledPart(t *testing.T) {
	t.Parallel()

	// Frames only — which is all the handler ever sees, because the world-cache
	// lookup it uses does not report air.
	ring := ringFrames([3]int32{0, 64, 0}, "x")
	// One filled frame, at the top of the ring, furthest from the bot.
	open := []protocol.BlockPos{{1, 68, 0}}

	cell, ok := endPortalInteriorCell(ring, open, mgl32.Vec3{1.5, 64, 3})
	if !ok {
		t.Fatal("no interior cell selected")
	}
	if cell != (protocol.BlockPos{1, 68, 0}) {
		t.Errorf("interior cell = %v, want the filled frame {1,68,0} rather than the nearer empty one", cell)
	}
}

// TestEndPortalInteriorCellIgnoresOutsiders guards against a stray frame across
// the room being read as part of the portal: it touches one cell where the
// middle of the ring touches four.
func TestEndPortalInteriorCellIgnoresOutsiders(t *testing.T) {
	t.Parallel()

	ring := cellsOf(activatedEndPortal([3]int32{0, 64, 0}, "x"))
	ring = append(ring, protocol.BlockPos{3, 67, 0})
	cell, ok := endPortalInteriorCell(ring, ring, mgl32.Vec3{1.5, 64, 3})
	if !ok {
		t.Fatal("no interior cell selected")
	}
	if cell.X() > 2 {
		t.Errorf("interior cell %v sits outside the ring itself", cell)
	}
}

// TestEndPortalInteriorCellWithoutFrames: no structure, no interior. Inventing
// one would walk the bot into a wall.
func TestEndPortalInteriorCellWithoutFrames(t *testing.T) {
	t.Parallel()

	if _, ok := endPortalInteriorCell(nil, nil, mgl32.Vec3{}); ok {
		t.Error("endPortalInteriorCell invented an interior for an empty portal")
	}
}

// --- fill planning ---

// TestEmptyFrameCellsSkipsAlreadyFilled is the honesty rule for the whole
// action: a filled frame is one the bot did not fill and must not claim credit
// for filling.
func TestEmptyFrameCellsSkipsAlreadyFilled(t *testing.T) {
	t.Parallel()

	frames := []protocol.BlockPos{{0, 64, 0}, {1, 64, 0}, {2, 64, 0}, {0, 66, 0}}
	open := []protocol.BlockPos{{2, 64, 0}}

	empty := emptyFrameCells(frames, open)
	if len(empty) != 3 {
		t.Fatalf("emptyFrameCells = %v (%d cells), want 3", empty, len(empty))
	}
	for _, p := range empty {
		if p == (protocol.BlockPos{2, 64, 0}) {
			t.Error("a frame that already reads end_portal was queued for filling")
		}
	}
}

// TestEmptyFrameCellsOrdersBottomFirst: the ring is five blocks tall, and the
// bottom row is the part the bot can reach from the floor. Filling in that
// order means running short on eyes of ender costs the top of the ring, which
// is the half it could not have clicked anyway.
func TestEmptyFrameCellsOrdersBottomFirst(t *testing.T) {
	t.Parallel()

	frames := []protocol.BlockPos{
		{1, 68, 0}, {0, 64, 0}, {2, 68, 0}, {1, 64, 0}, {0, 66, 0},
	}
	empty := emptyFrameCells(frames, nil)
	for i := 1; i < len(empty); i++ {
		if empty[i].Y() < empty[i-1].Y() {
			t.Errorf("emptyFrameCells not ordered bottom-first: %v", empty)
			break
		}
	}
	if len(empty) != 5 {
		t.Errorf("emptyFrameCells returned %d cells, want 5", len(empty))
	}
}

// TestFramesNeedingEyesCapsAtSupply: the bot must never queue more clicks than
// it has items for. Each queued frame is a UseItem that consumes an eye, and a
// twelfth empty-handed click is a lie waiting to be told.
func TestFramesNeedingEyesCapsAtSupply(t *testing.T) {
	t.Parallel()

	frames := []protocol.BlockPos{
		{0, 64, 0}, {1, 64, 0}, {2, 64, 0},
		{0, 65, 0}, {1, 65, 0}, {2, 65, 0},
	}
	if got := framesNeedingEyes(frames, nil, 4); len(got) != 4 {
		t.Errorf("framesNeedingEyes(_, 4) = %d cells, want 4", len(got))
	}
	if got := framesNeedingEyes(frames, nil, 100); len(got) != 6 {
		t.Errorf("framesNeedingEyes(_, 100) = %d cells, want all 6", len(got))
	}
	if got := framesNeedingEyes(frames, nil, 0); got != nil {
		t.Errorf("framesNeedingEyes(_, 0) = %v, want nil", got)
	}
	if got := framesNeedingEyes(nil, nil, 12); got != nil {
		t.Errorf("framesNeedingEyes(nil, nil, 12) = %v, want nil", got)
	}
}

// TestFramesNeedingEyesOnActivePortalIsEmpty: a portal that is already open has
// nothing left to spend an eye on, and clicking it would be the bot poking a
// working portal for no reason.
func TestFramesNeedingEyesOnActivePortalIsEmpty(t *testing.T) {
	t.Parallel()

	open := cellsOf(activatedEndPortal([3]int32{0, 64, 0}, "x"))
	if got := framesNeedingEyes(nil, open, 12); got != nil {
		t.Errorf("framesNeedingEyes on an active portal = %v, want nil", got)
	}
}

// --- item matching ---

func TestIsEyeOfEnder(t *testing.T) {
	t.Parallel()

	yes := []string{"eye_of_ender", "minecraft:eye_of_ender", "EYE_OF_ENDER", "custom:eye_of_ender"}
	for _, name := range yes {
		if !isEyeOfEnder(name) {
			t.Errorf("isEyeOfEnder(%q) = false, want true", name)
		}
	}
	// Substring matching is deliberate — servers namespace items and behaviour
	// packs invent their own prefixes — so these are the near misses that must
	// not sneak through, not invented variants of the real name.
	no := []string{"ender_eye", "ender_pearl", "glass_bottle", "ender_chest", ""}
	for _, name := range no {
		if isEyeOfEnder(name) {
			t.Errorf("isEyeOfEnder(%q) = true, want false", name)
		}
	}
}

// --- travel confirmation ---

// TestDimensionChanged is the rule that keeps "I went to the End" honest: the
// terrain has to say something new, and "I still cannot tell" is not the same
// answer as "I arrived".
func TestDimensionChanged(t *testing.T) {
	t.Parallel()

	cases := []struct {
		before, after dimension.Dimension
		want          bool
	}{
		{dimension.Unknown, dimension.TheEnd, true},
		{dimension.Overworld, dimension.TheEnd, true},
		{dimension.Overworld, dimension.Nether, true},
		{dimension.Overworld, dimension.Overworld, false},
		{dimension.Unknown, dimension.Unknown, false},
		{dimension.Overworld, dimension.Unknown, false},
	}
	for _, tc := range cases {
		if got := dimensionChanged(tc.before, tc.after); got != tc.want {
			t.Errorf("dimensionChanged(%v, %v) = %v, want %v", tc.before, tc.after, got, tc.want)
		}
	}
}

// TestVisibleDimensionReadsTheTerrainThroughTheClassifier closes the loop from
// "what the bot can see" to "where it is", through the same classifier the rest
// of the bot uses. The only thing tested here is that the plumbing hands the
// names over — an empty view is Unknown, never a guess.
func TestVisibleDimensionReadsTheTerrainThroughTheClassifier(t *testing.T) {
	t.Parallel()

	endTerrain := []string{
		"minecraft:end_stone", "minecraft:end_stone", "minecraft:end_stone",
		"minecraft:end_stone", "minecraft:end_stone", "minecraft:chorus_flower",
	}
	if got := dimension.Detect(endTerrain); got != dimension.TheEnd {
		t.Errorf("Detect(end stone) = %v, want TheEnd", got)
	}
	if got := dimension.Detect([]string{"minecraft:none"}); got != dimension.Unknown {
		t.Errorf("Detect(nothing visible) = %v, want Unknown", got)
	}
}
