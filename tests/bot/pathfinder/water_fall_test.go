package pathfinder_test

import (
	"testing"

	"bedrock-ai/internal/bot/pathfinder"
)

// Water and the ground rules overlap, and the overlap is worth pinning.
//
// A body floating in a river can be offered a fall onto the riverbed by the
// ground rules, alongside the swim and dive links the water rules add. That
// looks like a hazard — a cheap shortcut that drops the bot three blocks and
// strands it on the bottom — and it was proposed as a bug to fix.
//
// It is not one, and this file is why. The two links are both offered; what
// decides the route is their cost, and swimming is the cheaper of the two. A
// fall to the bed pays 1.0 plus 0.3 a block, so a three-block drop costs 1.90,
// while the swim link beside it costs 1.00. A* takes the cheap one.
//
// Suppressing the fall was tried and reverted: tryDrop can only land a body on a
// solid floor, so the fall it offers into a river is always a drop onto a bed.
// Refusing water landings therefore also refuses stepping into the shin-deep
// water over that same bed, which is a walk a body does without swimming. The
// overlap is harmless and the alternative is worse.

// TestSwimmingIsCheaperThanFallingOntoTheRiverbed is the invariant: the swim
// link must undercut the fall, or the riverbed shortcut becomes the route.
func TestSwimmingIsCheaperThanFallingOntoTheRiverbed(t *testing.T) {
	t.Parallel()

	// A four-block-deep channel with its bed three blocks below the waterline.
	cells := riverCells(3, 12, 16, 63, 59, 63, 2)
	w := swimModel(cells)

	// A body floating at the surface in the middle of the channel.
	from := pathfinder.Node{X: 4, Y: 63, Z: 0}

	var swim, fall float32
	var sawFall bool
	for _, n := range w.GetNeighbors(from) {
		switch n.LinkType {
		case pathfinder.LinkSwim:
			if n.G < swim || swim == 0 {
				swim = n.G
			}
		case pathfinder.LinkFall:
			sawFall = true
			if n.G < fall || fall == 0 {
				fall = n.G
			}
		}
	}

	if swim == 0 {
		t.Fatal("no swim link from a body floating in a river")
	}
	if !sawFall {
		// Not a failure on its own: a channel with no reachable bed offers no
		// fall, which is the same end state.
		t.Log("this channel happens to offer no fall link; the swim link still has to exist")
		return
	}
	if fall < swim {
		t.Errorf("falling onto the riverbed costs %.2f but swimming costs %.2f; the cheaper link wins and drops the body three blocks onto the bottom", fall, swim)
	}
}

// TestTheRiverbedIsStillReachable documents why the fall link is not a bug at
// all: dropping to the bed is sometimes the cheapest way along a shallow river,
// and a rule that forbade it would strand a body that would rather walk the
// bottom than swim the surface.
func TestTheRiverbedIsStillReachable(t *testing.T) {
	t.Parallel()

	cells := riverCells(3, 12, 16, 63, 59, 63, 2)
	w := swimModel(cells)

	reachable := false
	for _, n := range w.GetNeighbors(pathfinder.Node{X: 4, Y: 63, Z: 0}) {
		if n.LinkType == pathfinder.LinkFall && n.Y == 60 {
			reachable = true
			break
		}
	}
	if !reachable {
		t.Error("the riverbed is no longer reachable from the waterline; the fall link is a legitimate option and suppressing it would be the regression")
	}
}
