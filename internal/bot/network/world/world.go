package world

import (
	"math"
	"sync/atomic"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/debuglog"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

var levelChunkReceived atomic.Uint64

// LevelChunkReceivedCount returns how many LevelChunk packets were received this session.
func LevelChunkReceivedCount() uint64 {
	return levelChunkReceived.Load()
}

func HandleWorldPacket(b *bot.Bot, pk packet.Packet) bool {
	switch p := pk.(type) {
	case *packet.LevelChunk:
		n := levelChunkReceived.Add(1)
		if n <= 2 || n%40 == 0 {
			// #region agent log
			debuglog.Log("G", "world.go:LevelChunk", "level chunk received", map[string]any{
				"count":         n,
				"subChunkCount": p.SubChunkCount,
				"cacheEnabled":  p.CacheEnabled,
				"payloadLen":    len(p.RawPayload),
				"runId":         "post-fix-v2",
			})
			// #endregion
		}

		if p.CacheEnabled && len(p.BlobHashes) > 0 {
			_ = b.Conn.WritePacket(&packet.ClientCacheBlobStatus{
				MissHashes: append([]uint64(nil), p.BlobHashes...),
			})
			_ = b.Conn.Flush()
		}

		// Venity hub floods 400+ full chunks at spawn. Decode only chunks
		// near the bot/active target so pathfinding has local ground data
		// without making the packet loop chew through the whole flood.
		if shouldDecodeLevelChunk(b, p) {
			pkCopy := *p
			if len(p.RawPayload) > 0 {
				pkCopy.RawPayload = append([]byte(nil), p.RawPayload...)
			}
			go b.WorldCache.HandleLevelChunk(&pkCopy)
		}

		if p.SubChunkCount == protocol.SubChunkRequestModeLimitless || p.SubChunkCount == protocol.SubChunkRequestModeLimited {
			b.Mu.Lock()
			b.SubChunkRequestMode = true
			b.Mu.Unlock()

			highestY := int32(25)
			if p.SubChunkCount == protocol.SubChunkRequestModeLimited {
				highestY = int32(p.HighestSubChunk)
			}

			var offsets []protocol.SubChunkOffset
			for y := int32(-4); y <= highestY; y++ {
				offsets = append(offsets, protocol.SubChunkOffset{0, int8(y), 0})
			}

			_ = b.Conn.WritePacket(&packet.SubChunkRequest{
				Dimension: p.Dimension,
				Position: protocol.SubChunkPos{
					p.Position[0],
					0,
					p.Position[1],
				},
				Offsets: offsets,
			})
			_ = b.Conn.Flush()
		}
		return true

	case *packet.ClientCacheMissResponse:
		blobs := make(map[uint64][]byte, len(p.Blobs))
		for _, blob := range p.Blobs {
			blobs[blob.Hash] = blob.Payload
		}
		b.WorldCache.StoreBlobs(blobs)
		return true

	case *packet.SubChunk:
		b.WorldCache.HandleSubChunk(p)
		return true

	case *packet.UpdateBlock:
		b.WorldCache.SetBlockRID(p.Position.X(), p.Position.Y(), p.Position.Z(), p.NewBlockRuntimeID)
		isSolid := b.WorldCache.IsRIDSolid(p.NewBlockRuntimeID)
		b.WorldModel.SetSolid(p.Position.X(), p.Position.Y(), p.Position.Z(), isSolid)
		return true
	}

	return false
}

func shouldDecodeLevelChunk(b *bot.Bot, p *packet.LevelChunk) bool {
	if !b.VenityCompat {
		return true
	}

	b.Mu.Lock()
	pos := b.Pos
	target := b.TargetPos
	movementState := b.MovementState
	b.Mu.Unlock()

	if chunkWithinRadius(p.Position, pos, 2) {
		return true
	}
	if movementState != "idle" && chunkWithinRadius(p.Position, target, 2) {
		return true
	}
	return false
}

func chunkWithinRadius(chunkPos [2]int32, pos mgl32.Vec3, radius int32) bool {
	chunkX := int32(math.Floor(float64(pos.X()) / 16.0))
	chunkZ := int32(math.Floor(float64(pos.Z()) / 16.0))
	dx := abs32(chunkPos[0] - chunkX)
	dz := abs32(chunkPos[1] - chunkZ)
	return dx <= radius && dz <= radius
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
