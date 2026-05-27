package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/network/player"
	"bedrock-ai/internal/bot/network/world"
	"bedrock-ai/internal/debuglog"
	"bedrock-ai/internal/event"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func PacketLoop(ctx context.Context, b *bot.Bot) error {
	var lastReadAt time.Time
	var packetsSinceLog int
	for {
		select {
		case <-ctx.Done():
			b.Logger.Info("shutting down", slog.String("reason", ctx.Err().Error()))
			return nil
		default:
		}

		readStart := time.Now()
		pk, err := b.Conn.ReadPacket()
		if err != nil {
			// #region agent log
			gapMs := int64(0)
			if !lastReadAt.IsZero() {
				gapMs = time.Since(lastReadAt).Milliseconds()
			}
			debuglog.Log("E", "network.go:ReadPacket_err", "read failed", map[string]any{
				"error":      err.Error(),
				"gapSinceMs": gapMs,
				"handleMs":   time.Since(readStart).Milliseconds(),
				"runId":      "post-fix",
			})
			// #endregion
			var disc minecraft.DisconnectError
			if errors.As(err, &disc) {
				b.Logger.Info("disconnected by server",
					slog.String("reason", disc.Error()),
				)
				b.Bus.Publish(event.SpawnEvent{})
				return nil
			}
			return fmt.Errorf("read packet: %w", err)
		}
		if !lastReadAt.IsZero() {
			gap := time.Since(lastReadAt)
			packetsSinceLog++
			if gap > 200*time.Millisecond || packetsSinceLog >= 500 {
				// #region agent log
				debuglog.Log("B", "network.go:read_gap", "packet read gap", map[string]any{
					"gapMs":      gap.Milliseconds(),
					"packetId":   pk.ID(),
					"packetType": fmt.Sprintf("%T", pk),
					"sinceBatch": packetsSinceLog,
				})
				// #endregion
				packetsSinceLog = 0
			}
		}
		lastReadAt = time.Now()
		handleStart := lastReadAt

		// Delegate packets to world or player handlers
		handled := world.HandleWorldPacket(b, pk)
		if !handled {
			handled = player.HandlePlayerPacket(b, pk)
		}

		if handleErr := b.Registry.Handle(ctx, pk); handleErr != nil {
			b.Logger.Error("handle packet",
				slog.String("error", handleErr.Error()),
			)
		}
		
		handleMs := time.Since(handleStart).Milliseconds()
		if handleMs > 50 {
			// #region agent log
			debuglog.Log("B", "network.go:slow_handle", "slow packet handler", map[string]any{
				"handleMs":   handleMs,
				"packetId":   pk.ID(),
				"packetType": fmt.Sprintf("%T", pk),
			})
			// #endregion
		}

		if !handled && pk.ID() != packet.IDUpdateBlock && pk.ID() != packet.IDMoveActorDelta && pk.ID() != packet.IDSetActorData && pk.ID() != packet.IDSetTime {
			b.Logger.Debug("unhandled packet", slog.Uint64("id", uint64(pk.ID())), slog.String("type", fmt.Sprintf("%T", pk)))
		}
	}
}
