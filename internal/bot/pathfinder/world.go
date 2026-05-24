package pathfinder

import (
	"fmt"
	"math"
	"sync"
)

type WorldModel interface {
	IsSolid(x, y, z int32) bool
	IsHazard(x, y, z int32) bool
	GetNeighbors(node Node) []Node
}

type LocalWorldModel struct {
	mu           sync.RWMutex
	solidBlocks  map[string]bool
	hazardBlocks map[string]bool

	// Pathfinder bounds for fallback solidity when chunk block data is empty
	hasBounds bool
	startX    int32
	startY    int32
	startZ    int32
	targetX   int32
	targetY   int32
	targetZ   int32
}

func NewLocalWorldModel() *LocalWorldModel {
	return &LocalWorldModel{
		solidBlocks:  make(map[string]bool),
		hazardBlocks: make(map[string]bool),
	}
}

// SetPathBounds registers the start and target search coordinates to enable ground interpolation fallback.
func (w *LocalWorldModel) SetPathBounds(start, target Node) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.startX, w.startY, w.startZ = start.X, start.Y, start.Z
	w.targetX, w.targetY, w.targetZ = target.X, target.Y, target.Z
	w.hasBounds = true
}

// SetSolid marks a coordinate as solid or empty in the local map representation
func (w *LocalWorldModel) SetSolid(x, y, z int32, solid bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	k := fmt.Sprintf("%d,%d,%d", x, y, z)
	if solid {
		w.solidBlocks[k] = true
	} else {
		delete(w.solidBlocks, k)
	}
}

func (w *LocalWorldModel) IsSolid(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	k := fmt.Sprintf("%d,%d,%d", x, y, z)
	if val, ok := w.solidBlocks[k]; ok {
		return val
	}

	// Fallback when we don't have block data from chunk packets
	if !w.hasBounds {
		// Default guess baseline - assume ground at y=62 (typical sea level)
		// This is more conservative than y<70 for varied terrain
		// But if y is very high (void), assume no solid blocks
		if y > 320 {
			return false // Void - no solid blocks
		}
		return y <= 62
	}

	// Calculate distance-based interpolated ground level at (x, z)
	dx := float64(w.targetX - w.startX)
	dz := float64(w.targetZ - w.startZ)
	totDistSq := dx*dx + dz*dz

	var groundY int32
	if totDistSq < 0.01 {
		groundY = w.targetY - 1
	} else {
		// Project the point (x, z) onto the line segment from start to target
		px := float64(x - w.startX)
		pz := float64(z - w.startZ)

		t := (px*dx + pz*dz) / totDistSq
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}

		interpolatedY := float64(w.startY-1) + t*float64(w.targetY-w.startY)
		groundY = int32(math.Round(interpolatedY))
	}

	// If in void (y > 320), assume no solid blocks to allow pathfinding to work
	if y > 320 {
		return false
	}

	return y <= groundY
}

// SetHazard registers or removes a block as hazardous (lava, fire, void)
func (w *LocalWorldModel) SetHazard(x, y, z int32, hazard bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	k := fmt.Sprintf("%d,%d,%d", x, y, z)
	if hazard {
		w.hazardBlocks[k] = true
	} else {
		delete(w.hazardBlocks, k)
	}
}

func (w *LocalWorldModel) IsHazard(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	k := fmt.Sprintf("%d,%d,%d", x, y, z)
	return w.hazardBlocks[k]
}

// GetNeighbors generates valid walk, step-up, and drop-down coordinates, skipping hazards
func (w *LocalWorldModel) GetNeighbors(node Node) []Node {
	var neighbors []Node
	cx, cy, cz := node.X, node.Y, node.Z

	// 8 directions (4 cardinal + 4 diagonals) for smoother movement
	offsets := []struct{ dx, dz int32 }{
		{0, -1},  // North
		{0, 1},   // South
		{1, 0},   // East
		{-1, 0},  // West
		{1, -1},  // Northeast
		{1, 1},   // Southeast
		{-1, 1},  // Southwest
		{-1, -1}, // Northwest
	}

	for _, off := range offsets {
		tx := cx + off.dx
		tz := cz + off.dz

		// Skip if target block or head height is a known hazard
		if w.IsHazard(tx, cy, tz) || w.IsHazard(tx, cy+1, tz) {
			continue
		}

		// For diagonal movement, ensure both adjacent cardinal blocks are walkable
		// to prevent cutting through corners
		if off.dx != 0 && off.dz != 0 {
			// Check the two adjacent cardinal blocks
			if w.IsSolid(cx+off.dx, cy, cz) || w.IsSolid(cx+off.dx, cy+1, cz) ||
				w.IsSolid(cx, cy, cz+off.dz) || w.IsSolid(cx, cy+1, cz+off.dz) {
				continue
			}
		}

		// 1. Move straight (same height)
		if !w.IsSolid(tx, cy, tz) && !w.IsSolid(tx, cy+1, tz) {
			if w.IsSolid(tx, cy-1, tz) && !w.IsHazard(tx, cy-1, tz) {
				neighbors = append(neighbors, Node{X: tx, Y: cy, Z: tz})
				continue
			}
		}

		// 2. Step up 1 block
		if !w.IsSolid(cx, cy+2, cz) && !w.IsHazard(tx, cy+2, tz) {
			if !w.IsSolid(tx, cy+1, tz) && !w.IsSolid(tx, cy+2, tz) && w.IsSolid(tx, cy, tz) && !w.IsHazard(tx, cy, tz) {
				neighbors = append(neighbors, Node{X: tx, Y: cy + 1, Z: tz})
				continue
			}
		}

		// 3. Drop down (up to 3 blocks)
		for dy := int32(1); dy <= 3; dy++ {
			clear := true
			for y := cy; y >= cy-dy; y-- {
				if w.IsSolid(tx, y, tz) || w.IsHazard(tx, y, tz) {
					clear = false
					break
				}
			}
			if clear {
				if w.IsSolid(tx, cy-dy-1, tz) && !w.IsHazard(tx, cy-dy-1, tz) {
					neighbors = append(neighbors, Node{X: tx, Y: cy - dy, Z: tz})
					break
				}
			}
		}
	}

	return neighbors
}
