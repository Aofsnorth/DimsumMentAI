package bot

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/df-mc/dragonfly/server/block"
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/block/model"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/sandertv/gophertunnel/minecraft/nbt"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// chunkPos encodes a chunk column position (chunk X, chunk Z).
type chunkPos struct {
	X, Z int32
}

// WorldCache stores decoded chunk data received from the server so that the
// pathfinder can query actual block states instead of guessing.
type WorldCache struct {
	mu        sync.RWMutex
	chunks    map[chunkPos]*chunk.Chunk
	airRID    uint32     // runtime ID that represents air
	r         cube.Range // vertical range of the world (usually [-64, 319])
	logger    *slog.Logger
	hashToRID map[uint32]uint32
	useHashes bool
}

// NewWorldCache creates a WorldCache. airRID should be 0 for most Bedrock
// servers (runtime ID 0 = air). The range should be [-64, 319] for overworld.
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

// TranslateRuntimeID converts a network block state hash to a local runtime ID
// when the server sends hashed block palette entries.
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

// precomputeBlockHashes generates FNV-1a hashes for all registered block states
// so that network block hashes can be translated to dragonfly runtime IDs.
func (wc *WorldCache) precomputeBlockHashes() {
	wc.hashToRID = make(map[uint32]uint32)
	if chunk.RuntimeIDToState == nil {
		return
	}

	count := uint32(0)
	for {
		name, properties, found := chunk.RuntimeIDToState(count)
		if !found {
			break
		}

		sNoVer := struct {
			Name       string         `nbt:"name"`
			Properties map[string]any `nbt:"states"`
		}{Name: name, Properties: properties}

		leBytes, err := nbt.MarshalEncoding(sNoVer, nbt.LittleEndian)
		if err == nil {
			h := fnv1a(leBytes)
			wc.hashToRID[h] = count
		}
		count++
	}
}

func fnv1a(data []byte) uint32 {
	var hash uint32 = 0x811c9dc5
	for _, b := range data {
		hash ^= uint32(b)
		hash *= 0x01000193
	}
	return hash
}

// HandleLevelChunk processes a LevelChunk packet and stores the decoded chunk.
func (wc *WorldCache) HandleLevelChunk(pk *packet.LevelChunk) {
	pos := chunkPos{X: pk.Position.X(), Z: pk.Position.Z()}
	count := int(pk.SubChunkCount)

	// SubChunkCount == protocol.SubChunkRequestModeLimitless or Limited means
	// the server will send sub-chunks separately via SubChunk packets.
	if pk.SubChunkCount == protocol.SubChunkRequestModeLimitless ||
		pk.SubChunkCount == protocol.SubChunkRequestModeLimited {
		// Create an empty chunk placeholder that will be filled by SubChunk packets.
		c := chunk.New(wc.airRID, wc.r)
		wc.mu.Lock()
		wc.chunks[pos] = c
		wc.mu.Unlock()
		return
	}

	c, err := chunk.NetworkDecode(wc.airRID, pk.RawPayload, count, wc.r)
	if err != nil {
		wc.logger.Error("WorldCache: failed to decode LevelChunk",
			"chunkX", pos.X, "chunkZ", pos.Z, "error", err)
		return
	}

	wc.mu.Lock()
	wc.chunks[pos] = c
	wc.mu.Unlock()

	wc.logger.Debug("WorldCache: decoded LevelChunk", "chunkX", pos.X, "chunkZ", pos.Z)
}

// HandleSubChunk processes a SubChunk packet and merges the decoded sub-chunk
// entries into the existing chunk columns.
func (wc *WorldCache) HandleSubChunk(pk *packet.SubChunk) {
	for _, entry := range pk.SubChunkEntries {
		if entry.Result != protocol.SubChunkResultSuccess &&
			entry.Result != protocol.SubChunkResultSuccessAllAir {
			continue
		}

		// Compute absolute sub-chunk position from center + offset.
		absX := pk.Position.X() + int32(entry.Offset[0])
		absZ := pk.Position.Z() + int32(entry.Offset[2])
		subY := pk.Position.Y() + int32(entry.Offset[1])

		cPos := chunkPos{X: absX, Z: absZ}

		wc.mu.Lock()
		c, ok := wc.chunks[cPos]
		if !ok {
			c = chunk.New(wc.airRID, wc.r)
			wc.chunks[cPos] = c
		}
		wc.mu.Unlock()

		if entry.Result == protocol.SubChunkResultSuccessAllAir {
			// All air — the chunk.New already initialises sub-chunks as air.
			continue
		}

		// Decode the sub-chunk payload into the existing chunk.
		buf := bytes.NewBuffer(entry.RawPayload)
		// The payload is a single serialised sub-chunk (version byte + storages).
		ver, err := buf.ReadByte()
		if err != nil {
			continue
		}

		// We only handle version 8 and 9 sub-chunks (current Bedrock).
		switch ver {
		case 1:
			storage, err := wc.decodeNetworkPalettedStorage(buf)
			if err != nil {
				continue
			}
			applyStorageToChunk(c, wc.airRID, wc.r, subY, []*palettedResult{storage})
		case 8, 9:
			storageCount, err := buf.ReadByte()
			if err != nil {
				continue
			}
			if ver == 9 {
				// Read the sub-chunk index byte (Y value)
				if _, err := buf.ReadByte(); err != nil {
					continue
				}
			}
			storages := make([]*palettedResult, storageCount)
			valid := true
			for i := byte(0); i < storageCount; i++ {
				storages[i], err = wc.decodeNetworkPalettedStorage(buf)
				if err != nil {
					valid = false
					break
				}
				if storages[i] != nil && len(storages[i].palette) > 0 {
					var names []string
					for _, rid := range storages[i].palette {
						name, _, _ := chunk.RuntimeIDToState(rid)
						if name == "" {
							names = append(names, fmt.Sprintf("unknown(%d)", rid))
						} else {
							names = append(names, name)
						}
					}
					limit := 5
					if len(names) < limit {
						limit = len(names)
					}
					wc.logger.Debug("Decoded storage palette", "layer", i, "bits", storages[i].bitsPerBlock, "paletteCount", len(storages[i].palette), "names", names[:limit])
				}
			}
			if !valid {
				continue
			}
			applyStorageToChunk(c, wc.airRID, wc.r, subY, storages)
			wc.logger.Debug("WorldCache: decoded SubChunk", "chunkX", cPos.X, "chunkZ", cPos.Z, "subY", subY)
		}
	}
}

// BlockRIDAt returns the block runtime ID at the given world coordinates.
// If the chunk is not loaded, returns (0, false).
func (wc *WorldCache) BlockRIDAt(x, y, z int32) (uint32, bool) {
	cPos := chunkPos{X: x >> 4, Z: z >> 4}

	wc.mu.RLock()
	c, ok := wc.chunks[cPos]
	wc.mu.RUnlock()
	if !ok {
		return 0, false
	}

	// Bounds check
	if int(y) < wc.r.Min() || int(y) > wc.r.Max() {
		return wc.airRID, true
	}

	rid := wc.TranslateRuntimeID(c.Block(uint8(x&0xf), int16(y), uint8(z&0xf), 0))
	return rid, true
}

// IsBlockAir checks if the block at the given world coordinates is air.
// Returns (isAir, chunkLoaded).
// GetBlockRID returns the block runtime ID at the given world coordinates.
// If the chunk is not loaded, returns (0, false).
func (wc *WorldCache) GetBlockRID(x, y, z int32) (uint32, bool) {
	cPos := chunkPos{X: x >> 4, Z: z >> 4}
	wc.mu.RLock()
	c, ok := wc.chunks[cPos]
	wc.mu.RUnlock()
	if !ok {
		return 0, false
	}

	if int(y) < wc.r.Min() || int(y) > wc.r.Max() {
		return wc.airRID, true
	}

	rid := wc.TranslateRuntimeID(c.Block(uint8(x&0xf), int16(y), uint8(z&0xf), 0))
	return rid, true
}

// IsBlockAir tetap ada untuk backward compatibility jika ada yang panggil
func (wc *WorldCache) IsBlockAir(x, y, z int32) (bool, bool) {
	rid, loaded := wc.GetBlockRID(x, y, z)
	if !loaded {
		return false, false
	}
	return rid == wc.airRID, true
}

type mockBlockSource struct{}

func (mockBlockSource) Block(cube.Pos) world.Block {
	return block.Air{}
}

func isBlockNamePassable(name string) bool {
	if strings.HasSuffix(name, "_sign") ||
		strings.HasSuffix(name, "_button") ||
		strings.HasSuffix(name, "_sapling") ||
		strings.HasSuffix(name, "_pressure_plate") ||
		(strings.Contains(name, "grass") && !strings.Contains(name, "block") && !strings.Contains(name, "path")) ||
		strings.Contains(name, "fern") ||
		strings.Contains(name, "mushroom") ||
		strings.Contains(name, "roots") ||
		strings.Contains(name, "vines") ||
		strings.Contains(name, "carpet") ||
		(strings.Contains(name, "coral") && !strings.Contains(name, "block")) ||
		strings.Contains(name, "crop") ||
		strings.Contains(name, "rail") ||
		strings.Contains(name, "torch") {
		return true
	}

	switch name {
	case "minecraft:air", "minecraft:water", "minecraft:flowing_water", "minecraft:lava", "minecraft:flowing_lava",
		"minecraft:ladder", "minecraft:tripwire", "minecraft:trip_wire", "minecraft:tripwire_hook", "minecraft:lever",
		"minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots", "minecraft:nether_wart",
		"minecraft:sugar_cane", "minecraft:sweet_berry_bush", "minecraft:glow_lichen", "minecraft:vine", "minecraft:fire",
		"minecraft:poppy", "minecraft:dandelion", "minecraft:blue_orchid", "minecraft:allium", "minecraft:azure_bluet",
		"minecraft:red_tulip", "minecraft:orange_tulip", "minecraft:white_tulip", "minecraft:pink_tulip",
		"minecraft:oxeye_daisy", "minecraft:cornflower", "minecraft:lily_of_the_valley", "minecraft:wither_rose",
		"minecraft:sunflower", "minecraft:lilac", "minecraft:rose_bush", "minecraft:peony", "minecraft:pitcher_plant",
		"minecraft:torchflower":
		return true
	}
	return false
}

// IsRIDSolid checks if the given block runtime ID is solid.
func (wc *WorldCache) IsRIDSolid(rid uint32) bool {
	rid = wc.TranslateRuntimeID(rid)
	name, _, ok := chunk.RuntimeIDToState(rid)
	if ok && isBlockNamePassable(name) {
		return false
	}

	b, ok := world.BlockByRuntimeID(rid)
	if !ok {
		return true // Default to solid if unknown
	}

	m := b.Model()
	if m == nil {
		return false
	}

	if _, isEmpty := m.(model.Empty); isEmpty {
		return false
	}

	boxes := m.BBox(cube.Pos{}, mockBlockSource{})
	return len(boxes) > 0
}

// IsBlockSolid checks if the block at the given coordinates is solid.
func (wc *WorldCache) IsBlockSolid(x, y, z int32) (bool, bool) {
	rid, loaded := wc.GetBlockRID(x, y, z)
	if !loaded {
		return false, false
	}
	return wc.IsRIDSolid(rid), true
}

// ChunkCount returns the number of chunks currently cached.
func (wc *WorldCache) ChunkCount() int {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	return len(wc.chunks)
}

// -----------------------------------------------------------
// Minimal network paletted storage decoder
// We decode directly from the Bedrock network format without
// needing dragonfly's full block registry.
// -----------------------------------------------------------

// palettedResult is a decoded paletted storage with runtime IDs.
type palettedResult struct {
	bitsPerBlock byte
	blocks       []uint32 // raw uint32 words
	palette      []uint32 // runtime ID palette
}

// runtimeIDAt returns the runtime ID stored at local (x, y, z) in [0..15].
func (p *palettedResult) runtimeIDAt(x, y, z byte) uint32 {
	if p.bitsPerBlock == 0 {
		if len(p.palette) > 0 {
			return p.palette[0]
		}
		return 0
	}

	// Guard against invalid bitsPerBlock values that could cause divide-by-zero
	if p.bitsPerBlock > 32 {
		if len(p.palette) > 0 {
			return p.palette[0]
		}
		return 0
	}

	offset := int(uint16(x)<<8|uint16(z)<<4|uint16(y)) * int(p.bitsPerBlock)
	filledBits := int(32 / p.bitsPerBlock * p.bitsPerBlock)
	if filledBits == 0 {
		if len(p.palette) > 0 {
			return p.palette[0]
		}
		return 0
	}
	uint32Offset := offset / filledBits
	bitOffset := uint(offset % filledBits)
	mask := uint32((1 << p.bitsPerBlock) - 1)

	if uint32Offset >= len(p.blocks) {
		return 0
	}
	index := (p.blocks[uint32Offset] >> bitOffset) & mask
	if int(index) >= len(p.palette) {
		return 0
	}
	return p.palette[index]
}

func (wc *WorldCache) decodeNetworkPalettedStorage(buf *bytes.Buffer) (*palettedResult, error) {
	blockSizeByte, err := buf.ReadByte()
	if err != nil {
		return nil, err
	}
	bitsPerBlock := blockSizeByte >> 1

	if bitsPerBlock == 0x7f {
		// Refers to previous storage
		return &palettedResult{}, nil
	}

	// Calculate number of uint32s needed
	uint32Count := 0
	if bitsPerBlock > 0 {
		if bitsPerBlock > 32 {
			// Invalid bits per block, treat as single-palette storage
			return &palettedResult{bitsPerBlock: 0}, nil
		}
		blocksPerUint32 := 32 / int(bitsPerBlock)
		if blocksPerUint32 == 0 {
			return &palettedResult{bitsPerBlock: 0}, nil
		}
		uint32Count = 4096 / blocksPerUint32
		if 4096%blocksPerUint32 != 0 {
			uint32Count++
		}
	}

	// Read block data
	blocks := make([]uint32, uint32Count)
	for i := 0; i < uint32Count; i++ {
		data := buf.Next(4)
		if len(data) < 4 {
			return nil, bytes.ErrTooLarge
		}
		blocks[i] = uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
	}

	// Read palette (varint32 encoded)
	var paletteCount int32 = 1
	if bitsPerBlock != 0 {
		paletteCount, err = readVarint32(buf)
		if err != nil {
			return nil, err
		}
		if paletteCount <= 0 {
			paletteCount = 1
		}
	}

	palette := make([]uint32, paletteCount)
	for i := int32(0); i < paletteCount; i++ {
		v, err := readVarint32(buf)
		if err != nil {
			return nil, err
		}
		palette[i] = wc.TranslateRuntimeID(uint32(v))
	}

	return &palettedResult{
		bitsPerBlock: bitsPerBlock,
		blocks:       blocks,
		palette:      palette,
	}, nil
}

func readVarint32(buf *bytes.Buffer) (int32, error) {
	var val int32
	err := protocol.Varint32(buf, &val)
	return val, err
}

// applyStorageToChunk writes the decoded paletted storage data into the chunk
// at the given sub-chunk Y index.
func applyStorageToChunk(c *chunk.Chunk, airRID uint32, r cube.Range, subY int32, storages []*palettedResult) {
	if len(storages) == 0 {
		return
	}

	baseWorldY := subY * 16

	// Write blocks from the primary storage (layer 0) into the chunk.
	primary := storages[0]
	if primary == nil {
		return
	}

	for lx := byte(0); lx < 16; lx++ {
		for ly := byte(0); ly < 16; ly++ {
			for lz := byte(0); lz < 16; lz++ {
				rid := primary.runtimeIDAt(lx, ly, lz)
				worldY := int16(int32(baseWorldY) + int32(ly))
				if int(worldY) < r.Min() || int(worldY) > r.Max() {
					continue
				}
				c.SetBlock(lx, worldY, lz, 0, rid)
			}
		}
	}
}
