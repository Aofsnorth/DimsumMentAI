package pathfinder_test

import (
	"testing"

	"bedrock-ai/internal/bot/pathfinder"
)

// stairWorld builds a straight staircase climbing west from flat ground:
// ground floor at y=70, then one stair block per block of run, each one cell
// higher, ending on a platform whose floor is at y=73.
//
// This is the shape the live hub has in front of the bot: the route up is a
// staircase, and the report from the server was that the bot walked around it
// instead of climbing it. If A* cannot produce a route here, the staircase is
// invisible to the search and every climb on the hub dead-ends one block short.
func stairWorld(t *testing.T, start, target pathfinder.Node, platformWall bool) (*pathfinder.LocalWorldModel, *namedQuerier) {
	t.Helper()
	q := newNamedQuerier()
	// Flat ground the bot starts on.
	for x := int32(24); x <= 27; x++ {
		q.set(x, 70, 0, "minecraft:stone")
	}
	// The staircase: each step one cell higher than the last.
	q.set(23, 70, 0, "minecraft:stone_brick_stairs")
	q.set(22, 71, 0, "minecraft:stone_brick_stairs")
	q.set(21, 72, 0, "minecraft:stone_brick_stairs")
	// Platform floor at the top of the stairs.
	platform := "minecraft:stone"
	if platformWall {
		platform = "minecraft:tuff_brick_wall"
	}
	for x := int32(18); x <= 20; x++ {
		q.set(x, 73, 0, platform)
	}
	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(start, target)
	model.SetChunkQuerier(q)
	return model, q
}

// TestAStaircaseIsAClimbableRoute is the direct answer to "the bot does not
// know stairs". A* must reach the platform by way of the stair cells rather
// than reporting no route.
func TestAStaircaseIsAClimbableRoute(t *testing.T) {
	t.Parallel()

	start := pathfinder.Node{X: 25, Y: 71, Z: 0}
	target := pathfinder.Node{X: 19, Y: 74, Z: 0}

	model, _ := stairWorld(t, start, target, false)
	path := pathfinder.FindPath(start, target, model, false)
	if len(path) == 0 {
		t.Fatalf("A* found no route up a straight staircase; start neighbours: %+v", model.GetNeighbors(start))
	}
	last := path[len(path)-1]
	if last.X != target.X || last.Y != target.Y || last.Z != target.Z {
		t.Fatalf("route stops short at %+v, want %+v (full: %+v)", last, target, path)
	}
	// The route must actually use the stair cells, not tunnel around them.
	usedStairs := false
	for _, n := range path {
		if n.X == 22 && n.Y == 72 || n.X == 21 && n.Y == 73 {
			usedStairs = true
		}
	}
	if !usedStairs {
		t.Errorf("route reached the platform without stepping on the staircase: %+v", path)
	}
}

// TestAStaircaseOntoAWallPavedPlatform is the live geometry: the hub's upper
// platform is paved in wall tiles, so the last step of the climb lands on a
// wall top one node above the stair the bot stands on.
func TestAStaircaseOntoAWallPavedPlatform(t *testing.T) {
	t.Parallel()

	start := pathfinder.Node{X: 25, Y: 71, Z: 0}
	target := pathfinder.Node{X: 19, Y: 74, Z: 0}

	model, _ := stairWorld(t, start, target, true)
	path := pathfinder.FindPath(start, target, model, false)
	if len(path) == 0 {
		t.Fatalf("A* found no route; start neighbours: %+v", model.GetNeighbors(start))
	}
	last := path[len(path)-1]
	if last.X != target.X || last.Y != target.Y || last.Z != target.Z {
		t.Fatalf("route stops short at %+v, want %+v (full: %+v)", last, target, path)
	}
}

// TestAStaircaseIsPreferredOverDetour verifies that A* prefers walking up a short
// staircase instead of taking a flat detour around the side.
func TestAStaircaseIsPreferredOverDetour(t *testing.T) {
	t.Parallel()

	start := pathfinder.Node{X: 25, Y: 71, Z: 0}
	target := pathfinder.Node{X: 19, Y: 74, Z: 0}

	model, q := stairWorld(t, start, target, true)
	// Add a flat detour at z=2 that also eventually ramps up gently.
	// The direct staircase at z=0 has 3 stairs.
	// The detour at z=2 goes around, meaning more steps.
	for x := int32(25); x >= 18; x-- {
		for z := int32(1); z <= 3; z++ {
			q.set(x, 70, z, "minecraft:stone")
		}
	}
	// Detour ramps up at x=18
	q.set(18, 71, 2, "minecraft:stone")
	q.set(18, 72, 1, "minecraft:stone")

	path := pathfinder.FindPath(start, target, model, false)
	if len(path) == 0 {
		t.Fatalf("A* found no route; start neighbours: %+v", model.GetNeighbors(start))
	}
	for _, n := range path {
		if n.Z != 0 {
			t.Errorf("route took a detour at z=%d instead of climbing the direct staircase at z=0: %+v", n.Z, path)
			break
		}
	}
}
