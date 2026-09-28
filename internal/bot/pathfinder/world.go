package pathfinder

import (
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
	solidBlocks     map[int64]bool
	hazardBlocks    map[int64]bool
	passableBlocks  map[int64]bool // mined/placed passable overrides (persistent)
	bodyClearance   map[int64]bool // bot AABB cells for the current tick only
	tempSolidBlocks map[int64]time.Time
	chunkQuerier    ChunkQuerier

	AllowScaffold bool

	hasBounds                 bool
	startX, startY, startZ    int32
	targetX, targetY, targetZ int32
}

// packBlockKey encodes a block coordinate into a single int64 map key.
//
// The obvious implementation is fmt.Sprintf("%d,%d,%z"), and that is what this
// used to do. It runs in the hottest path in the bot: IsSolid is called for
// every collision and every pathfinding neighbour, dozens of times a tick, and
// each Sprintf allocates a string and runs the formatting machinery. Packing
// into an int64 makes the lookup allocation-free.
//
// The three fields use 26 + 12 + 26 bits, which is exactly 64. That is not
// arbitrary: 26 bits per horizontal axis covers the full Bedrock world
// (±30,000,000) and 12 bits covers the build height (±2048) exactly, so no
// coordinate in a real world can ever collide.
//
// This is NOT the same encoding as packKey in astar.go. That one packs 21-bit
// fields and is never unpacked, so its range never mattered; block keys are
// unpacked again during PurgeFalseSolidOverrides and need the real range.
func packBlockKey(x, y, z int32) int64 {
	ux := uint64(int64(x)) & 0x3FFFFFF
	uy := uint64(int64(y)) & 0xFFF
	uz := uint64(int64(z)) & 0x3FFFFFF
	return int64(uz | uy<<26 | ux<<38)
}

func unpackBlockKey(key int64) (x, y, z int32) {
	// Read the fields back in the order packBlockKey wrote them.
	uz := uint64(key) & 0x3FFFFFF
	uy := (uint64(key) >> 26) & 0xFFF
	ux := (uint64(key) >> 38) & 0x3FFFFFF

	// Sign-extend the bit fields back to the negative coordinates they came from.
	if ux&(1<<25) != 0 {
		ux |= ^uint64(0x3FFFFFF)
	}
	if uy&(1<<11) != 0 {
		uy |= ^uint64(0xFFF)
	}
	if uz&(1<<25) != 0 {
		uz |= ^uint64(0x3FFFFFF)
	}
	return int32(ux), int32(uy), int32(uz)
}

func (w *LocalWorldModel) IsBreakable(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.chunkQuerier != nil {
		rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
		if loaded {
			name, ok := blockNameFor(w.chunkQuerier, rid)
			if ok {
				return name != "minecraft:bedrock"
			}
		}
	}
	return true
}

func NewLocalWorldModel() *LocalWorldModel {
	return &LocalWorldModel{
		solidBlocks:     make(map[int64]bool),
		hazardBlocks:    make(map[int64]bool),
		passableBlocks:  make(map[int64]bool),
		bodyClearance:   make(map[int64]bool),
		tempSolidBlocks: make(map[int64]time.Time),
	}
}

// ClearBodyClearance resets per-tick occupancy marks (bot body volume).
//
// The map is emptied in place rather than replaced. This runs 20 times a second
// and a fresh map every tick is 20 allocations a second of garbage for a
// structure that is almost always nearly empty anyway.
func (w *LocalWorldModel) ClearBodyClearance() {
	w.mu.Lock()
	clear(w.bodyClearance)
	w.mu.Unlock()
}

// SetBodyClearance marks a block as non-solid for collision this tick only.
func (w *LocalWorldModel) SetBodyClearance(x, y, z int32) {
	w.mu.Lock()
	w.bodyClearance[packBlockKey(x, y, z)] = true
	w.mu.Unlock()
}

// CanResolve reports whether the underlying chunk querier has data for the
// given position. The terrain gate in the movement layer uses this to skip
// pathfinding over an unloaded world while still allowing synthetic setups
// (tests, harness bots) whose querier always answers.
func (w *LocalWorldModel) CanResolve(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.chunkQuerier == nil {
		return false
	}
	_, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
	return loaded
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
	k := packBlockKey(x, y, z)

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
		x, y, z := unpackBlockKey(k)
		if w.chunkSaysSolid(x, y, z) {
			delete(w.passableBlocks, k)
			delete(w.solidBlocks, k)
		}
	}
	for k, solid := range w.solidBlocks {
		if solid {
			continue
		}
		x, y, z := unpackBlockKey(k)
		if w.chunkSaysSolid(x, y, z) {
			delete(w.passableBlocks, k)
			delete(w.solidBlocks, k)
		}
	}
}

func (w *LocalWorldModel) chunkSaysSolid(x, y, z int32) bool {
	isSolid, loaded := w.chunkQuerier.IsBlockSolid(x, y, z)
	return loaded && isSolid
}

// Reset drops everything the model believes about the world.
//
// It is called when the bot crosses a dimension. The model's overrides are
// learned facts about a specific place — this block was mined, that one was
// placed, this cell is a hazard — and every one of them is about a world the
// bot is no longer standing in. Keeping them means the bot arrives in the
// Nether already certain that cells are solid that are open sky, and the
// pathfinder believes it before it has seen a single Nether block.
func (w *LocalWorldModel) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.solidBlocks = make(map[int64]bool)
	w.hazardBlocks = make(map[int64]bool)
	w.passableBlocks = make(map[int64]bool)
	w.bodyClearance = make(map[int64]bool)
	w.tempSolidBlocks = make(map[int64]time.Time)
	// The chunk querier and the path bounds are not facts about the world: they
	// are where to ask and what the current trip is. Those survive.
}

// SetTempSolid marks a cell temporarily blocked, used for stuck recovery.
func (w *LocalWorldModel) SetTempSolid(x, y, z int32, duration time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tempSolidBlocks[packBlockKey(x, y, z)] = time.Now().Add(duration)
}

func (w *LocalWorldModel) IsSolid(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	k := packBlockKey(x, y, z)

	// Bot body volume wins over everything else this tick. If the bot just
	// got marked tempSolid at its own position (stuck-recovery), treating the
	// tile as solid makes GetNeighbors unable to leave the start node and the
	// whole A* loop deadlocks at path=nil every tick.
	if w.bodyClearance[k] {
		return false
	}

	if expiry, ok := w.tempSolidBlocks[k]; ok {
		if time.Now().Before(expiry) {
			return true
		}
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

// IsLoaded reports whether the world model actually knows what occupies a cell.
//
// This is the difference between "the server told me this is air" and "this cell
// was never decoded". Path smoothing must not treat the two alike: a
// string-pulled link is walked blindly, so pulling one across unknown cells
// aims the bot straight at terrain the model has never seen — which is how it
// used to wedge against a wall that only appeared once the bot was already
// halfway down the link.
func (w *LocalWorldModel) IsLoaded(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Explicit overrides are knowledge in their own right: the bot mined or
	// placed these cells, or a stuck-recovery penalty marked them.
	k := packBlockKey(x, y, z)
	if w.solidBlocks[k] || w.passableBlocks[k] || w.hazardBlocks[k] {
		return true
	}
	if _, marked := w.tempSolidBlocks[k]; marked {
		return true
	}

	// No chunk source attached (unit tests, synthetic models) — nothing to be
	// uncertain about, so stay permissive and keep the old behaviour.
	if w.chunkQuerier == nil {
		return true
	}
	_, loaded := w.chunkQuerier.IsBlockAir(x, y, z)
	return loaded
}

func (w *LocalWorldModel) SetHazard(x, y, z int32, hazard bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	k := packBlockKey(x, y, z)
	if hazard {
		w.hazardBlocks[k] = true
	} else {
		delete(w.hazardBlocks, k)
	}
}

// nameAwareQuerier is implemented by caches that remember resolved block names.
type nameAwareQuerier interface {
	BlockName(rid uint32) (string, bool)
}

// blockNameFor resolves a runtime ID to a block name, preferring the cache when
// the querier has one. IsHazard and IsLadder are called for every neighbour of
// every pathfinding step, so this runs in the millions over a long gather.
func blockNameFor(querier ChunkQuerier, rid uint32) (string, bool) {
	if nq, ok := querier.(nameAwareQuerier); ok {
		return nq.BlockName(rid)
	}
	// RuntimeIDToState is a package-level func var that dragonfly's world
	// package fills in at init. A binary that never links it (this package's own
	// tests, a tool) leaves it nil, and calling it takes the process down from
	// inside the movement loop. Treat an unresolved name as "unknown" instead.
	if chunk.RuntimeIDToState == nil {
		return "", false
	}
	name, _, ok := chunk.RuntimeIDToState(rid)
	return name, ok
}

func (w *LocalWorldModel) IsHazard(x, y, z int32) bool {
	// The void and the build ceiling come first because they are arithmetic.
	// Checking them here rather than in each movement mode is the whole point of
	// this vocabulary: the walk rule, the drop rule, the parkour rule and the
	// diagonal rule all call IsHazard, so none of them can forget that there is
	// something below the world to fall into.
	if IsVoid(y) {
		return true
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	// 1. Check self-learned hazards
	if w.hazardBlocks[packBlockKey(x, y, z)] {
		return true
	}

	// 2. Pre-emptively check for known natural hazards (lava, fire) and the rest
	// of the lethal set.
	if w.chunkQuerier != nil {
		rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
		if loaded {
			name, ok := blockNameFor(w.chunkQuerier, rid)
			if ok && normalisedLethal[normaliseBlockName(name)] {
				return true
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
			name, ok := blockNameFor(w.chunkQuerier, rid)
			if ok {
				return name == "minecraft:ladder" || strings.Contains(name, "vine") || name == "minecraft:scaffolding"
			}
		}
	}

	return false
}
