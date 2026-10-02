package pathfinder_test

import (
	"testing"

	"bedrock-ai/internal/bot/pathfinder"
)

// loadedRectQuerier reports cells as decoded only inside a rectangle. Everything
// outside it is "not loaded yet", which is exactly the state a LAN world is in
// while the bot outruns the chunk stream — and the state the world model turns
// into air.
type loadedRectQuerier struct {
	minX, maxX int32
	minZ, maxZ int32
	minY, maxY int32
}

func (q loadedRectQuerier) loaded(x, y, z int32) bool {
	return x >= q.minX && x <= q.maxX &&
		z >= q.minZ && z <= q.maxZ &&
		y >= q.minY && y <= q.maxY
}

// GetBlockRID reports no block identity. This double models air and solid only,
// and "no identity" is the honest answer for it — it also keeps the dragonfly
// palette lookup out of the test, where the converter is not linked in.
func (q loadedRectQuerier) GetBlockRID(x, y, z int32) (uint32, bool) { return 0, false }

func (q loadedRectQuerier) IsBlockAir(x, y, z int32) (bool, bool)   { return true, q.loaded(x, y, z) }
func (q loadedRectQuerier) IsBlockSolid(x, y, z int32) (bool, bool) { return false, q.loaded(x, y, z) }

// worldWithFloors builds a model where the given floor cells are solid and
// everything else is genuinely air.
//
// The bounds matter: without them the world model falls back to "everything
// below y=62 is solid" for cells it has no override and no chunk data for, so a
// test would be measuring that sea-level guess rather than the link it means to
// exercise. Bounds pointing elsewhere make unknown cells read as air, which is
// the honest baseline for a link-validation test.
func worldWithFloors(start, target pathfinder.Node, floors [][3]int32) *pathfinder.LocalWorldModel {
	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(start, target)
	for _, f := range floors {
		model.SetSolid(f[0], f[1], f[2], true)
	}
	return model
}

// TestCanWalkDirectlyRefusesUndecodedGround is the guard for the blind-link bug.
// A string-pulled link is walked without re-checking, so pulling one across
// cells nobody has decoded aims the bot at terrain the model has never seen —
// and it wedges there against a wall that only appears once it arrives.
func TestCanWalkDirectlyRefusesUndecodedGround(t *testing.T) {
	t.Parallel()

	from := pathfinder.Node{X: 0, Y: 1, Z: 0, LinkType: pathfinder.LinkWalk}
	withinDecoded := pathfinder.Node{X: 3, Y: 1, Z: 0, LinkType: pathfinder.LinkWalk}
	intoUnknown := pathfinder.Node{X: 8, Y: 1, Z: 0, LinkType: pathfinder.LinkWalk}

	floors := make([][3]int32, 0, 9)
	for x := int32(0); x <= 8; x++ {
		floors = append(floors, [3]int32{x, 0, 0})
	}

	model := worldWithFloors(from, intoUnknown, floors)
	// Floors are known everywhere (explicit overrides); only the body cells
	// beyond x=3 are still undecoded.
	model.SetChunkQuerier(loadedRectQuerier{minX: 0, maxX: 3, minZ: -8, maxZ: 8, minY: 0, maxY: 96})

	if !pathfinder.CanWalkDirectly(from, withinDecoded, model) {
		t.Error("link inside decoded ground was rejected, want it smoothed")
	}
	if pathfinder.CanWalkDirectly(from, intoUnknown, model) {
		t.Error("link across undecoded ground was accepted, want it refused (blind straight line)")
	}
}

// TestCanWalkDirectlyRequiresFloorOnDescendingLinks covers the other half of the
// same bug: descending links skipped the floor check entirely, so the smoother
// could merge a descent that crosses a hole or a cliff into one long blind
// straight line.
func TestCanWalkDirectlyRequiresFloorOnDescendingLinks(t *testing.T) {
	t.Parallel()

	// A one-block step down: ledge at y=1, ground at y=0 beyond x=0.
	from := pathfinder.Node{X: 0, Y: 2, Z: 0, LinkType: pathfinder.LinkWalk}
	to := pathfinder.Node{X: 2, Y: 1, Z: 0, LinkType: pathfinder.LinkWalk}

	// Same step down, but the ground the link crosses is missing.
	overHole := worldWithFloors(from, to, [][3]int32{{0, 1, 0}, {2, 0, 0}})
	if pathfinder.CanWalkDirectly(from, to, overHole) {
		t.Error("descending link across a hole was accepted, want it refused")
	}

	// The same step down over intact ground must still be smoothed, otherwise the
	// fix would cost every stair a waypoint.
	intact := worldWithFloors(from, to, [][3]int32{{0, 1, 0}, {1, 0, 0}, {2, 0, 0}})
	if !pathfinder.CanWalkDirectly(from, to, intact) {
		t.Error("descending link over intact ground was rejected, want it smoothed")
	}
}

// TestCanWalkDirectlyRequiresFloorOnLevelLinks keeps the floor requirement on
// level links: a hole in the floor must break the link, not be walked over.
func TestCanWalkDirectlyRequiresFloorOnLevelLinks(t *testing.T) {
	t.Parallel()

	from := pathfinder.Node{X: 0, Y: 1, Z: 0, LinkType: pathfinder.LinkWalk}
	to := pathfinder.Node{X: 6, Y: 1, Z: 0, LinkType: pathfinder.LinkWalk}

	var floors [][3]int32
	for x := int32(0); x <= 6; x++ {
		if x == 3 || x == 4 {
			continue // the hole
		}
		floors = append(floors, [3]int32{x, 0, 0})
	}

	holed := worldWithFloors(from, to, floors)
	if pathfinder.CanWalkDirectly(from, to, holed) {
		t.Error("level link over a hole in the floor was accepted, want it refused")
	}
}

// TestCanWalkDirectlyStopsAtAWall keeps the basic case honest: a wall between
// two walkable nodes must still break the link.
func TestCanWalkDirectlyStopsAtAWall(t *testing.T) {
	t.Parallel()

	from := pathfinder.Node{X: 0, Y: 1, Z: 0, LinkType: pathfinder.LinkWalk}
	to := pathfinder.Node{X: 6, Y: 1, Z: 0, LinkType: pathfinder.LinkWalk}

	floors := [][3]int32{{0, 0, 0}, {1, 0, 0}, {2, 0, 0}, {3, 0, 0}, {4, 0, 0}, {5, 0, 0}, {6, 0, 0}}
	model := worldWithFloors(from, to, floors)
	// Two-block wall at x=3.
	model.SetSolid(3, 1, 0, true)
	model.SetSolid(3, 2, 0, true)

	if pathfinder.CanWalkDirectly(from, to, model) {
		t.Error("link through a two-block wall was accepted, want it refused")
	}
}

// TestIsLoadedDistinguishesAirFromUnknown pins the distinction the smoother
// depends on: a cell the chunk cache has decoded is known even when it is air,
// and a cell nobody decoded is not.
func TestIsLoadedDistinguishesAirFromUnknown(t *testing.T) {
	t.Parallel()

	model := pathfinder.NewLocalWorldModel()
	model.SetChunkQuerier(loadedRectQuerier{minX: 0, maxX: 4, minZ: 0, maxZ: 4, minY: 0, maxY: 64})

	if !model.IsLoaded(2, 30, 2) {
		t.Error("decoded air reported as unknown, want known")
	}
	if model.IsLoaded(40, 30, 2) {
		t.Error("undecoded cell reported as known, want unknown")
	}
	// Overrides count as knowledge even where the chunk says nothing.
	model.SetSolid(40, 30, 2, true)
	if !model.IsLoaded(40, 30, 2) {
		t.Error("explicit override reported as unknown, want known")
	}
	// A model with no chunk source has nothing to be uncertain about.
	if !pathfinder.NewLocalWorldModel().IsLoaded(500, 30, 500) {
		t.Error("model without a chunk querier reported unknown, want permissive")
	}
}
