// Package world provides the bot's world model, chunk cache, and packet handlers
// for terrain data.
package world

import (
	"bytes"
	"context"
	"fmt"

	"bedrock-ai/internal/safecast"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// maxSubChunkCount is the highest sub-chunk count a LevelChunk payload can
// carry. Counts above it are request-mode markers rather than real counts, and
// the real sub-chunks arrive separately as SubChunk packets.
const maxSubChunkCount = 64

// HandleLevelChunk processes a LevelChunk packet and stores the decoded chunk.
func (wc *WorldCache) HandleLevelChunk(pk *packet.LevelChunk) {
	pos := chunkPos{X: pk.Position.X(), Z: pk.Position.Z()}
	count := int(pk.SubChunkCount)

	if count == 0 || count > maxSubChunkCount {
		// Request mode: the packet carries no sub-chunk data, and the real
		// terrain arrives later as SubChunk packets. Storing an empty air column
		// here would both wipe any data already loaded for this position and
		// report "loaded, all air" to the pathfinder, hiding the fact that the
		// chunk is simply unknown.
		return
	}

	c, err := chunk.NetworkDecode(wc.airRID, pk.RawPayload, count, wc.r)
	if err != nil {
		if wc.logger != nil {
			wc.logger.Error("WorldCache: failed to decode LevelChunk",
				"chunkX", pos.X, "chunkZ", pos.Z, "error", err)
		}
		return
	}

	wc.mu.Lock()
	wc.chunks[pos] = c
	wc.mu.Unlock()

	if wc.logger != nil {
		wc.logger.Debug("WorldCache: decoded LevelChunk", "chunkX", pos.X, "chunkZ", pos.Z)
	}
}

// HandleSubChunk processes a SubChunk packet and merges the decoded sub-chunk.
func (wc *WorldCache) HandleSubChunk(pk *packet.SubChunk) {
	for _, entry := range pk.SubChunkEntries {
		if !isSubChunkSuccess(entry.Result) {
			continue
		}
		wc.applySubChunkEntry(entry, pk.Position)
	}
}

func isSubChunkSuccess(result byte) bool {
	return result == protocol.SubChunkResultSuccess || result == protocol.SubChunkResultSuccessAllAir
}

func (wc *WorldCache) applySubChunkEntry(entry protocol.SubChunkEntry, pos protocol.SubChunkPos) {
	absX := pos.X() + int32(entry.Offset[0])
	absZ := pos.Z() + int32(entry.Offset[2])
	subY := pos.Y() + int32(entry.Offset[1])
	cPos := chunkPos{X: absX, Z: absZ}

	c := wc.getOrCreateChunk(cPos)
	if entry.Result == protocol.SubChunkResultSuccessAllAir {
		return
	}

	rawPayload, ok := entry.RawPayload.Value()
	if !ok {
		return
	}

	storages, ok := wc.decodeSubChunkPayload(rawPayload)
	if !ok {
		return
	}
	applyStorageToChunk(c, wc.airRID, wc.r, subY, storages)

	wc.mu.Lock()
	wc.subChunksApplied++
	applied := wc.subChunksApplied
	wc.mu.Unlock()

	// First real terrain is the breadcrumb that proves the world model is
	// populated. Log it at info level: everything downstream (A*, block
	// scanning, scaffolding) is meaningless while this counter is still zero.
	if applied == 1 && wc.logger != nil {
		wc.logger.Info("WorldCache: first terrain sub-chunk decoded",
			"chunkX", cPos.X, "chunkZ", cPos.Z, "subY", subY, "chunks", wc.ChunkCount())
	}
	if wc.logger != nil && wc.logger.Enabled(context.TODO(), -4) {
		wc.logger.Debug("WorldCache: decoded SubChunk", "chunkX", cPos.X, "chunkZ", cPos.Z, "subY", subY)
	}
}

func (wc *WorldCache) getOrCreateChunk(cPos chunkPos) *chunk.Chunk {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	if c, ok := wc.chunks[cPos]; ok {
		return c
	}
	c := chunk.New(wc.airRID, wc.r)
	wc.chunks[cPos] = c
	return c
}

func (wc *WorldCache) decodeSubChunkPayload(raw []byte) ([]*palettedResult, bool) {
	buf := bytes.NewBuffer(raw)
	ver, err := buf.ReadByte()
	if err != nil {
		return nil, false
	}

	switch ver {
	case 1:
		return wc.decodeVersion1Payload(buf)
	case 8, 9:
		return wc.decodeVersion89Payload(buf, ver)
	default:
		return nil, false
	}
}

func (wc *WorldCache) decodeVersion1Payload(buf *bytes.Buffer) ([]*palettedResult, bool) {
	storage, err := wc.decodeNetworkPalettedStorage(buf)
	if err != nil {
		return nil, false
	}
	return []*palettedResult{storage}, true
}

func (wc *WorldCache) decodeVersion89Payload(buf *bytes.Buffer, ver byte) ([]*palettedResult, bool) {
	storageCount, err := buf.ReadByte()
	if err != nil {
		return nil, false
	}
	if ver == 9 {
		if _, err := buf.ReadByte(); err != nil {
			return nil, false
		}
	}

	storages := make([]*palettedResult, storageCount)
	for i := byte(0); i < storageCount; i++ {
		storages[i], err = wc.decodeNetworkPalettedStorage(buf)
		if err != nil {
			return nil, false
		}
		wc.logStoragePalette(storages[i], i)
	}
	return storages, true
}

func (wc *WorldCache) logStoragePalette(storage *palettedResult, layer byte) {
	if wc.logger == nil || !wc.logger.Enabled(context.TODO(), -4) || storage == nil || len(storage.palette) == 0 {
		return
	}

	names := make([]string, 0, len(storage.palette))
	for _, rid := range storage.palette {
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
	wc.logger.Debug("Decoded storage palette", "layer", layer, "bits", storage.bitsPerBlock, "paletteCount", len(storage.palette), "names", names[:limit])
}

func applyStorageToChunk(c *chunk.Chunk, airRID uint32, r cube.Range, subY int32, storages []*palettedResult) {
	if len(storages) == 0 {
		return
	}

	baseWorldY := subY * 16
	primary := storages[0]
	if primary == nil {
		return
	}

	for lx := byte(0); lx < 16; lx++ {
		for ly := byte(0); ly < 16; ly++ {
			for lz := byte(0); lz < 16; lz++ {
				rid := primary.runtimeIDAt(lx, ly, lz)
				worldY := safecast.To[int16](baseWorldY + int32(ly))
				if int(worldY) < r.Min() || int(worldY) > r.Max() {
					continue
				}
				c.SetBlock(lx, worldY, lz, 0, rid)
			}
		}
	}
}
