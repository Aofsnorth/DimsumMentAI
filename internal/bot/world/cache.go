package world

import (
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
	mu        sync.RWMutex
	chunks    map[chunkPos]*chunk.Chunk
	blobs     map[uint64][]byte // client blob cache payloads from ClientCacheMissResponse
	airRID    uint32            // runtime ID that represents air
	r         cube.Range        // vertical range of the world (usually [-64, 319])
	logger    *slog.Logger
	hashToRID map[uint32]uint32
	useHashes bool
}

// NewWorldCache creates a WorldCache.
func NewWorldCache(airRID uint32, r cube.Range, logger *slog.Logger) *WorldCache {
	if airRID == 0 && chunk.StateToRuntimeID != nil {
		if rid, ok := chunk.StateToRuntimeID("minecraft:air", nil); ok {
			airRID = rid
		}
	}
	wc := &WorldCache{
		chunks: make(map[chunkPos]*chunk.Chunk),
		airRID: airRID,
		r:      r,
		logger: logger,
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
	c.SetBlock(uint8(x&0xf), int16(y), uint8(z&0xf), 0, rid)
	wc.mu.Unlock()
}
