package player

import (
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/debuglog"
)

// syncServerTick aligns PlayerAuthInput ticks with the server's tick counter.
func syncServerTick(b *bot.Bot, serverTick uint64, source string) {
	b.Mu.Lock()
	prev := b.ServerTick
	synced := b.TickSynced
	if !synced || serverTick+1 > b.ServerTick {
		b.ServerTick = serverTick + 1
		b.TickSynced = true
	}
	newTick := b.ServerTick
	b.Mu.Unlock()

	if !synced || prev != newTick {
		// #region agent log
		debuglog.Log("N", "player/tick_sync.go:syncServerTick", "server tick aligned", map[string]any{
			"source":     source,
			"serverTick": serverTick,
			"prevTick":   prev,
			"newTick":    newTick,
			"firstSync":  !synced,
			"runId":      "tick-fix-v2",
		})
		// #endregion
	}
	if !synced && b.RewindMovement && !b.VenityCompat {
		sendMovementPredictionSync(b, b.Conn.GameData().EntityUniqueID)
	}
}
