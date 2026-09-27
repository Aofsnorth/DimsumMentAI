package world

import (
	"bedrock-ai/internal/safecast"
	"log/slog"
	"sync"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
)

// chunkPos encodes a chunk column position (chunk X, chunk Z).
type chunkPos struct {
	X, Z int32
}

// WorldCache stores decoded chunk data received from the server.
type WorldCache struct {
	mu               sync.RWMutex
	chunks           map[chunkPos]*chunk.Chunk
	blobs            map[uint64][]byte // client blob cache payloads from ClientCacheMissResponse
	airRID           uint32            // runtime ID that represents air
	r                cube.Range        // vertical range of the world (usually [-64, 319])
	logger           *slog.Logger
	hashToRID        map[uint32]uint32
	ridToHash        map[uint32]uint32
	useHashes        bool
	paletteDumpCount int
	subChunksApplied uint64

	// paletteMu guards the resolved-palette caches below. They are deliberately
	// separate from mu: mu is held while chunks are decoded, and a cell query
	// that had to wait on a decode would stall the movement tick.
	//
	// These caches are palette-derived, not world-derived, so they survive a
	// Reset (a rejoin into a new world) and are seeded once at construction.
	paletteMu sync.RWMutex
	ridNames  map[uint32]string
	ridSolid  map[uint32]bool
}

// Reset drops everything that belongs to a single world session: the decoded
// chunks, cached blobs, and the decode counters.
//
// The block-name/hash tables and the vertical range are deliberately kept —
// they come from the protocol and the block palette, not from the world. A
// rejoin into a world the host closed and reopened has a different seed, so
// holding on to its old chunks would let pathfinding plan through terrain that
// is no longer there.
func (wc *WorldCache) Reset() {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	wc.chunks = make(map[chunkPos]*chunk.Chunk)
	wc.blobs = make(map[uint64][]byte)
	wc.paletteDumpCount = 0
	wc.subChunksApplied = 0
}

// NewWorldCache creates a WorldCache.
func NewWorldCache(airRID uint32, r cube.Range, logger *slog.Logger) *WorldCache {
	if airRID == 0 && chunk.StateToRuntimeID != nil {
		if rid, ok := chunk.StateToRuntimeID("minecraft:air", nil); ok {
			airRID = rid
		}
	}
	wc := &WorldCache{
		chunks:   make(map[chunkPos]*chunk.Chunk),
		airRID:   airRID,
		r:        r,
		logger:   logger,
		ridNames: make(map[uint32]string),
		ridSolid: make(map[uint32]bool),
	}
	wc.precomputeBlockHashes()
	return wc
}

// SetLogger configures the logger.
func (wc *WorldCache) SetLogger(logger *slog.Logger) {
	wc.mu.Lock()
	wc.logger = logger
	wc.mu.Unlock()
}

// SetUseBlockNetworkIDHashes configures the world cache to translate block network ID hashes.
func (wc *WorldCache) SetUseBlockNetworkIDHashes(use bool) {
	wc.mu.Lock()
	wc.useHashes = use
	wc.mu.Unlock()
	if use {
		if wc.logger != nil {
			wc.logger.Info("WorldCache configured to use FNV-1a block network ID hashes", "size", len(wc.hashToRID))
		}
	}
}

// TranslateRuntimeID converts a network block state hash to a local runtime ID.
func (wc *WorldCache) TranslateRuntimeID(rid uint32) uint32 {
	wc.mu.RLock()
	useHashes := wc.useHashes
	realRID, hasHash := wc.hashToRID[rid]
	wc.mu.RUnlock()

	if useHashes && hasHash {
		return realRID
	}
	if _, _, ok := chunk.RuntimeIDToState(rid); !ok && hasHash {
		return realRID
	}
	return rid
}

// NetworkRuntimeID converts a local runtime ID back to the block network ID
// format selected by the server in StartGame.
func (wc *WorldCache) NetworkRuntimeID(rid uint32) (uint32, bool) {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	if !wc.useHashes {
		return rid, true
	}
	hash, ok := wc.ridToHash[rid]
	return hash, ok
}

// GetBlockNetworkID returns the wire-format block network ID at a position.
func (wc *WorldCache) GetBlockNetworkID(x, y, z int32) (uint32, bool) {
	rid, ok := wc.GetBlockRID(x, y, z)
	if !ok {
		return 0, false
	}
	return wc.NetworkRuntimeID(rid)
}

// HashLookupHit reports whether rid appears in the precomputed hash map.
// Diagnostic-only; used by the A* neighbor probe to see whether wire hashes
// are hitting the local table.
func (wc *WorldCache) HashLookupHit(rid uint32) bool {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	_, ok := wc.hashToRID[rid]
	return ok
}

// StoreBlobs saves blob payloads from ClientCacheMissResponse.
func (wc *WorldCache) StoreBlobs(blobs map[uint64][]byte) {
	if len(blobs) == 0 {
		return
	}
	wc.mu.Lock()
	if wc.blobs == nil {
		wc.blobs = make(map[uint64][]byte, len(blobs))
	}
	for h, payload := range blobs {
		wc.blobs[h] = payload
	}
	wc.mu.Unlock()
}

// ChunkCount returns the number of chunks currently cached.
func (wc *WorldCache) ChunkCount() int {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	return len(wc.chunks)
}

// SubChunksApplied returns how many sub-chunk payloads have been merged into
// the cache. Zero means the world model is still effectively empty, no matter
// how many chunk columns exist.
func (wc *WorldCache) SubChunksApplied() uint64 {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	return wc.subChunksApplied
}

// SetBlockRID updates a single cached block from a server UpdateBlock packet.
func (wc *WorldCache) SetBlockRID(x, y, z int32, rid uint32) {
	if int(y) < wc.r.Min() || int(y) > wc.r.Max() {
		return
	}

	rid = wc.TranslateRuntimeID(rid)
	pos := chunkPos{X: x >> 4, Z: z >> 4}

	wc.mu.Lock()
	c, ok := wc.chunks[pos]
	if !ok {
		c = chunk.New(wc.airRID, wc.r)
		wc.chunks[pos] = c
	}
	c.SetBlock(safecast.To[uint8](x&0xf), safecast.To[int16](y), safecast.To[uint8](z&0xf), 0, rid)
	wc.mu.Unlock()
}
