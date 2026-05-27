package pathfinder

import (
	"fmt"
	"strconv"
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
	mu              sync.RWMutex
	solidBlocks     map[string]bool
	hazardBlocks    map[string]bool
	passableBlocks  map[string]bool // mined/placed passable overrides (persistent)
	bodyClearance   map[string]bool // bot AABB cells for the current tick only
	tempSolidBlocks map[string]time.Time
	chunkQuerier    ChunkQuerier

	AllowScaffold bool

	hasBounds bool
	startX, startY, startZ    int32
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
		bodyClearance:   make(map[string]bool),
		tempSolidBlocks: make(map[string]time.Time),
	}
}

// ClearBodyClearance resets per-tick occupancy marks (bot body volume).
func (w *LocalWorldModel) ClearBodyClearance() {
	w.mu.Lock()
	w.bodyClearance = make(map[string]bool)
	w.mu.Unlock()
}

// SetBodyClearance marks a block as non-solid for collision this tick only.
func (w *LocalWorldModel) SetBodyClearance(x, y, z int32) {
	w.mu.Lock()
	w.bodyClearance[fmt.Sprintf("%d,%d,%d", x, y, z)] = true
	w.mu.Unlock()
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

// SetSolid sets a persistent block override (UpdateBlock, mining, building).
// Use SetBodyClearance for per-tick bot occupancy — not SetSolid(false).
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

// PurgeFalseSolidOverrides removes stale non-solid overrides when chunk data says solid.
func (w *LocalWorldModel) PurgeFalseSolidOverrides() {
	if w.chunkQuerier == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for k := range w.passableBlocks {
		if coords, ok := parseBlockKey(k); ok && w.chunkSaysSolid(coords[0], coords[1], coords[2]) {
			delete(w.passableBlocks, k)
			delete(w.solidBlocks, k)
		}
	}
	for k, solid := range w.solidBlocks {
		if solid {
			continue
		}
		if coords, ok := parseBlockKey(k); ok && w.chunkSaysSolid(coords[0], coords[1], coords[2]) {
			delete(w.passableBlocks, k)
			delete(w.solidBlocks, k)
		}
	}
}

func (w *LocalWorldModel) chunkSaysSolid(x, y, z int32) bool {
	isSolid, loaded := w.chunkQuerier.IsBlockSolid(x, y, z)
	return loaded && isSolid
}

func parseBlockKey(k string) ([3]int32, bool) {
	parts := strings.Split(k, ",")
	if len(parts) != 3 {
		return [3]int32{}, false
	}
	x, errX := strconv.ParseInt(parts[0], 10, 32)
	y, errY := strconv.ParseInt(parts[1], 10, 32)
	z, errZ := strconv.ParseInt(parts[2], 10, 32)
	if errX != nil || errY != nil || errZ != nil {
		return [3]int32{}, false
	}
	return [3]int32{int32(x), int32(y), int32(z)}, true
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

	// Bot body volume this tick (does not persist).
	if w.bodyClearance[k] {
		return false
	}

	// Persistent overrides from UpdateBlock / mining / building.
	if val, ok := w.solidBlocks[k]; ok {
		return val
	}
	if _, isPassable := w.passableBlocks[k]; isPassable {
		return false
	}

	// Chunk cache is the source of truth when loaded.
	if w.chunkQuerier != nil {
		isSolid, loaded := w.chunkQuerier.IsBlockSolid(x, y, z)
		if loaded {
			return isSolid
		}
	}

	// Fallback when chunk is not loaded yet.
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