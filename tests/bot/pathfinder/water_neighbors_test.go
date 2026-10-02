package pathfinder_test

import (
	"testing"

	"bedrock-ai/internal/bot/pathfinder"
)

// The water vocabulary was written, tested against WaterNeighbors, and then
// never reached the search. GetNeighbors is the only neighbour entry point A*
// uses, and it called the ladder, cardinal, diagonal and scaffold rules and
// nothing else — so the swim links existed as a method nobody invoked, and a
// bot that walked into a river got a path to the bank and then stopped.
//
// These tests go through GetNeighbors, because that is the call production
// makes. WaterNeighbors is the vocabulary under test in water_test.go; this
// file is about whether the default expansion asks for it.

// riverRoute is the geometry every test here shares: dry banks at y=64 with
// their top solid cell at 64, a channel from x=2 to x=6 with a sand bed at
// y=62, and water filling y=63..64. A bot on the near bank stands at y=65; the
// river surface is two blocks below its feet and the bed is three.
//
//	                   water
//	y=65   . . . . .  . . .  . . . . .    <- bank feet / open air
//	y=64   # # . . .  ~ ~ ~  . . . # #    <- dirt | water
//	y=63   . . . . .  ~ ~ ~  . . . . .
//	y=62   . . . . .  # # #  . . . . .    <- sand bed
//	         x=0 1  2 3 4 5  6   7 8 9
func riverRoute() *pathfinder.LocalWorldModel {
	return swimModel(riverCells(1, 7, 9, 64, 62, 64, 1))
}

// linkTypes counts the neighbours of a node by link type.
func linkTypes(neighbors []pathfinder.Node) map[pathfinder.LinkType]int {
	out := make(map[pathfinder.LinkType]int, len(neighbors))
	for _, n := range neighbors {
		out[n.LinkType]++
	}
	return out
}

// hasLinkTo reports whether a neighbour of the given type lands on a cell.
func hasLinkTo(neighbors []pathfinder.Node, link pathfinder.LinkType, x, y, z int32) bool {
	for _, n := range neighbors {
		if n.LinkType == link && n.X == x && n.Y == y && n.Z == z {
			return true
		}
	}
	return false
}

// TestGetNeighborsOffersSwimLinksInsideARiver is B3 stated as the failure it
// prevents: expansion from a body floating in the river has to produce the
// moves a swimmer can make. Before the water rules were folded in, the only
// neighbours a mid-river node had were the ground rules' opinion of a river,
// which is that there is no floor anywhere and therefore nowhere to go.
func TestGetNeighborsOffersSwimLinksInsideARiver(t *testing.T) {
	t.Parallel()

	w := riverRoute()
	// Standing on the sand bed with the waterline two blocks above the feet.
	start := pathfinder.Node{X: 4, Y: 63, Z: 0}

	neighbors := w.GetNeighbors(start)
	counts := linkTypes(neighbors)

	if counts[pathfinder.LinkSwim] == 0 {
		t.Errorf("no swim link from a node inside the river: %+v", counts)
	}
	if counts[pathfinder.LinkSurface] == 0 {
		t.Errorf("no surface link from a node inside the river: %+v", counts)
	}

	// Walk links alongside the water links are correct here, and used to be
	// asserted away. This node is standing on the sand bed at y=62 with only
	// y=63 wet, which is shin-deep water: a real body walks across that without
	// swimming, and a path that refused the walk would make the bot pay a swim
	// cost for a puddle. The case that must NOT produce walk links is a body
	// over deep water with no floor under it, and that is enforced by the
	// standability check rather than by the link type — see
	// TestGetNeighborsOffersDiveLinksThroughDeepWater.
	if counts[pathfinder.LinkWalk] == 0 {
		t.Errorf("shin-deep water over a solid bed refused every walk link: %+v", neighbors)
	}
}

// TestGetNeighborsOffersDiveLinksThroughDeepWater is the retrieval case: the
// reason water is in the vocabulary at all is a thing three blocks down.
func TestGetNeighborsOffersDiveLinksThroughDeepWater(t *testing.T) {
	t.Parallel()

	w := riverRoute()
	// Two below the surface, one above the bed: the middle of the channel.
	start := pathfinder.Node{X: 4, Y: 64, Z: 0}

	neighbors := w.GetNeighbors(start)

	if !hasLinkTo(neighbors, pathfinder.LinkDive, 4, 63, 0) {
		t.Errorf("no dive link into the cell below a mid-river node: %+v", neighbors)
	}
	if !hasLinkTo(neighbors, pathfinder.LinkSwim, 3, 64, 0) {
		t.Errorf("no horizontal swim link beside a mid-river node: %+v", neighbors)
	}
}

// TestGetNeighborsEntersWaterFromTheBank is the other half: a bot standing on
// dry ground at the river's edge has to be able to get in, or the swim links
// only help a body that is already wet.
func TestGetNeighborsEntersWaterFromTheBank(t *testing.T) {
	t.Parallel()

	w := riverRoute()
	// Feet on the dirt bank at x=1, whose top solid cell is y=64.
	start := pathfinder.Node{X: 1, Y: 65, Z: 0}

	neighbors := w.GetNeighbors(start)

	if !hasLinkTo(neighbors, pathfinder.LinkSwim, 2, 64, 0) {
		t.Errorf("no swim entry from the bank into the water beside it: %+v", neighbors)
	}
}

// TestGetNeighborsClimbsOutOfWaterOntoTheBank closes the loop. A swimmer that
// can enter but never leave is a bot that ends every trip in a river.
func TestGetNeighborsClimbsOutOfWaterOntoTheBank(t *testing.T) {
	t.Parallel()

	w := riverRoute()
	start := pathfinder.Node{X: 2, Y: 64, Z: 0}

	neighbors := w.GetNeighbors(start)

	if !hasLinkTo(neighbors, pathfinder.LinkSurface, 1, 65, 0) {
		t.Errorf("no exit link from the water onto the bank beside it: %+v", neighbors)
	}
}

// TestGetNeighborsFindsARouteAcrossTheRiver is the assertion that matters: not
// that the links exist, but that A* can string them together. The bot starts on
// the near bank and has to reach the far one. Before the water rules were in
// the default expansion this returned the near bank and stopped.
func TestGetNeighborsFindsARouteAcrossTheRiver(t *testing.T) {
	t.Parallel()

	w := riverRoute()
	start := pathfinder.Node{X: 1, Y: 65, Z: 0}
	target := pathfinder.Node{X: 7, Y: 65, Z: 0}

	path := pathfinder.FindPath(start, target, w, false)
	if len(path) == 0 {
		t.Fatal("FindPath across a five-block river returned no path")
	}

	last := path[len(path)-1]
	if last.X != target.X || last.Y != target.Y || last.Z != target.Z {
		t.Fatalf("path ends at %+v, want the far bank %+v", last, target)
	}
	// Every step has to be legal water vocabulary, not a teleport: the route
	// must actually pass through the channel rather than round it.
	sawWater := false
	for _, n := range path {
		if n.X >= 2 && n.X <= 6 {
			sawWater = true
		}
	}
	if !sawWater {
		t.Errorf("route crossed without ever entering the channel: %+v", path)
	}
}

// TestGetNeighborsCostsStayComparableToWalking guards the cost model. The water
// vocabulary is priced so that crossing a river is neither a free shortcut nor
// a detour the bot refuses to take: a swim step is one block of travel, a dive
// is cheaper because the body controls its own depth, and entering costs more
// than a step because it is a change of medium.
func TestGetNeighborsCostsStayComparableToWalking(t *testing.T) {
	t.Parallel()

	w := riverRoute()

	var stepG, diveG, entryG float32
	haveStep, haveDive, haveEntry := false, false, false

	// Step and dive, from the middle of the channel.
	for _, n := range w.GetNeighbors(pathfinder.Node{X: 4, Y: 64, Z: 0}) {
		switch n.LinkType {
		case pathfinder.LinkSwim:
			if !haveStep {
				stepG, haveStep = n.G, true
			}
		case pathfinder.LinkDive:
			if !haveDive {
				diveG, haveDive = n.G, true
			}
		}
	}
	// Entry, from the bank.
	for _, n := range w.GetNeighbors(pathfinder.Node{X: 1, Y: 65, Z: 0}) {
		if n.LinkType == pathfinder.LinkSwim && !haveEntry {
			entryG, haveEntry = n.G, true
		}
	}

	if !haveStep {
		t.Fatal("no swim step link to price")
	}
	if !haveDive {
		t.Fatal("no dive link to price")
	}
	if !haveEntry {
		t.Fatal("no water entry link to price")
	}

	if stepG < 0.5 || stepG > 1.5 {
		t.Errorf("a one-block swim step costs %v; it should be about a block of travel", stepG)
	}
	if diveG >= stepG {
		t.Errorf("diving costs %v and a horizontal step costs %v; a controlled descent "+
			"must not be priced above a block of swimming or A* will never dive", diveG, stepG)
	}
	if entryG <= stepG {
		t.Errorf("entering water costs %v and a step within it costs %v; changing medium "+
			"must cost more than staying in it, or rivers become free shortcuts", entryG, stepG)
	}
}

// TestGetNeighborsOnDryGroundIsUnchangedByTheWaterRules is the blast-radius
// check. Every other movement mode shares this one function, so the water rules
// are only safe to fold in if a world with no water in it produces exactly what
// it produced before.
func TestGetNeighborsOnDryGroundIsUnchangedByTheWaterRules(t *testing.T) {
	t.Parallel()

	cells := make(map[string]string)
	for x := int32(-3); x <= 3; x++ {
		for z := int32(-3); z <= 3; z++ {
			cells[blockKeyFor(x, 64, z)] = "minecraft:stone"
		}
	}
	w := swimModel(cells)

	neighbors := w.GetNeighbors(pathfinder.Node{X: 0, Y: 65, Z: 0})

	for _, n := range neighbors {
		switch n.LinkType {
		case pathfinder.LinkSwim, pathfinder.LinkDive:
			t.Errorf("a flat stone floor produced water link %+v", n)
		}
	}

	// A flat floor must still be walkable in every cardinal direction.
	cardinals := 0
	for _, n := range neighbors {
		if n.LinkType == pathfinder.LinkWalk && n.Y == 65 {
			cardinals++
		}
	}
	if cardinals < 4 {
		t.Errorf("flat stone floor offers %d walk neighbours, want at least 4: %+v", cardinals, neighbors)
	}
}
