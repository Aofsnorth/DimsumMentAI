package pathfinder

import (
	"strings"

	"github.com/df-mc/dragonfly/server/world/chunk"
)

// GetNeighbors mengembalikan node-node tetangga yang valid untuk dilalui bot.
func (w *LocalWorldModel) GetNeighbors(node Node) []Node {
	var neighbors []Node
	cx, cy, cz := node.X, node.Y, node.Z

	// Cardinal directions
	cardinalOffsets := []struct{ dx, dz int32 }{
		{0, -1}, {0, 1}, {1, 0}, {-1, 0},
	}

	// Diagonal directions
	diagonalOffsets := []struct{ dx, dz int32 }{
		{1, 1}, {1, -1}, {-1, 1}, {-1, -1},
	}

	// Helper: check if a position is safe to stand (feet + head are passable, floor is solid OR target is a ladder)
	canStandAt := func(x, y, z int32) bool {
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

	const maxSafeStandDrop int32 = 3

	// Helper: try to find a controlled step down. A one-block lower floor is
	// at y-2 because y is the bot's current feet position.
	// Returns (landingY, found)
	tryDrop := func(x, y, z int32) (int32, bool) {
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

	const maxParkourLandingDistance int32 = 4

	// Helper: check if a gap jump to the same Y-level is valid
	canParkourTo := func(dx, dz, distance int32) (Node, bool) {
		if distance < 2 || distance > maxParkourLandingDistance {
			return Node{}, false
		}
		if w.IsHazard(cx, cy-1, cz) || !w.IsSolid(cx, cy-1, cz) {
			return Node{}, false
		}
		if w.IsSolid(cx, cy+1, cz) || w.IsSolid(cx, cy+2, cz) {
			return Node{}, false
		}

		for step := int32(1); step < distance; step++ {
			gx := cx + dx*step
			gz := cz + dz*step
			if w.IsHazard(gx, cy, gz) || w.IsHazard(gx, cy-1, gz) {
				return Node{}, false
			}
			// Check clearance up to Y+3 relative to start cy to ensure overhead head room when jumping
			if w.IsSolid(gx, cy, gz) || w.IsSolid(gx, cy+1, gz) || w.IsSolid(gx, cy+2, gz) || w.IsSolid(gx, cy+3, gz) {
				return Node{}, false
			}
			if w.IsSolid(gx, cy-1, gz) {
				return Node{}, false
			}
		}

		lx := cx + dx*distance
		lz := cz + dz*distance
		if !canStandAt(lx, cy, lz) {
			return Node{}, false
		}
		// Landing block headroom check
		if w.IsSolid(lx, cy+2, lz) {
			return Node{}, false
		}
		return Node{X: lx, Y: cy, Z: lz, G: node.G + 2.5 + float32(distance)*1.2, LinkType: LinkJump}, true
	}

	// Helper: check if a step jump (Y+1) with/without a gap is valid
	canStepJumpTo := func(dx, dz, distance int32) (Node, bool) {
		if distance < 1 || distance > 3 { // direct step-up (1), 1-block gap step-up (2), 2-block gap step-up (3)
			return Node{}, false
		}
		if w.IsHazard(cx, cy-1, cz) || !w.IsSolid(cx, cy-1, cz) {
			return Node{}, false
		}
		if w.IsSolid(cx, cy+1, cz) || w.IsSolid(cx, cy+2, cz) {
			return Node{}, false
		}

		tx := cx + dx*distance
		tz := cz + dz*distance
		ty := cy + 1

		// Target floor must be solid, target feet and head must be clear
		if !w.IsSolid(tx, ty-1, tz) || w.IsHazard(tx, ty-1, tz) {
			return Node{}, false
		}
		// If target floor is a half-block (slab/stairs), we can stand on top of it at y+1
		// Otherwise require full block clearance
		if !w.isHalfBlock(tx, ty-1, tz) {
			if w.IsSolid(tx, ty, tz) || w.IsSolid(tx, ty+1, tz) {
				return Node{}, false
			}
			if w.IsHazard(tx, ty, tz) || w.IsHazard(tx, ty+1, tz) {
				return Node{}, false
			}
		} else {
			// Half-block floor: check head clearance at y+2 (one above half-block)
			if w.IsSolid(tx, ty+1, tz) || w.IsSolid(tx, ty+2, tz) {
				return Node{}, false
			}
			if w.IsHazard(tx, ty+1, tz) || w.IsHazard(tx, ty+2, tz) {
				return Node{}, false
			}
		}

		// Check intermediate gap blocks
		for step := int32(1); step < distance; step++ {
			gx := cx + dx*step
			gz := cz + dz*step
			if w.IsSolid(gx, cy, gz) || w.IsSolid(gx, cy+1, gz) || w.IsSolid(gx, cy+2, gz) || w.IsSolid(gx, cy+3, gz) {
				return Node{}, false
			}
			if w.IsHazard(gx, cy, gz) || w.IsHazard(gx, cy+1, gz) || w.IsHazard(gx, cy+2, gz) {
				return Node{}, false
			}
			if w.IsSolid(gx, cy-1, gz) {
				return Node{}, false
			}
		}

		return Node{X: tx, Y: ty, Z: tz, G: node.G + 3.0 + float32(distance)*1.5, LinkType: LinkStepJump}, true
	}

	// Helper: check if a step-down jump (Y-1) with a gap is valid
	canStepDownJumpTo := func(dx, dz, distance int32) (Node, bool) {
		if distance < 2 || distance > 3 { // 1-block gap (2), 2-block gap (3)
			return Node{}, false
		}
		if w.IsHazard(cx, cy-1, cz) || !w.IsSolid(cx, cy-1, cz) {
			return Node{}, false
		}
		if w.IsSolid(cx, cy+1, cz) || w.IsSolid(cx, cy+2, cz) {
			return Node{}, false
		}

		tx := cx + dx*distance
		tz := cz + dz*distance
		ty := cy - 1

		// Target floor must be solid
		if !w.IsSolid(tx, ty-1, tz) || w.IsHazard(tx, ty-1, tz) {
			return Node{}, false
		}
		// Target feet, head and ceiling must be clear
		if w.IsSolid(tx, ty, tz) || w.IsSolid(tx, ty+1, tz) || w.IsSolid(tx, ty+2, tz) {
			return Node{}, false
		}
		if w.IsHazard(tx, ty, tz) || w.IsHazard(tx, ty+1, tz) {
			return Node{}, false
		}

		// Check intermediate gap blocks
		for step := int32(1); step < distance; step++ {
			gx := cx + dx*step
			gz := cz + dz*step
			if w.IsSolid(gx, cy, gz) || w.IsSolid(gx, cy+1, gz) || w.IsSolid(gx, cy+2, gz) {
				return Node{}, false
			}
			if w.IsHazard(gx, cy, gz) || w.IsHazard(gx, cy+1, gz) {
				return Node{}, false
			}
			if w.IsSolid(gx, cy-1, gz) {
				return Node{}, false
			}
		}

		return Node{X: tx, Y: ty, Z: tz, G: node.G + 2.2 + float32(distance)*1.0, LinkType: LinkJump}, true
	}

	// Process vertical ladder climbing
	if w.IsLadder(cx, cy, cz) {
		// 1. Climb Up: target must be a ladder, or a valid standing spot
		if !w.IsSolid(cx, cy+1, cz) && !w.IsHazard(cx, cy+1, cz) {
			neighbors = append(neighbors, Node{X: cx, Y: cy + 1, Z: cz, G: node.G + 0.8, LinkType: LinkWalk})
		}
		// 2. Climb Down: target must be a ladder, or a valid standing spot
		if !w.IsSolid(cx, cy-1, cz) && !w.IsHazard(cx, cy-1, cz) {
			if w.IsLadder(cx, cy-1, cz) || w.IsSolid(cx, cy-2, cz) { // Note: Y-1 of target is cy-2
				neighbors = append(neighbors, Node{X: cx, Y: cy - 1, Z: cz, G: node.G + 0.8, LinkType: LinkWalk})
			}
		}
	}

	// Enter ladder from above
	if !w.IsLadder(cx, cy, cz) {
		// Check if we can step down into a ladder column directly below
		if w.IsLadder(cx, cy-1, cz) && !w.IsHazard(cx, cy-1, cz) {
			neighbors = append(neighbors, Node{X: cx, Y: cy - 1, Z: cz, G: node.G + 1.0, LinkType: LinkWalk})
		}
		// Check cardinal-adjacent ladder entries
		for _, off := range cardinalOffsets {
			lx, lz := cx+off.dx, cz+off.dz
			if w.IsLadder(lx, cy, lz) && !w.IsSolid(lx, cy, lz) && !w.IsSolid(lx, cy+1, lz) &&
				!w.IsHazard(lx, cy, lz) && !w.IsHazard(lx, cy+1, lz) {
				neighbors = append(neighbors, Node{X: lx, Y: cy, Z: lz, G: node.G + 1.0, LinkType: LinkWalk})
			}
			// Adjacent block one level down has a ladder
			if w.IsLadder(lx, cy-1, lz) && !w.IsSolid(lx, cy-1, lz) && !w.IsSolid(lx, cy, lz) &&
				!w.IsHazard(lx, cy-1, lz) && !w.IsHazard(lx, cy, lz) {
				neighbors = append(neighbors, Node{X: lx, Y: cy - 1, Z: lz, G: node.G + 1.2, LinkType: LinkWalk})
			}
		}
	}

	// Process cardinal directions
	for _, off := range cardinalOffsets {
		tx := cx + off.dx
		tz := cz + off.dz

		// 1. Walk straight (same level)
		if canStandAt(tx, cy, tz) {
			neighbors = append(neighbors, Node{X: tx, Y: cy, Z: tz, G: node.G + 1.0, LinkType: LinkWalk})
		} else if !w.IsSolid(tx, cy, tz) && !w.IsSolid(tx, cy+1, tz) &&
			!w.IsHazard(tx, cy, tz) && !w.IsHazard(tx, cy+1, tz) {
			// Target has air at body level but no floor — try dropping
			if landY, ok := tryDrop(tx, cy, tz); ok {
				dropDist := float32(cy - landY)
				neighbors = append(neighbors, Node{X: tx, Y: landY, Z: tz, G: node.G + 1.0 + dropDist*0.3, LinkType: LinkFall})
			}

			// Also try parkour jump over gap.
			for distance := int32(2); distance <= maxParkourLandingDistance; distance++ {
				if jumpNode, ok := canParkourTo(off.dx, off.dz, distance); ok {
					neighbors = append(neighbors, jumpNode)
					break
				}
			}
		}

		// 2. Step up / Step jump (adjacent Y+1, or over gap)
		for distance := int32(1); distance <= 3; distance++ {
			if stepJumpNode, ok := canStepJumpTo(off.dx, off.dz, distance); ok {
				neighbors = append(neighbors, stepJumpNode)
				break
			}
		}

		// 3. Step down jump (over gap)
		for distance := int32(2); distance <= 3; distance++ {
			if stepDownNode, ok := canStepDownJumpTo(off.dx, off.dz, distance); ok {
				neighbors = append(neighbors, stepDownNode)
				break
			}
		}
	}

	// Process diagonal directions
	for _, off := range diagonalOffsets {
		tx := cx + off.dx
		tz := cz + off.dz

		// Diagonal flat/drop/step-up check
		adj1Clear := !w.IsSolid(cx+off.dx, cy, cz) && !w.IsSolid(cx+off.dx, cy+1, cz) &&
			!w.IsHazard(cx+off.dx, cy, cz) && !w.IsHazard(cx+off.dx, cy+1, cz)
		adj2Clear := !w.IsSolid(cx, cy, cz+off.dz) && !w.IsSolid(cx, cy+1, cz+off.dz) &&
			!w.IsHazard(cx, cy, cz+off.dz) && !w.IsHazard(cx, cy+1, cz+off.dz)

		if adj1Clear && adj2Clear {
			// 1. Diagonal flat walk
			if canStandAt(tx, cy, tz) {
				neighbors = append(neighbors, Node{X: tx, Y: cy, Z: tz, G: node.G + 1.414, LinkType: LinkWalk})
			} else if !w.IsSolid(tx, cy, tz) && !w.IsSolid(tx, cy+1, tz) &&
				!w.IsHazard(tx, cy, tz) && !w.IsHazard(tx, cy+1, tz) {
				if landY, ok := tryDrop(tx, cy, tz); ok {
					dropDist := float32(cy - landY)
					neighbors = append(neighbors, Node{X: tx, Y: landY, Z: tz, G: node.G + 1.414 + dropDist*0.3, LinkType: LinkFall})
				}
			}
		}
	}

	// Process scaffolding and mining when AllowScaffold is enabled
	if w.AllowScaffold {
		// 1. Tower up in place
		if !w.IsSolid(cx, cy+2, cz) && !w.IsHazard(cx, cy+2, cz) {
			neighbors = append(neighbors, Node{X: cx, Y: cy + 1, Z: cz, G: node.G + 12.0, Action: "place", LinkType: LinkWalk})
		}

		for _, off := range cardinalOffsets {
			tx := cx + off.dx
			tz := cz + off.dz

			// 2. Mine forward
			isTargetFeetSolid := w.IsSolid(tx, cy, tz)
			isTargetHeadSolid := w.IsSolid(tx, cy+1, tz)
			if (isTargetFeetSolid || isTargetHeadSolid) && w.IsSolid(tx, cy-1, tz) && !w.IsHazard(tx, cy-1, tz) {
				if w.IsBreakable(tx, cy, tz) && w.IsBreakable(tx, cy+1, tz) {
					neighbors = append(neighbors, Node{X: tx, Y: cy, Z: tz, G: node.G + 15.0, Action: "mine", LinkType: LinkWalk})
				}
			}

			// 3. Bridge forward
			if !w.IsSolid(tx, cy-1, tz) && !w.IsSolid(tx, cy, tz) && !w.IsSolid(tx, cy+1, tz) &&
				!w.IsHazard(tx, cy-1, tz) && !w.IsHazard(tx, cy, tz) && !w.IsHazard(tx, cy+1, tz) {
				neighbors = append(neighbors, Node{X: tx, Y: cy, Z: tz, G: node.G + 10.0, Action: "place", LinkType: LinkWalk})
			}
		}
	}

	return neighbors
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
