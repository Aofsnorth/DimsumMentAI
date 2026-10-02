package pathfinder_test

import (
	"testing"

	"bedrock-ai/internal/bot/pathfinder"
)

// The tower step is the only way a bot gains height, and it used to be offered
// on a single condition: that the cell two above was empty.
//
// That is not enough to place anything. A placement is a click on the top face
// of the block below, so the block below has to exist, and the cell being filled
// has to be free or breakable. A tower that cannot be built burns the retry
// budget, drops the path, and sends the bot into a replan loop that never
// climbs — which is what a bot that "scaffolds upward but never gets anywhere"
// actually is.

// towerWorld is a column the bot can stand in: the bot's feet at y=81, a solid
// block under them at y=80, and open air above. obstacle, if set, is the block
// sitting in the cell the climb has to fill.
func towerWorld(obstacle string) *pathfinder.LocalWorldModel {
	cells := map[string]string{
		blockKeyFor(0, 80, 0): "minecraft:dirt",
		blockKeyFor(0, 81, 0): "minecraft:air",
		blockKeyFor(0, 82, 0): "minecraft:air",
	}
	if obstacle != "" {
		cells[blockKeyFor(0, 81, 0)] = obstacle
	}
	w := swimModel(cells)
	w.AllowScaffold = true
	return w
}

// towerUpNode asks for the neighbours of a node the bot is standing on.
func towerUpNode(w *pathfinder.LocalWorldModel) []pathfinder.Node {
	return w.GetNeighbors(pathfinder.Node{X: 0, Y: 81, Z: 0})
}

// hasTower reports whether a straight-up placement step is among the neighbours.
func hasTower(neighbors []pathfinder.Node) (pathfinder.Node, bool) {
	for _, n := range neighbors {
		if n.X == 0 && n.Z == 0 && n.Action == "place" && n.Y == 82 {
			return n, true
		}
	}
	return pathfinder.Node{}, false
}

func TestAColumnWithSomethingUnderItOffersAClimb(t *testing.T) {
	t.Parallel()

	node, ok := hasTower(towerUpNode(towerWorld("")))
	if !ok {
		t.Fatal("no straight-up place step offered on an open column, want one: this is the case that has to work")
	}
	if node.LinkType != pathfinder.LinkWalk {
		t.Errorf("LinkType = %v, want LinkWalk", node.LinkType)
	}
}

func TestAColumnWithNothingUnderItOffersNoClimb(t *testing.T) {
	t.Parallel()

	// The face to click is missing. Offering the step anyway means the bot tries,
	// fails, drops the path and replans — three times, then it gives up on the
	// whole trip instead of walking around the edge.
	w := swimModel(map[string]string{
		blockKeyFor(0, 81, 0): "minecraft:air",
	})
	w.AllowScaffold = true

	if _, ok := hasTower(towerUpNode(w)); ok {
		t.Error("a straight-up place step was offered with nothing underneath to place onto")
	}
}

func TestAColumnBlockedByBedrockOffersNoClimb(t *testing.T) {
	t.Parallel()

	// Bedrock was already excluded. The bot is told to go around, so the planner
	// must not hand it a step that ends in a wall.
	if _, ok := hasTower(towerUpNode(towerWorld("minecraft:bedrock"))); ok {
		t.Error("a straight-up place step was offered through bedrock")
	}
}

func TestAColumnBlockedByObsidianOffersNoClimb(t *testing.T) {
	t.Parallel()

	// The one that used to slip through. IsBreakable answered "not bedrock", so
	// obsidian counted as breakable and a route straight through an obsidian
	// pillar looked fine to the planner. Nine seconds with a diamond pickaxe;
	// minutes with what the bot was actually holding.
	if _, ok := hasTower(towerUpNode(towerWorld("minecraft:obsidian"))); ok {
		t.Error("a straight-up place step was offered through obsidian: the bot must route around it")
	}
}

func TestAColumnBlockedByDirtStillOffersAClimb(t *testing.T) {
	t.Parallel()

	// The control for the two tests above. Dirt in the way is ordinary: the
	// executor breaks it and carries on, and the planner must not pre-emptively
	// rule the column out or the bot would refuse to climb past anything.
	if _, ok := hasTower(towerUpNode(towerWorld("minecraft:dirt"))); !ok {
		t.Error("a straight-up place step was refused past a block the bot can break")
	}
}

func TestAColumnWithACrampedHeadIsNotOffered(t *testing.T) {
	t.Parallel()

	// Solid at the support plus solid above it leaves nowhere for the body to
	// stand once the block is in, so the step cannot end anywhere useful.
	cells := map[string]string{
		blockKeyFor(0, 80, 0): "minecraft:dirt",
		blockKeyFor(0, 81, 0): "minecraft:air",
		blockKeyFor(0, 82, 0): "minecraft:obsidian",
	}
	w := swimModel(cells)
	w.AllowScaffold = true

	if _, ok := hasTower(towerUpNode(w)); ok {
		t.Error("a straight-up place step was offered into a cell with no headroom")
	}
}

func TestScaffoldingIsStillTheOnlyWayUp(t *testing.T) {
	t.Parallel()

	// The whole point of the step. If this ever stops being true the bot has no
	// way to gain a single block of height and every climb is impossible.
	neighbors := towerUpNode(towerWorld(""))

	var up []pathfinder.Node
	for _, n := range neighbors {
		if n.Y > 81 {
			up = append(up, n)
		}
	}
	if len(up) == 0 {
		t.Fatal("no neighbour at all goes up from a standable node")
	}
}

func TestATowerStepChainsIntoTheNextOne(t *testing.T) {
	t.Parallel()

	// The bug that made a bot unable to climb at all.
	//
	// Every tower step places the block the NEXT step has to stand on, so from
	// the second step onward the face to click is a cell that is still air in the
	// world the planner is looking at — nothing has been placed yet. Requiring it
	// to be solid already capped every climb at one block: the first step was
	// planned, the second could not be, and a five-block ascent came back as a
	// one-block path and then nothing at all.
	//
	// The shape here is the live one — flat ground at y=80, the bot at y=81, a
	// target five blocks up — because the bug only shows when the chain has to be
	// longer than one.
	cells := map[string]string{}
	for x := int32(-46); x <= -34; x++ {
		for z := int32(225); z <= 237; z++ {
			cells[blockKeyFor(x, 80, z)] = "minecraft:grass_block"
			for y := int32(81); y <= 95; y++ {
				cells[blockKeyFor(x, y, z)] = "minecraft:air"
			}
		}
	}
	w := swimModel(cells)
	w.AllowScaffold = true

	start := pathfinder.Node{X: -40, Y: 81, Z: 231}
	target := pathfinder.Node{X: -40, Y: 86, Z: 231}

	path := pathfinder.FindPath(start, target, w, true)
	if len(path) == 0 {
		t.Fatal("no path to a target five blocks up, want a chain of tower steps")
	}

	arrived := path[len(path)-1]
	if arrived.Y != target.Y {
		t.Errorf("path ends at y=%d, want y=%d: the climb gave up short", arrived.Y, target.Y)
	}

	// Every step up has to be a placement, and they have to be consecutive.
	var places int
	for i := 1; i < len(path); i++ {
		if path[i].Y == path[i-1].Y {
			continue
		}
		if path[i].Action != "place" {
			t.Errorf("step %d climbs from y=%d to y=%d with action %q, want \"place\"",
				i, path[i-1].Y, path[i].Y, path[i].Action)
		}
		places++
	}
	if places != 5 {
		t.Errorf("climb used %d placement steps, want 5 to gain five blocks of height", places)
	}
}
