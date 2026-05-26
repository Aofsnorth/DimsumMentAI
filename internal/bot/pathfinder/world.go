package pathfinder

import (
	"fmt"
	"sync"
	"time"

	"github.com/df-mc/dragonfly/server/world/chunk"
)

type WorldModel interface {
	IsSolid(x, y, z int32) bool
	IsHazard(x, y, z int32) bool
	IsLadder(x, y, z int32) bool
	GetNeighbors(node Node) []Node
	SetPathBounds(start, target Node)
}

// ChunkQuerier interface untuk mengambil Runtime ID blok dari WorldCache
type ChunkQuerier interface {
	GetBlockRID(x, y, z int32) (rid uint32, loaded bool)
	IsBlockAir(x, y, z int32) (isAir bool, loaded bool)
	IsBlockSolid(x, y, z int32) (isSolid bool, loaded bool)
}

type LocalWorldModel struct {
	mu             sync.RWMutex
	solidBlocks    map[string]bool
	hazardBlocks   map[string]bool
	passableBlocks map[string]bool // Blok yang bot tahu bisa dilewati (rumput, bunga, air)
	tempSolidBlocks map[string]time.Time // Temporary solid blocks to avoid stuck loops
	chunkQuerier   ChunkQuerier

	hasBounds bool
	startX, startY, startZ   int32
	targetX, targetY, targetZ int32
}

func NewLocalWorldModel() *LocalWorldModel {
	return &LocalWorldModel{
		solidBlocks:     make(map[string]bool),
		hazardBlocks:    make(map[string]bool),
		passableBlocks:  make(map[string]bool),
		tempSolidBlocks: make(map[string]time.Time),
	}
}

func (w *LocalWorldModel) SetChunkQuerier(q ChunkQuerier) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chunkQuerier = q
}

func (w *LocalWorldModel) SetPathBounds(start, target Node) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.startX, w.startY, w.startZ = start.X, start.Y, start.Z
	w.targetX, w.targetY, w.targetZ = target.X, target.Y, target.Z
	w.hasBounds = true
}

// SetSolid menandai blok. Jika solid=false, blok masuk ke passableBlocks
func (w *LocalWorldModel) SetSolid(x, y, z int32, solid bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	k := fmt.Sprintf("%d,%d,%d", x, y, z)
	
	if solid {
		w.solidBlocks[k] = true
		delete(w.passableBlocks, k) // Hapus dari passable jika ternyata solid
	} else {
		w.solidBlocks[k] = false
		w.passableBlocks[k] = true // Tandai sebagai bisa dilewati (rumput, bunga, dll)
	}
}

func (w *LocalWorldModel) SetTempSolid(x, y, z int32, duration time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	k := fmt.Sprintf("%d,%d,%d", x, y, z)
	w.tempSolidBlocks[k] = time.Now().Add(duration)
}

func (w *LocalWorldModel) IsSolid(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	k := fmt.Sprintf("%d,%d,%d", x, y, z)

	if expiry, ok := w.tempSolidBlocks[k]; ok {
		if time.Now().Before(expiry) {
			return true
		}
	}

	// 1. Cek Passable Blocks (Auto-Learned dari posisi bot)
	if _, isPassable := w.passableBlocks[k]; isPassable {
		return false
	}

	// 2. Cek Override Map (dari packet UpdateBlock)
	if val, ok := w.solidBlocks[k]; ok {
		return val
	}

	// 3. Cek Chunk Cache (Real block data)
	if w.chunkQuerier != nil {
		isSolid, loaded := w.chunkQuerier.IsBlockSolid(x, y, z)
		if loaded {
			return isSolid
		}
	}

	// 4. Fallback jika chunk belum load
	if y > 320 || y < -64 {
		return false // Void
	}
	if !w.hasBounds {
		return y <= 62 // Asumsi sea-level
	}
	// FIX: Jika chunk belum terload, asumsikan air (bukan solid) agar bot tidak berjalan ke kehampaan.
	// Hanya asumsikan blok di bawah tempat bot berdiri saat ini adalah solid agar pathfinder bisa mulai.
	if x == w.startX && y == w.startY-1 && z == w.startZ {
		return true
	}
	return false
}

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
	
	// 1. Check self-learned hazards
	if w.hazardBlocks[fmt.Sprintf("%d,%d,%d", x, y, z)] {
		return true
	}
	
	// 2. Pre-emptively check for known natural hazards (lava, fire)
	if w.chunkQuerier != nil {
		rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
		if loaded {
			name, _, ok := chunk.RuntimeIDToState(rid)
			if ok {
				if name == "minecraft:lava" || name == "minecraft:flowing_lava" || name == "minecraft:fire" {
					return true
				}
			}
		}
	}
	
	return false
}

func (w *LocalWorldModel) IsLadder(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	
	if w.chunkQuerier != nil {
		rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
		if loaded {
			name, _, ok := chunk.RuntimeIDToState(rid)
			if ok {
				return name == "minecraft:ladder"
			}
		}
	}
	
	return false
}

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