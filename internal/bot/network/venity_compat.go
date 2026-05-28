package network

import (
	"context"
	"sync/atomic"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/network/world"
	"bedrock-ai/internal/debuglog"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

var venityHandshakeSent atomic.Bool

// VenityCompatLoop sends extra client signals after Venity's large initial chunk burst.
// Other servers do not need this; logs show Venity drops the session ~30s after spawn otherwise.
func VenityCompatLoop(ctx context.Context, b *bot.Bot) {
	if !b.VenityCompat {
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastCount uint64
	stable := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if venityHandshakeSent.Load() {
				return
			}
			cur := world.LevelChunkReceivedCount()
			if cur == lastCount && cur > 20 {
				stable++
			} else {
				stable = 0
				lastCount = cur
			}
			if stable >= 4 {
				sendVenityLoadedHandshake(b, cur)
				return
			}
		}
	}
}

func sendVenityLoadedHandshake(b *bot.Bot, chunkCount uint64) {
	if !venityHandshakeSent.CompareAndSwap(false, true) {
		return
	}

	_ = b.Conn.WritePacket(&packet.ServerBoundLoadingScreen{Type: packet.LoadingScreenTypeStart})
	_ = b.Conn.WritePacket(&packet.ServerBoundLoadingScreen{Type: packet.LoadingScreenTypeEnd})
	_ = b.Conn.WritePacket(&packet.RequestChunkRadius{
		ChunkRadius:    int32(b.Conn.ChunkRadius()),
		MaxChunkRadius: 32,
	})
	_ = b.Conn.WritePacket(&packet.SetLocalPlayerAsInitialised{
		EntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
	})
	_ = b.Conn.Flush()

	// Mark tick as synced so PlayerAuthInput starts sending ClientAckServerData.
	// Venity uses RewindMovement but never sends CorrectPlayerMovePrediction,
	// so syncServerTick() is never called and TickSynced stays false.
	// Without TickSynced, clientAck is never set and the server rejects movement.
	b.Mu.Lock()
	b.TickSynced = true
	b.Mu.Unlock()

	// Send initial ClientMovementPredictionSync so the server knows the client's
	// movement model (bounding box, speed) before accepting PlayerAuthInput.
	if b.RewindMovement {
		b.Conn.WritePacket(&packet.ClientMovementPredictionSync{
			ActorFlags:            protocol.NewBitset(protocol.EntityDataFlagCount),
			EntityUniqueID:        b.Conn.GameData().EntityUniqueID,
			BoundingBoxWidth:      0.6,
			BoundingBoxHeight:     1.8,
			MovementSpeed:         0.1,
		})
	}

	b.Logger.Info("venity compat: sent post-chunk-load handshake",
		"chunks_seen", chunkCount,
	)
	// #region agent log
	debuglog.Log("L", "venity_compat.go:handshake", "venity post-load handshake sent", map[string]any{
		"chunkCount":  chunkCount,
		"tickSynced":  true,
		"rewindMovement": b.RewindMovement,
		"runId":       "venity-fix",
	})
	// #endregion
}
