package world

import (
	"bedrock-ai/internal/bot"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func HandleWorldPacket(b *bot.Bot, pk packet.Packet) bool {
	switch p := pk.(type) {
	case *packet.LevelChunk:
		b.WorldCache.HandleLevelChunk(p)

		if p.SubChunkCount == protocol.SubChunkRequestModeLimitless || p.SubChunkCount == protocol.SubChunkRequestModeLimited {
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
		}
		return true

	case *packet.SubChunk:
		b.WorldCache.HandleSubChunk(p)
		return true

	case *packet.UpdateBlock:
		isSolid := b.WorldCache.IsRIDSolid(p.NewBlockRuntimeID)
		b.WorldModel.SetSolid(p.Position.X(), p.Position.Y(), p.Position.Z(), isSolid)
		return true
	}

	return false
}
