package network

import (
	"context"
	"sync/atomic"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/network/world"
	"bedrock-ai/internal/debuglog"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

var venityHandshakeSent atomic.Bool

// VenityCompatLoop sends extra client signals after Venity's large initial chunk burst.
// Other servers do not need this; logs show Venity drops the session ~30s after spawn otherwise.
func VenityCompatLoop(ctx context.Context, b *bot.Bot) {
	if !b.VenityCompat {
		return
	}
	venityHandshakeSent.Store(false)
	
	// Send StartLoading as soon as we start listening, to mimic the real client
	// notifying the server/proxy that it's beginning to process chunks.
	_ = b.Conn.WritePacket(&packet.ServerBoundLoadingScreen{
		Type: packet.LoadingScreenTypeStart,
	})

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
			if cur == lastCount {
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

	b.Logger.Info("venity compat: sent post-chunk-load handshake",
		"chunks_seen", chunkCount,
	)

	// Send EndLoading to mimic the real client notifying the server/proxy
	// that it has finished rendering chunks.
	_ = b.Conn.WritePacket(&packet.ServerBoundLoadingScreen{
		Type: packet.LoadingScreenTypeEnd,
	})

	// Also send SetLocalPlayerAsInitialised in case the proxy transferred us
	// to a backend server that expects it, but gophertunnel didn't send it
	// because it didn't receive a second ChunkRadiusUpdated.
	_ = b.Conn.WritePacket(&packet.SetLocalPlayerAsInitialised{
		EntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
	})

	// #region agent log
	debuglog.Log("L", "venity_compat.go:handshake", "venity post-load handshake sent", map[string]any{
		"chunkCount":  chunkCount,
		"tickSynced":  true,
		"rewindMovement": b.RewindMovement,
		"runId":       "venity-fix",
	})
	// #endregion
}
