package world

import (
	"bedrock-ai/internal/safecast"
	"log/slog"
	"sync"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
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
	geyserCompat     bool
	paletteDumpCount int
	subChunksApplied uint64

	// customBlockToRID maps Bedrock numeric palette indices (when
	// UseBlockNetworkIDHashes is false) to Dragonfly local runtime IDs.
	customBlockToRID []uint32
	customBlockNames []string
	customBlockSolid []bool
	customBlockTops  []float32
	localToCustomRID map[uint32]uint32
	// serverAirs flags air in the merged non-hash server palette; nil when the
	// connection is not a non-hash one.
	serverAirs []bool

	// paletteMu guards the resolved-palette caches below. They are deliberately
	// separate from mu: mu is held while chunks are decoded, and a cell query
	// that had to wait on a decode would stall the movement tick.
	//
	// These caches are palette-derived, not world-derived, so they survive a
	// Reset (a rejoin into a new world) and are seeded once at construction.
	paletteMu sync.RWMutex
	ridNames  map[uint32]string
	ridSolid  map[uint32]bool

	// signTexts maps "x,y,z" to the text written on the sign there, parsed out
	// of the block entities that ride along with chunk payloads.
	signTexts map[string]string
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
	// Signs belong to the world: a rejoin has new ones.
	wc.signTexts = make(map[string]string)
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

// safeStateToRuntimeID converts a block state to Dragonfly's local runtime ID
// without panicking on unsupported property types (e.g. slices like blockTags).
func safeStateToRuntimeID(name string, properties map[string]any) (rid uint32, ok bool) {
	if chunk.StateToRuntimeID == nil {
		return 0, false
	}
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()

	var cleanProps map[string]any
	if len(properties) > 0 {
		cleanProps = make(map[string]any, len(properties))
		for k, v := range properties {
			switch val := v.(type) {
			case bool, uint8, int32, string:
				cleanProps[k] = val
			case int:
				cleanProps[k] = int32(val)
			case int8:
				cleanProps[k] = uint8(val)
			case int16:
				cleanProps[k] = int32(val)
			}
		}
	}

	if len(cleanProps) > 0 {
		rid, ok = chunk.StateToRuntimeID(name, cleanProps)
		if ok {
			return rid, true
		}
	}
	return chunk.StateToRuntimeID(name, nil)
}

// SetCustomBlocks registers the server's block palette sent in StartGame.
// When UseBlockNetworkIDHashes is false, Bedrock servers index blocks by their
// position in this palette rather than by FNV-1a hashes.
func (wc *WorldCache) SetCustomBlocks(blocks []protocol.BlockEntry) {
	if len(blocks) == 0 {
		return
	}

	toRID := make([]uint32, len(blocks))
	names := make([]string, len(blocks))
	solids := make([]bool, len(blocks))
	tops := make([]float32, len(blocks))
	localToNet := make(map[uint32]uint32)

	airFound := false
	var airLocalRID uint32
	if chunk.StateToRuntimeID != nil {
		if rid, ok := chunk.StateToRuntimeID("minecraft:air", nil); ok {
			airLocalRID = rid
			airFound = true
		}
	}

	for i, entry := range blocks {
		netRID := safecast.To[uint32](i)
		names[i] = entry.Name

		localRID, ok := safeStateToRuntimeID(entry.Name, entry.Properties)

		// Solidity comes from the server's name, not from the dragonfly
		// collision model. The local model covers vanilla 1.20 states; on
		// proxy/modded palettes it misclassifies decoration (e.g.
		// mangrove_propagule, wall fans) as solid, walling A* in.
		solids[i] = !isBlockNamePassable(entry.Name)
		tops[i] = TopOffsetFromState(entry.Name, entry.Properties)

		if ok {
			toRID[i] = localRID
			if _, exists := localToNet[localRID]; !exists {
				localToNet[localRID] = netRID
			}
		} else {
			toRID[i] = netRID
		}

		if entry.Name == "minecraft:air" && !airFound {
			airLocalRID = toRID[i]
			airFound = true
		}
	}

	wc.mu.Lock()
	nonHash := wc.geyserCompat || !wc.useHashes
	wc.mu.Unlock()

	var merged serverPaletteTables
	if nonHash {
		merged = mergeServerPalette(blocks)
	}

	wc.mu.Lock()
	wc.customBlockToRID = toRID
	wc.customBlockNames = names
	wc.customBlockSolid = solids
	wc.customBlockTops = tops
	wc.localToCustomRID = localToNet
	if nonHash {
		wc.customBlockNames = merged.names
		wc.customBlockSolid = merged.solids
		wc.customBlockTops = merged.tops
		wc.serverAirs = merged.airs
		// Chunks default-fill with airRID, which must be air in the same
		// network space the decoded cells live in.
		if idx, ok := merged.firstIndexOf("minecraft:air")["minecraft:air"]; ok {
			wc.airRID = safecast.To[uint32](idx)
			airLocalRID = wc.airRID
		}
	} else if airFound {
		wc.airRID = airLocalRID
	}
	wc.mu.Unlock()

	if nonHash && wc.logger != nil {
		wc.logger.Info("merged server palette",
			slog.Int("vanilla", len(palette121Names)),
			slog.Int("total", len(merged.names)),
			slog.Any("index_of", merged.firstIndexOf("minecraft:air", "minecraft:dirt", "minecraft:grass_block", "minecraft:stone")),
		)
		wc.logger.Debug("custom blocks detail", slog.Any("blocks", describeCustomBlocks(blocks)))
	}

	if wc.logger != nil {
		probe := map[string]int{}
		for i, n := range names {
			switch n {
			case "minecraft:air", "minecraft:grass_block", "minecraft:grass", "minecraft:stone", "minecraft:dirt":
				if _, seen := probe[n]; !seen {
					probe[n] = i
				}
			}
		}
		wc.logger.Info("WorldCache registered custom blocks palette",
			slog.Int("total_blocks", len(blocks)),
			slog.Uint64("air_rid", uint64(airLocalRID)),
			slog.Any("first_index_of", probe),
		)
	}
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

// SetGeyserCompat configures the world cache for servers fronted by Geyser.
func (wc *WorldCache) SetGeyserCompat(compat bool) {
	wc.mu.Lock()
	wc.geyserCompat = compat
	wc.mu.Unlock()
}

// GeyserCompat reports whether the world cache has Geyser compatibility enabled.
func (wc *WorldCache) GeyserCompat() bool {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	return wc.geyserCompat
}

// TranslateRuntimeID converts a network block state hash or numeric index to a local runtime ID.
func (wc *WorldCache) TranslateRuntimeID(rid uint32) uint32 {
	wc.mu.RLock()
	useHashes := wc.useHashes
	realRID, hasHash := wc.hashToRID[rid]
	wc.mu.RUnlock()

	// Geyser / non-hash connections: rid is already a server palette index —
	// the exact space SetCustomBlocks indexes and the space every resolver
	// (serverBlockName, palette_121) answers in. Translating it into dragonfly's
	// local space here left the chunk cache holding LOCAL rids while the
	// resolvers read NETWORK indices, so every block came back with another
	// block's name and solidity: grass_block/slab cells read as passable
	// plants, ground support failed and the bot froze on flat terrain. Keep
	// one space end-to-end; the BDS hash mode below is unchanged.
	if !useHashes {
		return rid
	}
	if hasHash {
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
	if wc.useHashes {
		hash, ok := wc.ridToHash[rid]
		return hash, ok
	}
	if len(wc.localToCustomRID) > 0 {
		if netRID, ok := wc.localToCustomRID[rid]; ok {
			return netRID, true
		}
	}
	return rid, true
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
