// Package pathfinder implements the 3D grid pathfinder used by the bot,
// including node expansion, A* search, and movement smoothing helpers.
package pathfinder

import (
	"fmt"
	"strings"

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
	if len(neighbors) == 0 {
		fmt.Printf("[GetNeighbors empty] node=(%d,%d,%d) ladder=%d cardinal=%d diag=%d scaffold=%d\n",
			node.X, node.Y, node.Z, ladderN, cardinalN, diagN, scaffoldN)
	}
	return neighbors
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
		ladder := w.IsLadder(tx, n.Y, tz)
		out += fmt.Sprintf(" %s[canStand=%v isAir=%v feet=%v head=%v floor=%v feetHaz=%v headHaz=%v floorHaz=%v half=%v ladder=%v]",
			d.name, canStand, isAir, feetS, headS, floorS, feetH, headH, floorH, halfBlock, ladder)
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
	return adj1Clear && adj2Clear
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

func (w *LocalWorldModel) appendScaffoldTower(neighbors []Node, node Node) []Node {
	cx, cy, cz := node.X, node.Y, node.Z
	if !w.IsSolid(cx, cy+2, cz) && !w.IsHazard(cx, cy+2, cz) {
		return append(neighbors, Node{X: cx, Y: cy + 1, Z: cz, G: node.G + 12.0, Action: "place", LinkType: LinkWalk})
	}
	return neighbors
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
			if w.isHalfBlock(x, y-1, z) {
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
	if w.IsHazard(cx, cy-1, cz) || !w.IsSolid(cx, cy-1, cz) {
		return false
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

	if !w.stepJumpTargetClear(tx, ty, tz) {
		return Node{}, false
	}
	if !w.stepJumpGapClear(cx, cy, cz, dx, dz, distance) {
		return Node{}, false
	}

	return Node{X: tx, Y: ty, Z: tz, G: node.G + 3.0 + float32(distance)*1.5, LinkType: LinkStepJump}, true
}

func (w *LocalWorldModel) stepJumpStartClear(cx, cy, cz int32) bool {
	if w.IsHazard(cx, cy-1, cz) || !w.IsSolid(cx, cy-1, cz) {
		return false
	}
	return !w.IsSolid(cx, cy+1, cz) && !w.IsSolid(cx, cy+2, cz)
}

func (w *LocalWorldModel) stepJumpTargetClear(tx, ty, tz int32) bool {
	if !w.IsSolid(tx, ty-1, tz) || w.IsHazard(tx, ty-1, tz) {
		return false
	}
	if w.isHalfBlock(tx, ty-1, tz) {
		return !w.IsSolid(tx, ty+1, tz) && !w.IsSolid(tx, ty+2, tz) &&
			!w.IsHazard(tx, ty+1, tz) && !w.IsHazard(tx, ty+2, tz)
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

func (w *LocalWorldModel) isHalfBlock(x, y, z int32) bool {
	if w.chunkQuerier == nil {
		return false
	}
	rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
	if !loaded {
		return false
	}
	name, properties, ok := chunk.RuntimeIDToState(rid)
	if !ok {
		return false
	}
	if strings.HasSuffix(name, "_slab") || strings.HasSuffix(name, "_stairs") || strings.HasSuffix(name, "_step") {
		return true
	}
	if strings.Contains(name, "stair") && strings.Contains(name, "outer") ||
		strings.Contains(name, "stair") && strings.Contains(name, "inner") {
		return true
	}
	if properties != nil {
		if half, ok := properties["half"]; ok {
			if half == "top" || half == "bottom" {
				return true
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
	name, _, ok := chunk.RuntimeIDToState(rid)
	if !ok {
		return false
	}
	return name == "minecraft:ladder" || name == "minecraft:scaffolding" ||
		strings.Contains(name, "vine") || strings.Contains(name, "rope")
}
