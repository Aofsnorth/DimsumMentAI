// Package world handles world-packet processing for chunk and block updates.
package world

import (
	"log/slog"
	"math"
	"sync/atomic"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/dimension"
	"bedrock-ai/internal/debuglog"
	"bedrock-ai/internal/evidence"
	"bedrock-ai/internal/safecast"

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
		handleLevelChunk(b, p)
		return true
	case *packet.ClientCacheMissResponse:
		handleClientCacheMissResponse(b, p)
		return true
	case *packet.SubChunk:
		b.WorldCache.HandleSubChunk(p)
		return true
	case *packet.UpdateSubChunkBlocks:
		handleUpdateSubChunkBlocks(b, p)
		return true
	case *packet.UpdateBlock:
		handleUpdateBlock(b, p)
		return true
	}
	return false
}

func handleLevelChunk(b *bot.Bot, p *packet.LevelChunk) {
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

	// Since protocol 800 (1.21.100+) Bedrock stopped inlining sub-chunk data in
	// LevelChunk: every chunk packet arrives with SubChunkCount == 0 and the
	// terrain only comes back as SubChunk packets after the client asks for it.
	// Decoding a zero-count payload produced an all-air column, which made the
	// whole WorldCache look like empty space — A* vetoed every neighbour and the
	// block scanner never found anything. So request mode has to be detected
	// before anything is stored.
	if isSubChunkRequestMode(p) {
		handleSubChunkRequestMode(b, p)
		return
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
}

// isSubChunkRequestMode reports whether the server expects the client to pull
// sub-chunk data instead of shipping it inside LevelChunk. Pre-800 servers
// signalled this with a count above maxSubChunkCount; current servers just send
// a count of 0 alongside a payload that is not a classic sub-chunk array.
func isSubChunkRequestMode(p *packet.LevelChunk) bool {
	return p.SubChunkCount == 0 || p.SubChunkCount > maxSubChunkCount
}

func handleSubChunkRequestMode(b *bot.Bot, p *packet.LevelChunk) {
	limit := int32(0)
	if v, ok := p.SubChunkLimit.Value(); ok && v > 0 {
		limit = v
	}

	b.Mu.Lock()
	first := !b.SubChunkRequestMode
	previousDimension := b.ChunkDimension
	b.SubChunkRequestMode = true
	b.ChunkDimension = p.Dimension
	b.Dimension = dimension.FromServerID(p.Dimension)
	b.SubChunkLimit = limit
	noSubChunks := b.GeyserNoSubChunks
	changed := !first && previousDimension != p.Dimension
	b.Mu.Unlock()

	if changed {
		// The cache is full of the world the bot just left. Every block in it is
		// a lie from here on: pathfinding would plan through overworld terrain
		// that is now a mile underground, storage would look for chests in cells
		// full of netherrack, and every "is that solid" answer would be about
		// somewhere the bot is not.
		//
		// Nothing in the protocol says "dimension changed", so this comparison is
		// the only signal there is. Skipping it is the difference between a bot
		// that knows it is in the Nether and one that confidently mines a wall of
		// end stone while believing it is home.
		if b.WorldCache != nil {
			b.WorldCache.Reset()
		}
		// The local world model is handed out as a three-method interface, and
		// clearing it is not one of them. Narrowing here rather than widening the
		// interface keeps the change off every implementer in the tree.
		if model, ok := b.GetLocalWorldModel().(interface{ Reset() }); ok {
			model.Reset()
		}
		b.Logger.Info("AGI: crossed a dimension",
			"from", dimension.FromServerID(previousDimension).String(),
			"to", b.Dimension.String(),
		)
		b.Evidence.Record(evidence.KindDimension, b.Dimension.String(), map[string]any{
			"from_id": previousDimension,
			"to_id":   p.Dimension,
			"from":    dimension.FromServerID(previousDimension).String(),
			"to":      b.Dimension.String(),
		})
	}

	if first {
		b.Logger.Info("server uses sub-chunk request mode; terrain arrives as SubChunk packets",
			"dimension", p.Dimension,
			"sub_chunk_limit", limit,
			"payload_len", len(p.RawPayload),
		)
	}

	// Geyser-fronted servers (GeyserNoSubChunks) drop the session on the first
	// SubChunkRequest: captured live, the server went permanently silent 7ms
	// after this packet left and never answered anything again, while the same
	// dial stack without it stays connected. Those servers still stream
	// data-carrying LevelChunks once a chunk radius has been requested, so the
	// bot keeps its terrain without asking for sub-chunks.
	if noSubChunks {
		if first {
			b.Logger.Debug("skipping eager SubChunkRequest (Geyser profile)",
				"chunkX", p.Position.X(), "chunkZ", p.Position.Z())
		}
		return
	}

	// Ask for the chunk that triggered this straight away. The background
	// requester in ChunkRequesterLoop covers the surrounding 5x5.
	pos := b.GetCoords()
	SendSubChunkRequest(b, p.Position.X(), p.Position.Z(),
		SubChunkRow(int32(pos.Y())), p.Dimension, limit)
}

// maxSubChunkCount is the highest sub-chunk count a LevelChunk payload can
// carry. The server signals request mode by sending a count above this, in
// which case the real sub-chunks must be requested explicitly.
const maxSubChunkCount = 64

const (
	// minSubChunkY and maxSubChunkY bound the overworld column in sub-chunk
	// units, matching the bot's world range of [-64, 319].
	minSubChunkY int32 = -4
	maxSubChunkY int32 = 19
	// fullColumnSubChunks is the number of sub-chunks in one full column.
	fullColumnSubChunks = maxSubChunkY - minSubChunkY + 1
)

// SubChunkRow converts a world Y coordinate into its sub-chunk row index.
func SubChunkRow(y int32) int32 {
	return y >> 4
}

// SubChunkOffsets builds the vertical window of sub-chunk offsets to request,
// centred on the row the bot is standing in. The server advertises
// SubChunkLimit as a cap on how many sub-chunks a single request may carry, so
// the window is clamped to that cap; overshooting it makes the server reject the
// whole request and return nothing.
func SubChunkOffsets(centerRow, limit int32) []protocol.SubChunkOffset {
	if limit <= 0 || limit > fullColumnSubChunks {
		limit = fullColumnSubChunks
	}

	start := centerRow - limit/2
	if start < minSubChunkY {
		start = minSubChunkY
	}
	if start+limit-1 > maxSubChunkY {
		start = maxSubChunkY - limit + 1
	}

	offsets := make([]protocol.SubChunkOffset, 0, limit)
	for y := start; y < start+limit; y++ {
		offsets = append(offsets, protocol.SubChunkOffset{0, safecast.To[int8](y), 0})
	}
	return offsets
}

// SendSubChunkRequest asks the server for one chunk column and flushes the
// packet. A SubChunkRequest for a chunk the server has not generated is
// answered with SubChunkResultChunkNotFound, which is harmless.
func SendSubChunkRequest(b *bot.Bot, chunkX, chunkZ, centerRow, dimension, limit int32) {
	_ = b.Conn.WritePacket(&packet.SubChunkRequest{
		Dimension: dimension,
		Position:  protocol.SubChunkPos{chunkX, 0, chunkZ},
		Offsets:   SubChunkOffsets(centerRow, limit),
	})
	_ = b.Conn.Flush()
}

// RequestChunkRadius asks the server to stream a chunk radius around the bot.
//
// A natural Bedrock client sends this right after spawning, and a server can
// treat its absence as a malformed join: Geyser in particular, and the Java
// server behind it, decide what to stream based on it. Without it the bot can
// connect, spawn, and then sit in a world the server never bothered to fill —
// which reads as a silent, intermittent disconnect depending on which server
// software is in front.
//
// The radius is a request, not a demand: the server clamps it to what it is
// willing to send, so this asks for a sane window and lets the server decide.
func RequestChunkRadius(b *bot.Bot, radius int32) {
	_ = b.Conn.WritePacket(&packet.RequestChunkRadius{
		ChunkRadius:    radius,
		MaxChunkRadius: uint8(radius),
	})
	_ = b.Conn.Flush()
	b.Logger.Info("chunk radius requested", slog.Int("radius", int(radius)))
}

// DefaultChunkRadius is the radius the bot requests. Exported so main can wire
// the function pointer without duplicating the number.
const DefaultChunkRadius int32 = 8

func handleClientCacheMissResponse(b *bot.Bot, p *packet.ClientCacheMissResponse) {
	blobs := make(map[uint64][]byte, len(p.Blobs))
	for _, blob := range p.Blobs {
		blobs[blob.Hash] = blob.Payload
	}
	b.WorldCache.StoreBlobs(blobs)
}

func handleUpdateBlock(b *bot.Bot, p *packet.UpdateBlock) {
	applyBlockChange(b, p.Position.X(), p.Position.Y(), p.Position.Z(), p.NewBlockRuntimeID)
}

// handleUpdateSubChunkBlocks applies the batched block changes that modern
// Bedrock sends instead of one UpdateBlock per change. Without this the world
// model goes stale as soon as the bot places or breaks anything, and both A*
// and the block scanner keep planning against the pre-change terrain.
func handleUpdateSubChunkBlocks(b *bot.Bot, p *packet.UpdateSubChunkBlocks) {
	for _, entry := range p.Blocks {
		pos := entry.BlockPos
		applyBlockChange(b, pos.X(), pos.Y(), pos.Z(), entry.BlockRuntimeID)
	}
}

func applyBlockChange(b *bot.Bot, x, y, z int32, wireRID uint32) {
	b.WorldCache.SetBlockRID(x, y, z, wireRID)
	localRID := b.WorldCache.TranslateRuntimeID(wireRID)
	b.WorldModel.SetSolid(x, y, z, b.WorldCache.IsRIDSolid(localRID))
	b.NotifyBlockUpdate(protocol.BlockPos{x, y, z}, localRID)
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
