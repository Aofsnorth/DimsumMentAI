package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/network/player"
	"bedrock-ai/internal/bot/network/world"
	"bedrock-ai/internal/event"
	"github.com/sandertv/gophertunnel/minecraft"
)

func PacketLoop(ctx context.Context, b *bot.Bot) error {
	for {
		select {
		case <-ctx.Done():
			b.Logger.Info("shutting down", slog.String("reason", ctx.Err().Error()))
			return nil
		default:
		}

		pk, err := b.Conn.ReadPacket()
		if err != nil {
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

		// Delegate packets to world or player handlers
		handled := world.HandleWorldPacket(b, pk)
		if !handled {
			player.HandlePlayerPacket(b, pk)
		}

		if handleErr := b.Registry.Handle(ctx, pk); handleErr != nil {
			b.Logger.Error("handle packet",
				slog.String("error", handleErr.Error()),
			)
		}
	}
}
