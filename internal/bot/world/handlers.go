package world

import (
	"bytes"
	"fmt"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// HandleLevelChunk processes a LevelChunk packet and stores the decoded chunk.
func (wc *WorldCache) HandleLevelChunk(pk *packet.LevelChunk) {
	pos := chunkPos{X: pk.Position.X(), Z: pk.Position.Z()}
	count := int(pk.SubChunkCount)

	if pk.SubChunkCount == protocol.SubChunkRequestModeLimitless ||
		pk.SubChunkCount == protocol.SubChunkRequestModeLimited {
		c := chunk.New(wc.airRID, wc.r)
		wc.mu.Lock()
		wc.chunks[pos] = c
		wc.mu.Unlock()
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
		if entry.Result != protocol.SubChunkResultSuccess &&
			entry.Result != protocol.SubChunkResultSuccessAllAir {
			continue
		}

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
			continue
		}

		buf := bytes.NewBuffer(entry.RawPayload)
		ver, err := buf.ReadByte()
		if err != nil {
			continue
		}

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
				if wc.logger != nil && wc.logger.Enabled(nil, -4) && storages[i] != nil && len(storages[i].palette) > 0 { // -4 is Debug
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
			if wc.logger != nil {
				wc.logger.Debug("WorldCache: decoded SubChunk", "chunkX", cPos.X, "chunkZ", cPos.Z, "subY", subY)
			}
		}
	}
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
				worldY := int16(int32(baseWorldY) + int32(ly))
				if int(worldY) < r.Min() || int(worldY) > r.Max() {
					continue
				}
				c.SetBlock(lx, worldY, lz, 0, rid)
			}
		}
	}
}
