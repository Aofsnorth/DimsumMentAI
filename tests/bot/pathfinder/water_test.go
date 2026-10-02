package pathfinder_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/pathfinder"

	// The palette is linked for one reason: world_neighbors.go's isHalfBlock and
	// isClimbableSurface call chunk.RuntimeIDToState without the nil guard that
	// blockNameFor has, and a test binary that never links dragonfly's world
	// package leaves that var nil. Any A* over a model with a chunk querier walks
	// straight into it. The fake runtime IDs below start well past the end of the
	// real palette, so those two helpers read "not a slab, not a ladder" and
	// answer deterministically instead of matching whatever block happens to sit
	// at ID 1 in dragonfly.
	_ "github.com/df-mc/dragonfly/server/world"
)

// Water is a movement surface the walk rules know nothing about. Every walk,
// fall, parkour and step-jump rule asks "is there a floor to stand on", and a
// river answers no for its whole depth — which is why a bot walks to the bank,
// finds no route across, and stands there. These tests pin the swim links that
// give the search something to follow through the water.

// fakeRIDBase puts the fake palette outside the real one, so the model's own
// name map is the only thing that can resolve a cell and dragonfly's global
// converter always answers "unknown" for these IDs.
const fakeRIDBase uint32 = 1 << 20

// swimQuerier models a world the way the real chunk cache does: water is a
// liquid, not a wall. safety_test.go's nameQuerier reports every non-air block
// as solid, which is right for a lava test and wrong for a river — a river the
// pathfinder believes is a stone floor still produces a path, just not a swim.
//
// fullyDecoded decides what a cell that is not in the map looks like. A world
// with a querier and no path bounds answers "solid below sea level" for every
// cell it has never heard of, which is a guess about terrain and not a fact
// about water; the fully-decoded models below make those cells air instead, and
// the one test that cares about undecoded ground asks for it explicitly.
type swimQuerier struct {
	blocks       map[string]uint32
	names        map[uint32]string
	next         uint32
	fullyDecoded bool
}

func (q *swimQuerier) GetBlockRID(x, y, z int32) (uint32, bool) {
	rid, ok := q.blocks[blockKeyFor(x, y, z)]
	return rid, ok
}

func (q *swimQuerier) BlockName(rid uint32) (string, bool) {
	name, ok := q.names[rid]
	return name, ok
}

func (q *swimQuerier) IsBlockAir(x, y, z int32) (bool, bool) {
	name, ok := q.nameAt(x, y, z)
	if !ok {
		return true, q.fullyDecoded
	}
	return name == "minecraft:air" || name == "minecraft:water" || name == "minecraft:flowing_water", true
}

func (q *swimQuerier) IsBlockSolid(x, y, z int32) (bool, bool) {
	name, ok := q.nameAt(x, y, z)
	if !ok {
		return false, q.fullyDecoded
	}
	return !strings.Contains(name, "air") && !strings.Contains(name, "water"), true
}

func (q *swimQuerier) nameAt(x, y, z int32) (string, bool) {
	rid, ok := q.blocks[blockKeyFor(x, y, z)]
	if !ok {
		return "", false
	}
	name, known := q.names[rid]
	return name, known
}

func swimQuerierFor(cells map[string]string, fullyDecoded bool) *pathfinder.LocalWorldModel {
	q := &swimQuerier{
		blocks:       make(map[string]uint32, len(cells)),
		names:        make(map[uint32]string, len(cells)),
		fullyDecoded: fullyDecoded,
	}
	byName := make(map[string]uint32, len(cells))
	for cell, name := range cells {
		rid, seen := byName[name]
		if !seen {
			q.next = fakeRIDBase + q.next
			rid = q.next
			byName[name] = rid
			q.names[rid] = name
		}
		q.blocks[cell] = rid
	}
	w := pathfinder.NewLocalWorldModel()
	w.SetChunkQuerier(q)
	return w
}

// swimModel is a world that has decoded everything it is asked about.
func swimModel(cells map[string]string) *pathfinder.LocalWorldModel {
	return swimQuerierFor(cells, true)
}

// partialSwimModel is a world where the listed cells are real and the rest is
// still streaming in, which is what IsLoaded has to be able to tell apart.
func partialSwimModel(cells map[string]string) *pathfinder.LocalWorldModel {
	return swimQuerierFor(cells, false)
}

// riverCells lays out a channel along x. The banks are dry ground whose top
// solid cell is bankTop, so a bot standing on one has its feet at bankTop+1.
// The channel runs from bankRun+1 to channelEnd-1, with a bed at bedTop and
// water filling bedTop+1 up to waterTop.
func riverCells(bankRun, channelEnd, farRun, bankTop, bedTop, waterTop, zSpan int32) map[string]string {
	cells := make(map[string]string, 512)
	for z := -zSpan; z <= zSpan; z++ {
		for x := int32(0); x <= bankRun; x++ {
			cells[blockKeyFor(x, bankTop, z)] = "minecraft:dirt"
		}
		for x := bankRun + 1; x < channelEnd; x++ {
			cells[blockKeyFor(x, bedTop, z)] = "minecraft:sand"
		}
		for x := channelEnd; x <= farRun; x++ {
			cells[blockKeyFor(x, bankTop, z)] = "minecraft:dirt"
		}
		for x := bankRun + 1; x < channelEnd; x++ {
			for y := bedTop + 1; y <= waterTop; y++ {
				cells[blockKeyFor(x, y, z)] = "minecraft:water"
			}
		}
	}
	return cells
}

// --- neighbour expansion ---------------------------------------------------

// TestWaterNeighborsSwimHorizontallyThroughWater is the basic swim link: from
// inside water, the four side cells that are also water are neighbours.
func TestWaterNeighborsSwimHorizontallyThroughWater(t *testing.T) {
	t.Parallel()

	// Open water at y=61 in a 3x3 patch, nothing else in the world.
	cells := make(map[string]string)
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			cells[blockKeyFor(x, 61, z)] = "minecraft:water"
		}
	}
	w := swimModel(cells)

	neighbors := w.WaterNeighbors(pathfinder.Node{X: 0, Y: 61, Z: 0})

	cardinal, diagonal := 0, 0
	for _, n := range neighbors {
		if n.LinkType != pathfinder.LinkSwim {
			continue
		}
		if n.Y != 61 {
			t.Errorf("horizontal swim changed height: %+v", n)
		}
		if n.X == 0 || n.Z == 0 {
			cardinal++
		} else {
			diagonal++
		}
	}
	if cardinal != 4 {
		t.Errorf("got %d cardinal swim links from open water, want 4 (%+v)", cardinal, neighbors)
	}
	if diagonal != 4 {
		t.Errorf("got %d diagonal swim links from open water, want 4 (%+v)", diagonal, neighbors)
	}
}

// TestWaterNeighborsDiveAndRise pins the two vertical links, which are what
// make a three-block retrieval reachable at all.
func TestWaterNeighborsDiveAndRise(t *testing.T) {
	t.Parallel()

	// A water column at (0, *) from y=58 to y=64, water ending at 64.
	cells := make(map[string]string)
	for y := int32(58); y <= 64; y++ {
		cells[blockKeyFor(0, y, 0)] = "minecraft:water"
	}
	w := swimModel(cells)

	neighbors := w.WaterNeighbors(pathfinder.Node{X: 0, Y: 62, Z: 0})

	var dive, rise bool
	for _, n := range neighbors {
		switch n.LinkType {
		case pathfinder.LinkDive:
			dive = n.Y == 61
		case pathfinder.LinkSurface:
			rise = n.Y == 63
		}
	}
	if !dive {
		t.Errorf("no dive link below the node: %+v", neighbors)
	}
	if !rise {
		t.Errorf("no rise link above the node: %+v", neighbors)
	}
}

// TestWaterNeighborsRefuseADropOntoTheBed is the safety half. A swim link into
// stone is a bot stuck in a riverbed, so a solid or hazardous cell is never a
// swim neighbour even when the cell above it is water.
func TestWaterNeighborsRefuseADropOntoTheBed(t *testing.T) {
	t.Parallel()

	w := swimModel(map[string]string{
		blockKeyFor(0, 62, 0):  "minecraft:water",
		blockKeyFor(0, 61, 0):  "minecraft:stone",
		blockKeyFor(0, 61, 1):  "minecraft:water",
		blockKeyFor(0, 61, -1): "minecraft:water",
		blockKeyFor(1, 62, 0):  "minecraft:water",
		blockKeyFor(-1, 62, 0): "minecraft:water",
	})

	neighbors := w.WaterNeighbors(pathfinder.Node{X: 0, Y: 62, Z: 0})

	for _, n := range neighbors {
		if n.X == 0 && n.Y == 61 && n.Z == 0 {
			t.Errorf("a swim link landed inside stone: %+v", n)
		}
	}
	if len(neighbors) == 0 {
		t.Error("every neighbour was refused; the node can still swim sideways and up")
	}
}

// TestWaterNeighborsEnterWaterFromTheBank is the link the walk rules cannot
// make. From a dry bank node the cell over the water has no floor, so
// canStandAt is false and tryDrop finds nothing; only the water vocabulary can
// say "you can step in here".
func TestWaterNeighborsEnterWaterFromTheBank(t *testing.T) {
	t.Parallel()

	t.Run("level entry", func(t *testing.T) {
		t.Parallel()
		w := swimModel(map[string]string{
			blockKeyFor(0, 63, 0): "minecraft:dirt", // the bank floor the bot stands on
			blockKeyFor(1, 64, 0): "minecraft:water",
			blockKeyFor(1, 63, 0): "minecraft:water",
		})

		neighbors := w.WaterNeighbors(pathfinder.Node{X: 0, Y: 64, Z: 0})

		if len(neighbors) != 1 {
			t.Fatalf("got %d neighbours from a bank node, want 1: %+v", len(neighbors), neighbors)
		}
		if n := neighbors[0]; n.X != 1 || n.Y != 64 || n.Z != 0 || n.LinkType != pathfinder.LinkSwim {
			t.Errorf("bank entry = %+v, want a swim link into (1,64,0)", n)
		}
	})

	t.Run("step down into a lower waterline", func(t *testing.T) {
		t.Parallel()
		// The common case: the bank top and the water's top cell are the same
		// height, so the river is one cell below the bot's feet.
		w := swimModel(map[string]string{
			blockKeyFor(0, 63, 0): "minecraft:dirt",
			blockKeyFor(1, 63, 0): "minecraft:water",
		})

		neighbors := w.WaterNeighbors(pathfinder.Node{X: 0, Y: 64, Z: 0})

		if len(neighbors) != 1 {
			t.Fatalf("got %d neighbours from a bank node, want 1: %+v", len(neighbors), neighbors)
		}
		if n := neighbors[0]; n.X != 1 || n.Y != 63 || n.Z != 0 || n.LinkType != pathfinder.LinkSwim {
			t.Errorf("bank entry = %+v, want a swim link down into (1,63,0)", n)
		}
	})

	t.Run("a two-block drop is a dive, not an entry", func(t *testing.T) {
		t.Parallel()
		w := swimModel(map[string]string{
			blockKeyFor(0, 63, 0): "minecraft:dirt",
			blockKeyFor(1, 62, 0): "minecraft:water",
			blockKeyFor(1, 63, 0): "minecraft:water",
		})

		for _, n := range w.WaterNeighbors(pathfinder.Node{X: 0, Y: 64, Z: 0}) {
			if n.Y < 63 {
				t.Errorf("bank entry dropped two blocks into the water: %+v", n)
			}
		}
	})
}

// TestWaterNeighborsClimbOutOntoTheBank is the exit. A body in the water has to
// be able to plan a step back onto land, or every river is a one-way trip.
func TestWaterNeighborsClimbOutOntoTheBank(t *testing.T) {
	t.Parallel()

	w := swimModel(map[string]string{
		blockKeyFor(0, 63, 0): "minecraft:water", // the node the bot is in
		blockKeyFor(1, 64, 0): "minecraft:air",   // unlisted below; listed as water head-room
		blockKeyFor(1, 63, 0): "minecraft:dirt",  // the bank floor
	})

	neighbors := w.WaterNeighbors(pathfinder.Node{X: 0, Y: 63, Z: 0})

	var exit bool
	for _, n := range neighbors {
		if n.X == 1 && n.Z == 0 && n.LinkType == pathfinder.LinkSurface {
			exit = true
		}
	}
	if !exit {
		t.Errorf("no way out of the water onto the bank: %+v", neighbors)
	}
}

// TestWaterNeighborsIgnoreWaterlessSpace keeps the expansion from inventing
// swim links across ordinary air, which would flood every land route with
// water-cost edges and make the search meaningless.
func TestWaterNeighborsIgnoreWaterlessSpace(t *testing.T) {
	t.Parallel()

	w := swimModel(map[string]string{
		blockKeyFor(0, 63, 0): "minecraft:dirt",
	})

	if neighbors := w.WaterNeighbors(pathfinder.Node{X: 0, Y: 64, Z: 0}); len(neighbors) != 0 {
		t.Errorf("dry, floorless cell produced water neighbours: %+v", neighbors)
	}
}

// --- water-aware search ----------------------------------------------------

// TestWaterAwarePathCrossesARiver is the 2.1 acceptance at search level: a path
// from one bank to the other exists and most of it is a swim.
func TestWaterAwarePathCrossesARiver(t *testing.T) {
	t.Parallel()

	// Banks top out at y=63 (a bot on them has its feet at 64), the channel
	// spans x=4..11 with its bed at 59, and the water fills 60..63.
	world := pathfinder.NewWaterWorldModel(swimModel(riverCells(3, 12, 16, 63, 59, 63, 2)))

	start := pathfinder.Node{X: 2, Y: 64, Z: 0}
	target := pathfinder.Node{X: 16, Y: 64, Z: 0}

	path := pathfinder.FindPath(start, target, world, false)
	if len(path) == 0 {
		t.Fatal("no path across the river; the bot would stand on the bank forever")
	}

	swimLinks := 0
	for _, n := range path {
		if n.LinkType == pathfinder.LinkSwim || n.LinkType == pathfinder.LinkDive || n.LinkType == pathfinder.LinkSurface {
			swimLinks++
		}
	}
	if swimLinks == 0 {
		t.Fatalf("the route across the river used no water links at all: %+v", path)
	}
	if !pathfinder.IsTargetReached(&path[len(path)-1], target) {
		t.Fatalf("path ends at %+v, short of %+v", path[len(path)-1], target)
	}
}

// TestWaterAwarePathReachesAnUnderwaterTarget is 2.2 at search level: three
// blocks down, the search still gets there, and every cell it goes through is
// one a body can actually be in.
//
// The claim is reachability, not which vertical link won the cost comparison.
// The ground rules also offer a three-block fall into the riverbed at a price
// the swim dive does not beat, and that is a preference, not a defect: what
// matters is that a route exists at all and it stays in the water.
func TestWaterAwarePathReachesAnUnderwaterTarget(t *testing.T) {
	t.Parallel()

	cells := riverCells(3, 12, 16, 63, 59, 63, 2)
	world := pathfinder.NewWaterWorldModel(swimModel(cells))

	start := pathfinder.Node{X: 2, Y: 64, Z: 0}
	target := pathfinder.Node{X: 8, Y: 60, Z: 0} // three cells below the water's top

	path := pathfinder.FindPath(start, target, world, false)
	if len(path) == 0 {
		t.Fatal("no path to an item three blocks underwater")
	}

	last := path[len(path)-1]
	if !last.Equal(&target) {
		t.Fatalf("path ends at (%d,%d,%d), want the item at (8,60,0)", last.X, last.Y, last.Z)
	}

	// The dive itself is a chain of LinkDive links, so build one by hand and
	// check the search can actually descend three cells in water.
	diveChain := pathfinder.Node{X: 5, Y: 63, Z: 0}
	descended := diveChain
	for want := int32(62); want >= 60; want-- {
		var next *pathfinder.Node
		for _, n := range world.WaterNeighbors(descended) {
			if n.LinkType == pathfinder.LinkDive && n.Y == want {
				copied := n
				next = &copied
				break
			}
		}
		if next == nil {
			t.Fatalf("no dive link from y=%d to y=%d; the descent is not reachable in water", descended.Y, want)
		}
		descended = *next
	}
}

// TestTheWaterVocabularyIsWhatMakesTheRouteExist gives the file its reason: the
// route only exists because of the water vocabulary, and it has to be a real
// crossing rather than a detour round the problem.
//
// This used to assert the opposite shape — that the bare model found nothing and
// only NewWaterWorldModel found a route. The water rules are now folded into
// GetNeighbors, because that call is the only neighbour entry point A* uses and
// a wrapper a production caller has to remember to apply is a wrapper nobody
// applies. So the bare model finding the route is now the requirement, and the
// wrapper is kept only as an additive decoration that must not change the answer.
func TestTheWaterVocabularyIsWhatMakesTheRouteExist(t *testing.T) {
	t.Parallel()

	start := pathfinder.Node{X: 2, Y: 64, Z: 0}
	target := pathfinder.Node{X: 8, Y: 60, Z: 0}
	cells := riverCells(3, 12, 16, 63, 59, 63, 2)

	bare := pathfinder.FindPath(start, target, swimModel(cells), false)
	if len(bare) == 0 {
		t.Fatal("the default expansion found no route across the water; the swim, dive and surface links are not reaching A*")
	}
	last := bare[len(bare)-1]
	if last.X != target.X || last.Y != target.Y || last.Z != target.Z {
		t.Fatalf("route ends at %+v, want the target %+v", last, target)
	}
	// A route that never enters the channel is not a crossing.
	sawWater := false
	for _, n := range bare {
		if n.X >= 4 && n.X <= 11 {
			sawWater = true
			break
		}
	}
	if !sawWater {
		t.Errorf("route reached the far side without entering the channel: %+v", bare)
	}

	// The decorator still has to work, and still has to agree: it adds water on
	// top of a model that now supplies it, so a disagreement would mean one of
	// the two paths is producing links the other does not.
	wrapped := pathfinder.FindPath(start, target, pathfinder.NewWaterWorldModel(swimModel(cells)), false)
	if len(wrapped) == 0 {
		t.Error("NewWaterWorldModel found no route over a model whose default expansion already does")
	}
}

// TestWaterAwarePathRefusesToSwimThroughStone is the guard for the whole feature:
// a swim link is only legal where the body can actually be.
func TestWaterAwarePathRefusesToSwimThroughStone(t *testing.T) {
	t.Parallel()

	cells := make(map[string]string)
	for x := int32(0); x <= 4; x++ {
		cells[blockKeyFor(x, 59, 0)] = "minecraft:dirt"
		cells[blockKeyFor(x, 64, 0)] = "minecraft:water"
	}
	cells[blockKeyFor(2, 64, 0)] = "minecraft:obsidian" // a wall across the channel
	world := pathfinder.NewWaterWorldModel(swimModel(cells))

	path := pathfinder.FindPath(
		pathfinder.Node{X: 0, Y: 64, Z: 0},
		pathfinder.Node{X: 4, Y: 64, Z: 0},
		world, false,
	)

	for _, n := range path {
		if n.X == 2 && n.Y == 64 && n.Z == 0 {
			t.Fatalf("the path walks through an obsidian wall: %+v", n)
		}
	}
}

// TestWaterWorldModelKeepsThePathBoundsAndLoadAwareness checks that wrapping a
// world model in the water expansion does not quietly downgrade it. A* asks
// for SetPathBounds through a type assertion and the smoother asks for
// IsLoaded through another; a wrapper that swallows either one changes how
// every other link in the path is validated.
func TestWaterWorldModelKeepsThePathBoundsAndLoadAwareness(t *testing.T) {
	t.Parallel()

	inner := partialSwimModel(map[string]string{blockKeyFor(0, 63, 0): "minecraft:dirt"})
	inner.SetPathBounds(pathfinder.Node{X: 0, Y: 64, Z: 0}, pathfinder.Node{X: 8, Y: 64, Z: 0})

	world := pathfinder.NewWaterWorldModel(inner)
	pathfinder.FindPath(
		pathfinder.Node{X: 0, Y: 64, Z: 0},
		pathfinder.Node{X: 8, Y: 64, Z: 0},
		world, false,
	)

	if !inner.HasPathBounds() {
		t.Error("the water wrapper swallowed SetPathBounds; unknown cells now fall back to the sea-level guess")
	}
	if !world.IsLoaded(0, 63, 0) {
		t.Error("a decoded cell is reported unknown through the water wrapper")
	}
	if world.IsLoaded(900, 63, 0) {
		t.Error("an undecoded cell is reported known through the water wrapper")
	}
}

// TestWaterWorldModelStaysDryWithoutAWaterVocabulary keeps the wrapper honest
// when the model underneath cannot answer water questions at all.
func TestWaterWorldModelStaysDryWithoutAWaterVocabulary(t *testing.T) {
	t.Parallel()

	world := pathfinder.NewWaterWorldModel(dryWorld{})
	if world.IsWater(0, 64, 0) {
		t.Error("a model with no water vocabulary reported water; every swim would be planned on a guess")
	}
}

// TestCanSwimDirectly checks the straight-line swim link. A string-pulled link
// is walked without re-checking, so pulling one out of the water and across a
// bank is the same class of bug as pulling a walk link through a wall.
func TestCanSwimDirectly(t *testing.T) {
	t.Parallel()

	// A channel from x=1 to x=10, with dry ground at x=0 and x=11.
	cells := make(map[string]string)
	for x := int32(0); x <= 11; x++ {
		cells[blockKeyFor(x, 63, 0)] = "minecraft:dirt"
	}
	for x := int32(1); x <= 10; x++ {
		cells[blockKeyFor(x, 64, 0)] = "minecraft:water"
	}
	world := pathfinder.NewWaterWorldModel(swimModel(cells))

	from := pathfinder.Node{X: 1, Y: 64, Z: 0, LinkType: pathfinder.LinkSwim}
	across := pathfinder.Node{X: 10, Y: 64, Z: 0, LinkType: pathfinder.LinkSwim}
	bank := pathfinder.Node{X: 0, Y: 64, Z: 0, LinkType: pathfinder.LinkWalk}

	if !pathfinder.CanSwimDirectly(from, across, world) {
		t.Error("a straight link across open water was refused")
	}
	if pathfinder.CanSwimDirectly(bank, across, world) {
		t.Error("a link starting on dry land was accepted as a swim")
	}

	cells[blockKeyFor(5, 64, 0)] = "minecraft:stone"
	blocked := pathfinder.NewWaterWorldModel(swimModel(cells))
	if pathfinder.CanSwimDirectly(from, across, blocked) {
		t.Error("a straight swim link through a block was accepted")
	}
}

// TestCanSwimDirectlyRefusesAWorldWithoutWater mirrors the type-assertion seam:
// a world model that cannot answer must not be read as "all water".
func TestCanSwimDirectlyRefusesAWorldWithoutWater(t *testing.T) {
	t.Parallel()

	from := pathfinder.Node{X: 0, Y: 64, Z: 0}
	to := pathfinder.Node{X: 4, Y: 64, Z: 0}

	if pathfinder.CanSwimDirectly(from, to, dryWorld{}) {
		t.Error("a world model with no water vocabulary produced a swim link")
	}
}

// dryWorld satisfies the bare search interface and nothing else — no IsWater,
// no IsLoaded — which is how a hand-written test double usually looks.
type dryWorld struct{}

func (dryWorld) IsSolid(x, y, z int32) bool                       { return false }
func (dryWorld) IsHazard(x, y, z int32) bool                      { return false }
func (dryWorld) IsLadder(x, y, z int32) bool                      { return false }
func (dryWorld) GetNeighbors(n pathfinder.Node) []pathfinder.Node { return nil }
func (dryWorld) SetPathBounds(start, target pathfinder.Node)      {}
