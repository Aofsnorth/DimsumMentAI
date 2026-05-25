package pathfinder

import (
	"fmt"
	"sync"
)

type WorldModel interface {
	IsSolid(x, y, z int32) bool
	IsHazard(x, y, z int32) bool
	GetNeighbors(node Node) []Node
}

// ChunkQuerier is the interface for querying actual block data from the chunk cache.
// If the chunk at (x, y, z) is loaded, it returns (isAir, true).
// If the chunk is not loaded, it returns (false, false).
type ChunkQuerier interface {
	IsBlockAir(x, y, z int32) (isAir bool, loaded bool)
}

type LocalWorldModel struct {
	mu           sync.RWMutex
	solidBlocks  map[string]bool
	hazardBlocks map[string]bool

	// Chunk cache for real block data from the server
	chunkQuerier ChunkQuerier

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

// SetChunkQuerier sets the chunk cache querier for real block data lookups.
func (w *LocalWorldModel) SetChunkQuerier(q ChunkQuerier) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chunkQuerier = q
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
		// Explicitly mark as not-solid (false) so we know we've checked it
		w.solidBlocks[k] = false
	}
}

func (w *LocalWorldModel) IsSolid(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// 1. Check explicit override map (from UpdateBlock packets, or manual SetSolid calls)
	k := fmt.Sprintf("%d,%d,%d", x, y, z)
	if val, ok := w.solidBlocks[k]; ok {
		return val
	}

	// 2. Query chunk cache for real block data from the server
	if w.chunkQuerier != nil {
		isAir, loaded := w.chunkQuerier.IsBlockAir(x, y, z)
		if loaded {
			// Block is solid if it is NOT air.
			// (This is a simplification — water/lava are also not air but not solid walkable.
			//  However, for basic pathfinding this is a massive improvement over guessing.)
			return !isAir
		}
	}

	// 3. Fallback: chunk not loaded. Treat as impassable so the bot won't walk
	//    into unknown territory. This is the conservative approach.
	//    However, if we're close to start/target positions we still need some
	//    fallback so pathfinding can at least begin.
	if y > 320 {
		return false // Void
	}

	// If no bounds set, use simple sea-level assumption
	if !w.hasBounds {
		return y <= 62
	}

	// Conservative: if chunk is not loaded but we have bounds,
	// assume a flat ground at the lower of start/target Y levels.
	// This is only used as a last resort.
	minY := w.startY - 1
	if w.targetY-1 < minY {
		minY = w.targetY - 1
	}
	return y <= minY
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

	// 4 cardinal directions (North, South, East, West)
	offsets := []struct{ dx, dz int32 }{
		{0, -1}, // North
		{0, 1},  // South
		{1, 0},  // East
		{-1, 0}, // West
	}

	for _, off := range offsets {
		tx := cx + off.dx
		tz := cz + off.dz

		// Skip if target block or head height is a known hazard
		if w.IsHazard(tx, cy, tz) || w.IsHazard(tx, cy+1, tz) {
			continue
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
