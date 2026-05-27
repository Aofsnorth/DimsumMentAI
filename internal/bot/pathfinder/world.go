package pathfinder

import (
	"fmt"
	"strings"
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

	AllowScaffold  bool

	hasBounds bool
	startX, startY, startZ   int32
	targetX, targetY, targetZ int32
}

func (w *LocalWorldModel) IsBreakable(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.chunkQuerier != nil {
		rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
		if loaded {
			name, _, ok := chunk.RuntimeIDToState(rid)
			if ok {
				if name == "minecraft:bedrock" {
					return false
				}
				return true
			}
		}
	}
	return true
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
				return name == "minecraft:ladder" || strings.Contains(name, "vine") || name == "minecraft:scaffolding"
			}
		}
	}
	
	return false
}