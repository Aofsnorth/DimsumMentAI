// Package pathfinder implements the 3D grid pathfinder used by the bot,
// including node expansion, A* search, and movement smoothing helpers.
package pathfinder

import (
	"fmt"
	"math"
	"strings"

	"bedrock-ai/internal/debuglog"

	"github.com/df-mc/dragonfly/server/world/chunk"
)

const (
	maxSafeStandDrop          int32 = 3
	maxParkourLandingDistance int32 = 4
)

var (
	// Cardinal directions used by the walker.
	cardinalOffsets = []struct{ dx, dz int32 }{
		{0, -1}, {0, 1}, {1, 0}, {-1, 0},
	}

	// Diagonal directions used by the walker.
	diagonalOffsets = []struct{ dx, dz int32 }{
		{1, 1}, {1, -1}, {-1, 1}, {-1, -1},
	}
)

// GetNeighbors returns all valid neighbor nodes for the given node.
//
// Water is one of the rules folded in here rather than a decorator a caller has
// to remember to wrap the model in. The wrap was the only way to add a rule
// before this call existed, and it never happened in production: the bot holds
// a *LocalWorldModel directly, so the swim, dive and surface links were written,
// tested against WaterNeighbors, and then unreachable — a path across a river
// stopped at the bank. The water rules are additive and read the same model the
// ground rules read, so merging them here costs a dry world nothing and gives a
// wet one a route.
func (w *LocalWorldModel) GetNeighbors(node Node) []Node {
	neighbors := make([]Node, 0, 16)
	before := len(neighbors)
	neighbors = w.appendLadderNeighbors(neighbors, node)
	ladderN := len(neighbors) - before
	before = len(neighbors)
	neighbors = w.appendCardinalNeighbors(neighbors, node)
	cardinalN := len(neighbors) - before
	before = len(neighbors)
	neighbors = w.appendDiagonalNeighbors(neighbors, node)
	diagN := len(neighbors) - before
	before = len(neighbors)
	neighbors = w.appendScaffoldNeighbors(neighbors, node)
	scaffoldN := len(neighbors) - before
	before = len(neighbors)
	neighbors = w.AppendWaterNeighbors(neighbors, node)
	waterN := len(neighbors) - before
	if len(neighbors) == 0 {
		// Sampled: an empty set is the common case inside a failed search, so
		// printing it unconditionally produced dozens of lines per repath and
		// buried the signal. debuglog keeps ~1 line per 250ms and stays silent
		// unless debug session logging is enabled.
		debuglog.Log("P", "pathfinder/world_neighbors.go:GetNeighbors", "empty neighbor set", map[string]any{
			"x": node.X, "y": node.Y, "z": node.Z,
			"ladder": ladderN, "cardinal": cardinalN, "diag": diagN, "scaffold": scaffoldN, "water": waterN,
		})
	}
	return neighbors
}

// DebugClimbRefused explains, for a search that exhausted without reaching its
// destination, which cell nearest the destination was the last one the bot could
// stand on and what stopped it going further. Diagnostic-only.
//
// A*'s own exhaustion line reports the best node and the iteration count, which
// says where the search stopped but never why. On a hub whose walkways are wall
// tiles, "why" is always a height question — the bot can see the destination and
// cannot climb to it — and every predicate involved reads a different block. This
// dumps them together, per direction, from the frontier cell itself.
func (w *LocalWorldModel) DebugClimbRefused(from, target Node) string {
	w.mu.RLock()
	hasBounds := w.hasBounds
	w.mu.RUnlock()
	footing := w.footingTopY()
	out := fmt.Sprintf("frontier=(%d,%d,%d) target=(%d,%d,%d) dy=%d footingTop=%.2f footingIsWall=%v bounds=%v:",
		from.X, from.Y, from.Z, target.X, target.Y, target.Z, target.Y-from.Y, footing,
		hasBounds && w.isFenceOrWall(w.startX, w.startY-1, w.startZ), hasBounds)
	for _, d := range []struct {
		name   string
		dx, dz int32
	}{
		{"N", 0, -1}, {"S", 0, 1}, {"E", 1, 0}, {"W", -1, 0},
	} {
		tx, tz := from.X+d.dx, from.Z+d.dz
		// Same level: can we walk there? One up: can we jump there?
		walk := w.canStandAt(tx, from.Y, tz)
		_, stepJump := w.canStepJumpTo(d.dx, d.dz, 1, from)
		// The neighbour's own floor, named, so "is it a wall" stops being a guess.
		floorName := "?"
		if w.chunkQuerier != nil {
			if rid, ok := w.chunkQuerier.GetBlockRID(tx, from.Y-1, tz); ok {
				if n, ok := blockNameFor(w.chunkQuerier, rid); ok {
					floorName = n
				} else {
					floorName = "<unnamed>"
				}
			}
		}
		upName := "?"
		if w.chunkQuerier != nil {
			if rid, ok := w.chunkQuerier.GetBlockRID(tx, from.Y, tz); ok {
				if n, ok := blockNameFor(w.chunkQuerier, rid); ok {
					upName = n
				} else {
					upName = "<unnamed>"
				}
			}
		}
		out += fmt.Sprintf(" %s[walk=%v jump=%v floor=%s@y%d wall=%v up=%s]",
			d.name, walk, stepJump, floorName, from.Y-1, w.isFenceOrWall(tx, from.Y-1, tz), upName)
	}
	return out
}

// DebugNeighborVeto explains why each cardinal direction from n was vetoed.
// Diagnostic-only; called by A* when start expands to zero neighbors.
func (w *LocalWorldModel) DebugNeighborVeto(n Node) string {
	out := fmt.Sprintf("start=(%d,%d,%d):", n.X, n.Y, n.Z)
	dirs := []struct {
		name   string
		dx, dz int32
	}{
		{"N", 0, -1}, {"S", 0, 1}, {"E", 1, 0}, {"W", -1, 0},
	}
	for _, d := range dirs {
		tx, tz := n.X+d.dx, n.Z+d.dz
		canStand := w.canStandAt(tx, n.Y, tz)
		isAir := w.isAirAt(tx, n.Y, tz)
		feetS := w.IsSolid(tx, n.Y, tz)
		headS := w.IsSolid(tx, n.Y+1, tz)
		floorS := w.IsSolid(tx, n.Y-1, tz)
		feetH := w.IsHazard(tx, n.Y, tz)
		headH := w.IsHazard(tx, n.Y+1, tz)
		floorH := w.IsHazard(tx, n.Y-1, tz)
		halfBlock := w.isHalfBlock(tx, n.Y-1, tz)
		fenceWall := w.isFenceOrWall(tx, n.Y-1, tz)
		ladder := w.IsLadder(tx, n.Y, tz)
		// Whether each climb construction can leave this cell. Without these a
		// dump that shows every flat move vetoed reads as "boxed in" even when
		// the real answer is "the only way out is a jump, and the jump rule
		// refused it" — which is a different bug in a different predicate.
		_, stepJump := w.canStepJumpTo(d.dx, d.dz, 1, n)
		_, parkour := w.canParkourTo(d.dx, d.dz, 2, n)
		out += fmt.Sprintf(" %s[canStand=%v isAir=%v feet=%v head=%v floor=%v feetHaz=%v headHaz=%v floorHaz=%v half=%v fenceWall=%v ladder=%v stepJump=%v parkour=%v]",
			d.name, canStand, isAir, feetS, headS, floorS, feetH, headH, floorH, halfBlock, fenceWall, ladder, stepJump, parkour)
	}
	return out
}

// appendLadderNeighbors adds vertical ladder movement and ladder-entry neighbors.
func (w *LocalWorldModel) appendLadderNeighbors(neighbors []Node, node Node) []Node {
	neighbors = w.appendLadderVertical(neighbors, node)
	neighbors = w.appendLadderEntries(neighbors, node)
	return neighbors
}

// appendLadderVertical handles climbing up/down inside a ladder column.
func (w *LocalWorldModel) appendLadderVertical(neighbors []Node, node Node) []Node {
	cx, cy, cz := node.X, node.Y, node.Z
	if !w.IsLadder(cx, cy, cz) {
		return neighbors
	}

	if !w.IsSolid(cx, cy+1, cz) && !w.IsHazard(cx, cy+1, cz) {
		neighbors = append(neighbors, Node{X: cx, Y: cy + 1, Z: cz, G: node.G + 0.8, LinkType: LinkWalk})
	}
	if !w.IsSolid(cx, cy-1, cz) && !w.IsHazard(cx, cy-1, cz) {
		if w.IsLadder(cx, cy-1, cz) || w.IsSolid(cx, cy-2, cz) {
			neighbors = append(neighbors, Node{X: cx, Y: cy - 1, Z: cz, G: node.G + 0.8, LinkType: LinkWalk})
		}
	}
	return neighbors
}

// appendLadderEntries handles entering a ladder from above or from cardinal sides.
func (w *LocalWorldModel) appendLadderEntries(neighbors []Node, node Node) []Node {
	cx, cy, cz := node.X, node.Y, node.Z
	if w.IsLadder(cx, cy, cz) {
		return neighbors
	}

	if w.IsLadder(cx, cy-1, cz) && !w.IsHazard(cx, cy-1, cz) {
		neighbors = append(neighbors, Node{X: cx, Y: cy - 1, Z: cz, G: node.G + 1.0, LinkType: LinkWalk})
	}

	for _, off := range cardinalOffsets {
		neighbors = w.appendLadderEntryFor(neighbors, node, off.dx, off.dz)
	}

	return neighbors
}

func (w *LocalWorldModel) appendLadderEntryFor(neighbors []Node, node Node, dx, dz int32) []Node {
	lx, cy, lz := node.X+dx, node.Y, node.Z+dz

	if w.IsLadder(lx, cy, lz) && !w.IsSolid(lx, cy, lz) && !w.IsSolid(lx, cy+1, lz) &&
		!w.IsHazard(lx, cy, lz) && !w.IsHazard(lx, cy+1, lz) {
		neighbors = append(neighbors, Node{X: lx, Y: cy, Z: lz, G: node.G + 1.0, LinkType: LinkWalk})
	}
	if w.IsLadder(lx, cy-1, lz) && !w.IsSolid(lx, cy-1, lz) && !w.IsSolid(lx, cy, lz) &&
		!w.IsHazard(lx, cy-1, lz) && !w.IsHazard(lx, cy, lz) {
		neighbors = append(neighbors, Node{X: lx, Y: cy - 1, Z: lz, G: node.G + 1.2, LinkType: LinkWalk})
	}

	return neighbors
}

// appendCardinalNeighbors adds walk, fall, parkour, and step neighbors for all cardinal directions.
func (w *LocalWorldModel) appendCardinalNeighbors(neighbors []Node, node Node) []Node {
	for _, off := range cardinalOffsets {
		neighbors = w.appendCardinalDirection(neighbors, node, off.dx, off.dz)
	}
	return neighbors
}

// appendCardinalDirection adds movement neighbors for one cardinal direction.
func (w *LocalWorldModel) appendCardinalDirection(neighbors []Node, node Node, dx, dz int32) []Node {
	cx, cy, cz := node.X, node.Y, node.Z
	tx, tz := cx+dx, cz+dz

	if w.canStandAt(tx, cy, tz) {
		neighbors = append(neighbors, Node{X: tx, Y: cy, Z: tz, G: node.G + 1.0, LinkType: LinkWalk})
	} else if w.isAirAt(tx, cy, tz) {
		if landY, ok := w.tryDrop(tx, cy, tz); ok {
			dropDist := float32(cy - landY)
			neighbors = append(neighbors, Node{X: tx, Y: landY, Z: tz, G: node.G + 1.0 + dropDist*0.3, LinkType: LinkFall})
		}

		neighbors = w.appendParkourIfPossible(neighbors, dx, dz, node)
	}

	neighbors = w.appendStepJumpIfPossible(neighbors, dx, dz, node)
	neighbors = w.appendStepDownJumpIfPossible(neighbors, dx, dz, node)
	neighbors = w.appendCornerParkourIfPossible(neighbors, dx, dz, node)

	return neighbors
}

func (w *LocalWorldModel) isAirAt(x, y, z int32) bool {
	return !w.IsSolid(x, y, z) && !w.IsSolid(x, y+1, z) &&
		!w.IsHazard(x, y, z) && !w.IsHazard(x, y+1, z)
}

func (w *LocalWorldModel) appendParkourIfPossible(neighbors []Node, dx, dz int32, node Node) []Node {
	for distance := int32(2); distance <= maxParkourLandingDistance; distance++ {
		if jumpNode, ok := w.canParkourTo(dx, dz, distance, node); ok {
			return append(neighbors, jumpNode)
		}
	}
	return neighbors
}

func (w *LocalWorldModel) appendStepJumpIfPossible(neighbors []Node, dx, dz int32, node Node) []Node {
	for distance := int32(1); distance <= 3; distance++ {
		if stepJumpNode, ok := w.canStepJumpTo(dx, dz, distance, node); ok {
			return append(neighbors, stepJumpNode)
		}
	}
	return neighbors
}

func (w *LocalWorldModel) appendStepDownJumpIfPossible(neighbors []Node, dx, dz int32, node Node) []Node {
	for distance := int32(2); distance <= 3; distance++ {
		if stepDownNode, ok := w.canStepDownJumpTo(dx, dz, distance, node); ok {
			return append(neighbors, stepDownNode)
		}
	}
	return neighbors
}

// appendDiagonalNeighbors adds walk/fall neighbors for all diagonal directions.
func (w *LocalWorldModel) appendDiagonalNeighbors(neighbors []Node, node Node) []Node {
	for _, off := range diagonalOffsets {
		neighbors = w.appendDiagonalDirection(neighbors, node, off.dx, off.dz)
	}
	return neighbors
}

func (w *LocalWorldModel) appendDiagonalDirection(neighbors []Node, node Node, dx, dz int32) []Node {
	cx, cy, cz := node.X, node.Y, node.Z
	if !w.diagonalSidesClear(cx, cy, cz, dx, dz) {
		return neighbors
	}

	tx, tz := cx+dx, cz+dz
	if w.canStandAt(tx, cy, tz) {
		return append(neighbors, Node{X: tx, Y: cy, Z: tz, G: node.G + 1.414, LinkType: LinkWalk})
	}
	if w.isAirAt(tx, cy, tz) {
		if landY, ok := w.tryDrop(tx, cy, tz); ok {
			dropDist := float32(cy - landY)
			neighbors = append(neighbors, Node{X: tx, Y: landY, Z: tz, G: node.G + 1.414 + dropDist*0.3, LinkType: LinkFall})
		}
	}
	return neighbors
}

func (w *LocalWorldModel) diagonalSidesClear(cx, cy, cz, dx, dz int32) bool {
	adj1Clear := !w.IsSolid(cx+dx, cy, cz) && !w.IsSolid(cx+dx, cy+1, cz) &&
		!w.IsHazard(cx+dx, cy, cz) && !w.IsHazard(cx+dx, cy+1, cz)
	adj2Clear := !w.IsSolid(cx, cy, cz+dz) && !w.IsSolid(cx, cy+1, cz+dz) &&
		!w.IsHazard(cx, cy, cz+dz) && !w.IsHazard(cx, cy+1, cz+dz)
	if !adj1Clear || !adj2Clear {
		return false
	}
	// Diagonal movement sweeps across adjacent cells. Both adjacent cells must
	// have solid floor support so the bot doesn't cut across a corner with no ground
	// and drop into a gap or pit.
	adj1Floor := w.IsSolid(cx+dx, cy-1, cz) && !w.IsHazard(cx+dx, cy-1, cz)
	adj2Floor := w.IsSolid(cx, cy-1, cz+dz) && !w.IsHazard(cx, cy-1, cz+dz)
	return adj1Floor && adj2Floor
}

// appendScaffoldNeighbors adds mining, placing, and tower-up neighbors when scaffolding is allowed.
func (w *LocalWorldModel) appendScaffoldNeighbors(neighbors []Node, node Node) []Node {
	if !w.AllowScaffold {
		return neighbors
	}

	neighbors = w.appendScaffoldTower(neighbors, node)
	for _, off := range cardinalOffsets {
		neighbors = w.appendScaffoldCardinal(neighbors, node, off.dx, off.dz)
	}
	return neighbors
}

// appendScaffoldTower offers a step straight up, which is the only way a bot
// gains height. The node it returns is the position the bot ends up STANDING at,
// one above the node it is standing on now, so the support the step needs is the
// cell the bot's legs are currently in.
//
// The old version checked one thing: that the cell two above was empty. That is
// not enough to place anything. A placement is a click on the top face of the
// block below, so that block has to exist, and the cell being filled has to be
// free — or breakable. Offering a tower that cannot be built wastes the step,
// burns the retry budget and drops the path, which is how a climb turned into a
// replan loop that never climbed.
//
// Nothing here breaks anything. Whether an obstruction is worth breaking is a
// judgement the executor makes with the block in front of it, where the name is
// known for certain rather than inferred from a solidity flag.
func (w *LocalWorldModel) appendScaffoldTower(neighbors []Node, node Node) []Node {
	cx, cy, cz := node.X, node.Y, node.Z

	// The cell the new block goes into is the one the body is in, and the face it
	// is placed against is the block the body is standing on.
	supportY, clickY := cy, cy-1
	if w.IsHazard(cx, supportY, cz) || w.IsHazard(cx, supportY+1, cz) || w.IsHazard(cx, clickY, cz) {
		return neighbors
	}

	// The face to click. Without it the block would float and the server would
	// refuse the placement.
	//
	// The one exception is the block this node's own action is about to place. A
	// "place" step fills exactly the cell below the node it stands on, so the next
	// tower step clicks that cell — and the planner is looking at a world where
	// it is still air, because nothing has been placed yet. Requiring it to be
	// solid already is what capped every climb at a single block: the first tower
	// step was planned, and the second could not be, so a five-block ascent came
	// back as a one-block path and then nothing at all.
	if !w.IsSolid(cx, clickY, cz) && node.Action != "place" {
		return neighbors
	}

	// A column closed by something not worth breaking gets no neighbour at all,
	// so the route goes around instead of being planned through and abandoned
	// three attempts later.
	if w.IsSolid(cx, supportY, cz) && !w.IsBreakable(cx, supportY, cz) {
		return neighbors
	}
	// Headroom for the body once it is standing up there.
	if w.IsSolid(cx, supportY+1, cz) && !w.IsBreakable(cx, supportY+1, cz) {
		return neighbors
	}

	return append(neighbors, Node{X: cx, Y: cy + 1, Z: cz, G: node.G + 12.0, Action: "place", LinkType: LinkWalk})
}

func (w *LocalWorldModel) appendScaffoldCardinal(neighbors []Node, node Node, dx, dz int32) []Node {
	tx, cy, tz := node.X+dx, node.Y, node.Z+dz

	if w.canScaffoldMine(tx, cy, tz) {
		neighbors = append(neighbors, Node{X: tx, Y: cy, Z: tz, G: node.G + 15.0, Action: "mine", LinkType: LinkWalk})
	}
	if w.canScaffoldBridge(tx, cy, tz) {
		neighbors = append(neighbors, Node{X: tx, Y: cy, Z: tz, G: node.G + 10.0, Action: "place", LinkType: LinkWalk})
	}

	return neighbors
}

func (w *LocalWorldModel) canScaffoldMine(tx, cy, tz int32) bool {
	if !(w.IsSolid(tx, cy, tz) || w.IsSolid(tx, cy+1, tz)) {
		return false
	}
	if !w.IsSolid(tx, cy-1, tz) || w.IsHazard(tx, cy-1, tz) {
		return false
	}
	return w.IsBreakable(tx, cy, tz) && w.IsBreakable(tx, cy+1, tz)
}

func (w *LocalWorldModel) canScaffoldBridge(tx, cy, tz int32) bool {
	return !w.IsSolid(tx, cy-1, tz) && !w.IsSolid(tx, cy, tz) && !w.IsSolid(tx, cy+1, tz) &&
		!w.IsHazard(tx, cy-1, tz) && !w.IsHazard(tx, cy, tz) && !w.IsHazard(tx, cy+1, tz)
}

// canStandAt reports whether the bot can safely stand at (x, y, z).
func (w *LocalWorldModel) canStandAt(x, y, z int32) bool {
	if !w.IsSolid(x, y, z) && !w.IsSolid(x, y+1, z) &&
		!w.IsHazard(x, y, z) && !w.IsHazard(x, y+1, z) {
		if w.IsSolid(x, y-1, z) && !w.IsHazard(x, y-1, z) {
			if w.isClimbableSurface(x, y-1, z) {
				return true
			}
			if w.wallBlocksStanding(x, y, z) {
				return false
			}
			return true
		}
		if w.IsLadder(x, y, z) {
			return true
		}
	}
	return false
}

// wallBlocksStanding reports whether the fence or wall under (x, y, z) stands in
// the body's way instead of carrying it.
//
// A wall or fence is 1.5 blocks tall, so its collision top sits half a block
// above the top of its own cell. Two things follow, and the bot has to be
// treated correctly on both:
//
//   - A body whose feet are at node level y is INSIDE a wall occupying y-1: the
//     wall reaches y+0.5. Nobody stands there, and planning a route through such
//     a cell walks the bot into a wall forever. That is the veto that has to
//     stay.
//   - But the bot itself is very often standing on one of those walls. A hub
//     built from granite_wall / tuff_brick_wall tiles decodes every walkway tile
//     that way, so the bot's feet rest at y+0.5 — half a block above the node
//     level the start calculation derives from floor(feet). Vetoing those
//     unconditionally meant the bot's own foothold read as unusable:
//     canStandAt returned false for every neighbour, GetNeighbors returned
//     nothing, and A* dead-ended with zero nodes on a surface the bot was
//     visibly standing on.
//
// So the test is not "is it a wall" but "is its top too high to walk onto from
// where the bot is standing". Climbing onto a wall is refused; walking across
// the tops of walls is allowed, because there the wall is the floor rather than
// a barrier.
func (w *LocalWorldModel) wallBlocksStanding(x, y, z int32) bool {
	if !w.isFenceOrWall(x, y-1, z) {
		return false
	}
	// A wall or fence is 1.5 blocks tall (top at y+0.5), so a standing body's head
	// reaches y+2.3 and requires the ceiling cell at y+2 to be clear.
	return w.IsSolid(x, y+2, z) || w.IsHazard(x, y+2, z)
}

// wallTopOverCell is how far a wall or fence's collision top rises above the top
// of the cell it occupies (1.5 blocks tall in a 1-block cell).
const wallTopOverCell = 0.5

// maxStepUp is the tallest ledge a player walks up without jumping (0.6 blocks,
// Minecraft's step height).
//
// It matters because the two footing heights a wall surface can present are half
// a block apart. A bot standing on ordinary ground has its feet at exactly the
// node level; a bot standing on a wall has them half a block higher. Comparing
// those two heights with nothing else — which is what this rule did first —
// vetoes the ordinary-ground-to-wall-surface transition. That transition is a
// half-block step, which a player makes without thinking, and the bot refused it:
// standing at the edge of a lobby paved in tuff_brick_wall, every tile inside it
// read as unclimbable and A* could not enter the lobby at all.
const maxStepUp = 0.6

// footingTopY is the height the bot's feet currently rest at, which is what a
// neighbouring floor has to be compared against.
//
// It is not always the node level. Standing on a wall or fence puts the feet
// half a block above the top of the floor's cell, and that difference is the
// whole question — so it is read from the world rather than assumed.
func (w *LocalWorldModel) footingTopY() float64 {
	if !w.hasBounds {
		// No trip in progress, so there is no known footing. Refuse the climb
		// rather than invent one.
		return math.Inf(-1)
	}
	if w.isFenceOrWall(w.startX, w.startY-1, w.startZ) {
		return float64(w.startY) + wallTopOverCell
	}
	return float64(w.startY)
}

// tryDrop searches up to maxSafeStandDrop blocks down for a safe landing.
func (w *LocalWorldModel) tryDrop(x, y, z int32) (int32, bool) {
	for standDrop := int32(1); standDrop <= maxSafeStandDrop; standDrop++ {
		landY := y - standDrop
		floorY := landY - 1

		if w.IsHazard(x, landY, z) || w.IsHazard(x, landY+1, z) {
			return 0, false
		}

		if w.IsSolid(x, floorY, z) {
			if w.IsHazard(x, floorY, z) {
				return 0, false
			}
			if !w.IsSolid(x, landY, z) && !w.IsSolid(x, landY+1, z) {
				return landY, true
			}
			return 0, false
		}
	}
	return 0, false
}

// canParkourTo checks if a same-level gap jump of distance blocks is valid.
func (w *LocalWorldModel) canParkourTo(dx, dz, distance int32, node Node) (Node, bool) {
	cx, cy, cz := node.X, node.Y, node.Z
	if distance < 2 || distance > maxParkourLandingDistance {
		return Node{}, false
	}
	if !w.parkourStartClear(cx, cy, cz) {
		return Node{}, false
	}
	if !w.parkourGapClear(cx, cy, cz, dx, dz, distance) {
		return Node{}, false
	}

	lx := cx + dx*distance
	lz := cz + dz*distance
	if !w.canStandAt(lx, cy, lz) || w.IsSolid(lx, cy+2, lz) {
		return Node{}, false
	}
	return Node{X: lx, Y: cy, Z: lz, G: node.G + 2.5 + float32(distance)*1.2, LinkType: LinkJump}, true
}

func (w *LocalWorldModel) parkourStartClear(cx, cy, cz int32) bool {
	if w.IsHazard(cx, cy-1, cz) {
		return false
	}
	if !(w.hasBounds && cx == w.startX && cy == w.startY && cz == w.startZ) {
		if !w.IsSolid(cx, cy-1, cz) {
			return false
		}
	}
	return !w.IsSolid(cx, cy+1, cz) && !w.IsSolid(cx, cy+2, cz)
}

func (w *LocalWorldModel) parkourGapClear(cx, cy, cz, dx, dz, distance int32) bool {
	for step := int32(1); step < distance; step++ {
		gx := cx + dx*step
		gz := cz + dz*step
		if w.IsHazard(gx, cy, gz) || w.IsHazard(gx, cy-1, gz) {
			return false
		}
		if w.IsSolid(gx, cy, gz) || w.IsSolid(gx, cy+1, gz) || w.IsSolid(gx, cy+2, gz) || w.IsSolid(gx, cy+3, gz) {
			return false
		}
		if w.IsSolid(gx, cy-1, gz) {
			return false
		}
	}
	return true
}

// canStepJumpTo checks if a step jump (Y+1) with/without a gap is valid.
func (w *LocalWorldModel) canStepJumpTo(dx, dz, distance int32, node Node) (Node, bool) {
	cx, cy, cz := node.X, node.Y, node.Z
	if distance < 1 || distance > 3 {
		return Node{}, false
	}
	if !w.stepJumpStartClear(cx, cy, cz) {
		return Node{}, false
	}

	tx := cx + dx*distance
	tz := cz + dz*distance
	ty := cy + 1

	if !w.stepJumpTargetClear(tx, ty, tz, node) {
		return Node{}, false
	}
	if !w.stepJumpGapClear(cx, cy, cz, dx, dz, distance) {
		return Node{}, false
	}

	cost := float32(3.0) + float32(distance)*1.5
	if distance == 1 && (w.isHalfBlock(cx, cy-1, cz) || w.isHalfBlock(tx, ty-1, tz)) {
		cost = 1.2
	}
	return Node{X: tx, Y: ty, Z: tz, G: node.G + cost, LinkType: LinkStepJump}, true
}

func (w *LocalWorldModel) stepJumpStartClear(cx, cy, cz int32) bool {
	if w.IsHazard(cx, cy-1, cz) {
		return false
	}
	// At the bot's own current start position, the bot is already standing there.
	// Allow jumping out of a pit or hole even if the block beneath reads as non-solid.
	if !(w.hasBounds && cx == w.startX && cy == w.startY && cz == w.startZ) {
		if !w.IsSolid(cx, cy-1, cz) {
			return false
		}
	}
	return !w.IsSolid(cx, cy+1, cz) && !w.IsSolid(cx, cy+2, cz)
}

func (w *LocalWorldModel) stepJumpTargetClear(tx, ty, tz int32, from Node) bool {
	if !w.IsSolid(tx, ty-1, tz) || w.IsHazard(tx, ty-1, tz) {
		return false
	}
	if w.isFenceOrWall(tx, ty-1, tz) {
		// A wall or fence is 1.5 blocks tall. Stepping onto it from below is
		// only possible when climbing from a stair or slab, where the step-up
		// is within reach. Jumping onto a wall barrier from ordinary ground or
		// another wall is refused.
		if !w.isHalfBlock(from.X, from.Y-1, from.Z) {
			return false
		}
	}
	return !w.IsSolid(tx, ty, tz) && !w.IsSolid(tx, ty+1, tz) &&
		!w.IsHazard(tx, ty, tz) && !w.IsHazard(tx, ty+1, tz)
}

func (w *LocalWorldModel) stepJumpGapClear(cx, cy, cz, dx, dz, distance int32) bool {
	for step := int32(1); step < distance; step++ {
		gx := cx + dx*step
		gz := cz + dz*step
		if w.IsSolid(gx, cy, gz) || w.IsSolid(gx, cy+1, gz) || w.IsSolid(gx, cy+2, gz) || w.IsSolid(gx, cy+3, gz) {
			return false
		}
		if w.IsHazard(gx, cy, gz) || w.IsHazard(gx, cy+1, gz) || w.IsHazard(gx, cy+2, gz) {
			return false
		}
		if w.IsSolid(gx, cy-1, gz) {
			return false
		}
	}
	return true
}

// appendCornerParkourIfPossible adds lateral/corner jump nodes around a blocking wall.
func (w *LocalWorldModel) appendCornerParkourIfPossible(neighbors []Node, dx, dz int32, node Node) []Node {
	cx, cy, cz := node.X, node.Y, node.Z
	// Only check if direct front block is solid (wall blocking forward path)
	frontX, frontZ := cx+dx, cz+dz
	if !w.IsSolid(frontX, cy, frontZ) && !w.IsSolid(frontX, cy+1, frontZ) {
		return neighbors
	}

	// Lateral offsets perpendicular to forward direction
	var sides []struct{ sx, sz int32 }
	if dx != 0 {
		sides = []struct{ sx, sz int32 }{{0, 1}, {0, -1}, {0, 2}, {0, -2}}
	} else {
		sides = []struct{ sx, sz int32 }{{1, 0}, {-1, 0}, {2, 0}, {-2, 0}}
	}

	for _, side := range sides {
		// Check diagonal landing spot around corner (dx, side)
		lx := cx + dx
		lz := cz + dz
		if side.sx != 0 {
			lx += side.sx
		}
		if side.sz != 0 {
			lz += side.sz
		}

		if w.canStandAt(lx, cy, lz) && !w.IsSolid(lx, cy+2, lz) {
			// The whole swept arc has to be open, not just the first cell of it.
			// A corner jump threads a one-block-wide body between the corner and
			// whatever is beside it, and checking a single midpoint let a
			// two-block lateral swing pass straight through a wall on its way.
			if w.CornerArcClear(cx, cy, cz, lx, lz) {
				neighbors = append(neighbors, Node{
					X:        lx,
					Y:        cy,
					Z:        lz,
					G:        node.G + 3.2,
					LinkType: LinkJump,
				})
			}
		}
	}
	return neighbors
}

// CornerArcClear walks the cells the bot's body sweeps through while rounding
// a corner and requires every one of them to be open.
//
// Clearance is checked on both sides of the move, which is what makes this a
// corner rule rather than a line-of-sight rule. A body is a block wide: getting
// from the bot's cell to the landing cell means every cell on the way has to be
// free at feet and head height at once. Testing only the midpoint let the two
// block offset clear a one block gap and a wall at the same time, because
// neither the near side nor the far side of the corner was ever checked for the
// whole way through.
func (w *LocalWorldModel) CornerArcClear(cx, cy, cz, lx, lz int32) bool {
	dx := int64(lx - cx)
	dz := int64(lz - cz)

	// Walk the longer axis in whole steps. For a corner move that lands on both
	// axes this traces the diagonal the body actually sweeps.
	span := dx
	for _, v := range []int64{-dx, -dz, dz} {
		if v > span {
			span = v
		}
	}
	if span < 1 {
		span = 1
	}

	// Step 0 is the bot's own cell and is deliberately not tested: it is
	// standing there.
	for step := int64(1); step <= span; step++ {
		x := cx + int32(dx*step/span)
		z := cz + int32(dz*step/span)
		if !w.isAirAt(x, cy, z) {
			return false
		}
	}
	return true
}

// canStepDownJumpTo checks if a step-down jump (Y-1) over a gap is valid.
func (w *LocalWorldModel) canStepDownJumpTo(dx, dz, distance int32, node Node) (Node, bool) {
	cx, cy, cz := node.X, node.Y, node.Z
	if distance < 2 || distance > 3 {
		return Node{}, false
	}
	if !w.stepJumpStartClear(cx, cy, cz) {
		return Node{}, false
	}

	tx := cx + dx*distance
	tz := cz + dz*distance
	ty := cy - 1

	if !w.stepDownJumpTargetClear(tx, ty, tz) {
		return Node{}, false
	}
	if !w.stepDownJumpGapClear(cx, cy, cz, dx, dz, distance) {
		return Node{}, false
	}

	return Node{X: tx, Y: ty, Z: tz, G: node.G + 2.2 + float32(distance)*1.0, LinkType: LinkJump}, true
}

func (w *LocalWorldModel) stepDownJumpTargetClear(tx, ty, tz int32) bool {
	if !w.IsSolid(tx, ty-1, tz) || w.IsHazard(tx, ty-1, tz) {
		return false
	}
	if w.IsSolid(tx, ty, tz) || w.IsSolid(tx, ty+1, tz) || w.IsSolid(tx, ty+2, tz) {
		return false
	}
	return !w.IsHazard(tx, ty, tz) && !w.IsHazard(tx, ty+1, tz)
}

func (w *LocalWorldModel) stepDownJumpGapClear(cx, cy, cz, dx, dz, distance int32) bool {
	for step := int32(1); step < distance; step++ {
		gx := cx + dx*step
		gz := cz + dz*step
		if w.IsSolid(gx, cy, gz) || w.IsSolid(gx, cy+1, gz) || w.IsSolid(gx, cy+2, gz) {
			return false
		}
		if w.IsHazard(gx, cy, gz) || w.IsHazard(gx, cy+1, gz) {
			return false
		}
		if w.IsSolid(gx, cy-1, gz) {
			return false
		}
	}
	return true
}

// runtimeIDToStateAvailable reports whether dragonfly's runtime-ID converter is
// linked into this binary.
//
// chunk.RuntimeIDToState is a package-level func var that dragonfly's world
// package fills in at init. A binary that never links it — this package's own
// consumers, a tool, a test — leaves it nil, and calling it takes the process
// down from inside the search. blockNameFor in world.go already guards its own
// call; isHalfBlock and isClimbableSurface reach the same var directly and
// have to ask the same question, or a bot whose movement package does not
// import the whole block palette dies on the first neighbour it expands.
//
// The answer it produces is "unknown", which is the safe direction: an unnamed
// block is not assumed to be a slab, and is not assumed to be a ladder. canStandAt
// treats both answers as "a full block", so the search still crosses a floor it
// cannot name.
func runtimeIDToStateAvailable() bool {
	return chunk.RuntimeIDToState != nil
}

func (w *LocalWorldModel) isHalfBlock(x, y, z int32) bool {
	if w.chunkQuerier == nil {
		return false
	}
	rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
	if !loaded {
		return false
	}
	name, ok := blockNameFor(w.chunkQuerier, rid)
	if !ok {
		return false
	}
	if strings.Contains(name, "stair") || strings.Contains(name, "slab") || strings.Contains(name, "step") {
		return true
	}
	if runtimeIDToStateAvailable() {
		if _, properties, ok := chunk.RuntimeIDToState(rid); ok && properties != nil {
			if half, ok := properties["half"]; ok {
				if half == "top" || half == "bottom" {
					return true
				}
			}
		}
	}
	return false
}

func (w *LocalWorldModel) isClimbableSurface(x, y, z int32) bool {
	if w.chunkQuerier == nil {
		return false
	}
	rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
	if !loaded {
		return false
	}
	name, ok := blockNameFor(w.chunkQuerier, rid)
	if !ok {
		return false
	}
	return name == "minecraft:ladder" || name == "minecraft:scaffolding" ||
		strings.Contains(name, "vine") || strings.Contains(name, "rope")
}

func (w *LocalWorldModel) isFenceOrWall(x, y, z int32) bool {
	if w.chunkQuerier == nil {
		return false
	}
	rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
	if !loaded {
		return false
	}
	name, ok := blockNameFor(w.chunkQuerier, rid)
	if !ok {
		return false
	}
	return isFenceOrWallName(name)
}

func isFenceOrWallName(name string) bool {
	return strings.HasSuffix(name, "_wall") ||
		strings.HasSuffix(name, "_fence") ||
		strings.HasSuffix(name, "_fence_gate") ||
		name == "minecraft:fence" ||
		name == "minecraft:fence_gate" ||
		name == "minecraft:cobblestone_wall"
}
