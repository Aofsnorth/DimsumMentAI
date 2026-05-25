package bot

import (
	"bytes"
	"log/slog"
	"sync"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
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
	mu     sync.RWMutex
	chunks map[chunkPos]*chunk.Chunk
	airRID uint32     // runtime ID that represents air
	r      cube.Range // vertical range of the world (usually [-64, 319])
	logger *slog.Logger
}

// NewWorldCache creates a WorldCache. airRID should be 0 for most Bedrock
// servers (runtime ID 0 = air). The range should be [-64, 319] for overworld.
func NewWorldCache(airRID uint32, r cube.Range, logger *slog.Logger) *WorldCache {
	return &WorldCache{
		chunks: make(map[chunkPos]*chunk.Chunk),
		airRID: airRID,
		r:      r,
		logger: logger,
	}
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
		wc.logger.Debug("WorldCache: failed to decode LevelChunk",
			"chunkX", pos.X, "chunkZ", pos.Z, "error", err)
		return
	}

	wc.mu.Lock()
	wc.chunks[pos] = c
	wc.mu.Unlock()

	wc.logger.Info("WorldCache: decoded LevelChunk", "chunkX", pos.X, "chunkZ", pos.Z)
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
			storage, err := decodeNetworkPalettedStorage(buf)
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
				storages[i], err = decodeNetworkPalettedStorage(buf)
				if err != nil {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			applyStorageToChunk(c, wc.airRID, wc.r, subY, storages)
			wc.logger.Info("WorldCache: decoded SubChunk", "chunkX", cPos.X, "chunkZ", cPos.Z, "subY", subY)
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

	rid := c.Block(uint8(x&0xf), int16(y), uint8(z&0xf), 0)
	return rid, true
}

// IsBlockAir checks if the block at the given world coordinates is air.
// Returns (isAir, chunkLoaded).
func (wc *WorldCache) IsBlockAir(x, y, z int32) (bool, bool) {
	rid, loaded := wc.BlockRIDAt(x, y, z)
	if !loaded {
		return false, false
	}
	return rid == wc.airRID, true
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

	offset := int(uint16(x)<<8|uint16(z)<<4|uint16(y)) * int(p.bitsPerBlock)
	filledBits := int(32 / p.bitsPerBlock * p.bitsPerBlock)
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

func decodeNetworkPalettedStorage(buf *bytes.Buffer) (*palettedResult, error) {
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
		blocksPerUint32 := 32 / int(bitsPerBlock)
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
		palette[i] = uint32(v)
	}

	return &palettedResult{
		bitsPerBlock: bitsPerBlock,
		blocks:       blocks,
		palette:      palette,
	}, nil
}

func readVarint32(buf *bytes.Buffer) (int32, error) {
	var val uint32
	for i := uint(0); i < 35; i += 7 {
		b, err := buf.ReadByte()
		if err != nil {
			return 0, err
		}
		val |= uint32(b&0x7f) << i
		if b&0x80 == 0 {
			break
		}
	}
	// Zig-zag decode
	return int32((val >> 1) ^ -(val & 1)), nil
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
