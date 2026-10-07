package pathfinder_test

import (
	"testing"

	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/bot/world"
)

// namedQuerier models the world through block NAMES rather than runtime IDs.
//
// It is a faithful stand-in for the live Geyser hub, whose 1.21 palette decodes
// its walkway surface as `minecraft:granite_wall` / `minecraft:tuff_brick_wall`
// with air at body height above it. The production code reaches block names via
// `BlockName(rid)`, so this double resolves a per-cell RID to a per-RID name
// exactly the way WorldCache does.
type namedQuerier struct {
	cells   map[[3]int32]string
	byRID   map[uint32]string
	nextRID uint32
}

func newNamedQuerier() *namedQuerier {
	return &namedQuerier{
		cells: make(map[[3]int32]string),
		byRID: make(map[uint32]string),
	}
}

// set places a block by name and assigns it a runtime ID, mirroring the way a
// decoded sub-chunk palette stores network indices.
func (q *namedQuerier) set(x, y, z int32, name string) {
	q.nextRID++
	q.byRID[q.nextRID] = name
	q.cells[[3]int32{x, y, z}] = name
}

func (q namedQuerier) ridAt(x, y, z int32) (uint32, bool) {
	name, ok := q.cells[[3]int32{x, y, z}]
	if !ok {
		return 0, false
	}
	for rid, n := range q.byRID {
		if n == name {
			return rid, true
		}
	}
	return 0, false
}

func (q namedQuerier) GetBlockRID(x, y, z int32) (uint32, bool) { return q.ridAt(x, y, z) }

func (q namedQuerier) BlockName(rid uint32) (string, bool) {
	name, ok := q.byRID[rid]
	return name, ok
}

func (q namedQuerier) nameAt(x, y, z int32) string {
	name, ok := q.cells[[3]int32{x, y, z}]
	if !ok {
		return "minecraft:air"
	}
	return name
}

func (q namedQuerier) IsBlockAir(x, y, z int32) (bool, bool) {
	return q.nameAt(x, y, z) == "minecraft:air", true
}

func (q namedQuerier) IsBlockSolid(x, y, z int32) (bool, bool) {
	return !world.IsBlockNamePassable(q.nameAt(x, y, z)), true
}

// hubWorld builds the surface the bot is actually standing on in the live log:
// a granite_wall floor with air at body height and the feet.
func hubWorld(t *testing.T, start, target pathfinder.Node) (*pathfinder.LocalWorldModel, *namedQuerier) {
	t.Helper()
	q := newNamedQuerier()
	for x := int32(0); x <= 25; x++ {
		for z := int32(-8); z <= 2; z++ {
			q.set(x, 68, z, "minecraft:granite_wall")
		}
	}
	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(start, target)
	model.SetChunkQuerier(q)
	return model, q
}

// TestWallFloorSurfaceIsWalkableAtTheBotsOwnLevel is the live regression.
//
// The bot stands on a surface the 1.21 palette decodes as `granite_wall`, with air
// at body height. Every canStandAt predicate passes except the fence/wall veto,
// which fired because a wall is 1.5 blocks tall and so is not climbable. That is
// true, and irrelevant here: the bot is already at the level of that surface
// rather than trying to climb onto it. The result was zero neighbours from the
// start node and an A* dead end the bot could not escape.
func TestWallFloorSurfaceIsWalkableAtTheBotsOwnLevel(t *testing.T) {
	t.Parallel()

	start := pathfinder.Node{X: 15, Y: 69, Z: -3}
	target := pathfinder.Node{X: 0, Y: 73, Z: 0}

	model, q := hubWorld(t, start, target)
	if !model.IsSolid(15, 68, -3) {
		t.Fatalf("premise broken: %q at y-1 should be solid", q.nameAt(15, 68, -3))
	}

	neighbors := model.GetNeighbors(start)
	if len(neighbors) == 0 {
		t.Fatalf("start node on a wall-topped surface produced zero neighbours")
	}

	for _, want := range []pathfinder.Node{{X: 15, Y: 69, Z: -4}, {X: 15, Y: 69, Z: -2}, {X: 14, Y: 69, Z: -3}, {X: 16, Y: 69, Z: -3}} {
		found := false
		for _, n := range neighbors {
			if n.X == want.X && n.Y == want.Y && n.Z == want.Z {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected cardinal neighbour %+v, got %+v", want, neighbors)
		}
	}
}

// TestStepUpFromOrdinaryGroundOntoAWallPavedSurface is the transition the first
// version of the wall rule broke.
//
// The bot stands on ordinary stone with its feet at exactly the node level. One
// tile away the floor is `tuff_brick_wall`, whose collision top is half a block
// higher. Comparing that against the bot's feet with nothing else vetoed it —
// but half a block is an ordinary step, below the 0.6 a player climbs without
// jumping. The lobby in the live log is paved in walls and ringed by ordinary
// ground, so this is the doorway into it, and the bot refused to walk through
// its own front door.
func TestStepUpFromOrdinaryGroundOntoAWallPavedSurface(t *testing.T) {
	t.Parallel()

	// Bot on stone at (15, 71, -3): feet at node level 71, floor is y=70.
	start := pathfinder.Node{X: 15, Y: 71, Z: -3}
	target := pathfinder.Node{X: 0, Y: 73, Z: 0}

	q := newNamedQuerier()
	q.set(15, 70, -3, "minecraft:stone") // ordinary floor under the bot
	for x := int32(13); x <= 16; x++ {   // the wall-paved surface to the west
		for z := int32(-4); z <= -2; z++ {
			q.set(x, 70, z, "minecraft:tuff_brick_wall")
		}
	}

	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(start, target)
	model.SetChunkQuerier(q)

	for _, n := range model.GetNeighbors(start) {
		if n.Y == 71 && n.X == 14 && n.Z == -3 {
			return
		}
	}
	t.Errorf("bot on stone could not step the half block onto the adjacent wall surface; neighbours: %+v", model.GetNeighbors(start))
}

// TestStepJumpOntoAWallIsStillRefused keeps the original rule alive.
//
// The veto exists so A* never plans a one-block hop onto a 1.5-block wall it
// cannot physically clear. Relaxing canStandAt must not relax this.
func TestStepJumpOntoAWallIsStillRefused(t *testing.T) {
	t.Parallel()

	start := pathfinder.Node{X: 15, Y: 69, Z: -3}
	target := pathfinder.Node{X: 16, Y: 70, Z: -3}

	q := newNamedQuerier()
	q.set(15, 68, -3, "minecraft:granite_wall")
	q.set(16, 69, -3, "minecraft:tuff_brick_wall") // the wall being climbed onto

	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(start, target)
	model.SetChunkQuerier(q)

	for _, n := range model.GetNeighbors(start) {
		if n.X == 16 && n.Y == 70 && n.Z == -3 {
			t.Errorf("A* planned a step onto a 1.5-block wall: %+v", n)
		}
	}
}

// TestFenceIsStillNotClimbedFromBelow is the same rule for fences.
func TestFenceIsStillNotClimbedFromBelow(t *testing.T) {
	t.Parallel()

	start := pathfinder.Node{X: 15, Y: 69, Z: -3}
	target := pathfinder.Node{X: 16, Y: 70, Z: -3}

	q := newNamedQuerier()
	q.set(15, 68, -3, "minecraft:granite")
	q.set(16, 69, -3, "minecraft:oak_fence")

	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(start, target)
	model.SetChunkQuerier(q)

	for _, n := range model.GetNeighbors(start) {
		if n.X == 16 && n.Y == 70 && n.Z == -3 {
			t.Errorf("A* planned a step onto a fence: %+v", n)
		}
	}
}
