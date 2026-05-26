package pathfinder

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
				return true
			}
			if w.IsLadder(x, y, z) {
				return true
			}
		}
		return false
	}

	// Helper: try to find a drop landing spot (max 3 blocks down)
	// Returns (landingY, found)
	tryDrop := func(x, y, z int32) (int32, bool) {
		for dy := int32(1); dy <= 3; dy++ {
			fallY := y - dy

			if w.IsHazard(x, fallY, z) {
				return 0, false
			}

			if w.IsSolid(x, fallY, z) {
				// fallY is the floor, bot stands at fallY+1
				landY := fallY + 1
				// Ensure there's head room
				if !w.IsSolid(x, landY+1, z) && !w.IsHazard(x, landY, z) {
					return landY, true
				}
				return 0, false // Floor found but no head room
			}
		}
		return 0, false // No floor found within 3 blocks = bottomless pit
	}

	// Process vertical ladder climbing
	if w.IsLadder(cx, cy, cz) {
		// 1. Climb Up: target must be a ladder, or a valid standing spot (like the platform at the exit of the ladder)
		if !w.IsSolid(cx, cy+1, cz) && !w.IsHazard(cx, cy+1, cz) {
			neighbors = append(neighbors, Node{X: cx, Y: cy + 1, Z: cz, G: node.G + 0.8})
		}
		// 2. Climb Down: target must be a ladder, or a valid standing spot
		if !w.IsSolid(cx, cy-1, cz) && !w.IsHazard(cx, cy-1, cz) {
			if w.IsLadder(cx, cy-1, cz) || w.IsSolid(cx, cy-2, cz) { // Note: Y-1 of target is cy-2
				neighbors = append(neighbors, Node{X: cx, Y: cy - 1, Z: cz, G: node.G + 0.8})
			}
		}
	}

	// Process cardinal directions
	for _, off := range cardinalOffsets {
		tx := cx + off.dx
		tz := cz + off.dz

		// 1. Walk straight (same level)
		if canStandAt(tx, cy, tz) {
			neighbors = append(neighbors, Node{X: tx, Y: cy, Z: tz, G: node.G + 1.0})
		} else if !w.IsSolid(tx, cy, tz) && !w.IsSolid(tx, cy+1, tz) &&
			!w.IsHazard(tx, cy, tz) && !w.IsHazard(tx, cy+1, tz) {
			// Target has air at body level but no floor — try dropping
			if landY, ok := tryDrop(tx, cy, tz); ok {
				dropDist := float32(cy - landY)
				neighbors = append(neighbors, Node{X: tx, Y: landY, Z: tz, G: node.G + 1.0 + dropDist*0.3})
			}

			// Also try jump gap (2 blocks forward, same level)
			if !w.IsSolid(tx, cy+2, tz) && !w.IsSolid(cx, cy+2, cz) {
				lx := cx + off.dx*2
				lz := cz + off.dz*2
				if canStandAt(lx, cy, lz) {
					neighbors = append(neighbors, Node{X: lx, Y: cy, Z: lz, G: node.G + 1.8})
				}
			}
		}

		// 2. Step up 1 block (jump up)
		if !w.IsSolid(cx, cy+2, cz) { // Need head room above current position
			if w.IsSolid(tx, cy, tz) && !w.IsHazard(tx, cy, tz) {
				// Block at target is the floor, bot stands at cy+1
				if !w.IsSolid(tx, cy+1, tz) && !w.IsSolid(tx, cy+2, tz) &&
					!w.IsHazard(tx, cy+1, tz) && !w.IsHazard(tx, cy+2, tz) {
					neighbors = append(neighbors, Node{X: tx, Y: cy + 1, Z: tz, G: node.G + 1.4})
				}
			}
		}
	}

	// Process diagonal directions
	for _, off := range diagonalOffsets {
		tx := cx + off.dx
		tz := cz + off.dz

		// Diagonal flat/drop/step-up check
		// For all of these, adjacent cardinal directions must be clear at current body/head level
		adj1Clear := !w.IsSolid(cx+off.dx, cy, cz) && !w.IsSolid(cx+off.dx, cy+1, cz) &&
			!w.IsHazard(cx+off.dx, cy, cz) && !w.IsHazard(cx+off.dx, cy+1, cz)
		adj2Clear := !w.IsSolid(cx, cy, cz+off.dz) && !w.IsSolid(cx, cy+1, cz+off.dz) &&
			!w.IsHazard(cx, cy, cz+off.dz) && !w.IsHazard(cx, cy+1, cz+off.dz)

		if adj1Clear && adj2Clear {
			// 1. Diagonal flat walk
			if canStandAt(tx, cy, tz) {
				neighbors = append(neighbors, Node{X: tx, Y: cy, Z: tz, G: node.G + 1.414})
			}
		}
	}

	return neighbors
}
