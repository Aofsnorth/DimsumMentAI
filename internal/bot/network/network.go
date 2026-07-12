// Package network manages the bot's connection, packet dispatch, and chunk requests.
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
			return handleReadError(b, err, readStart, lastReadAt)
		}

		logReadGap(pk, lastReadAt, &packetsSinceLog)
		lastReadAt = time.Now()
		handleStart := lastReadAt

		handled := handlePacket(ctx, b, pk)
		handleMs := time.Since(handleStart).Milliseconds()
		if handleMs > 50 {
			debuglog.Log("B", "network.go:slow_handle", "slow packet handler", map[string]any{
				"handleMs":   handleMs,
				"packetId":   pk.ID(),
				"packetType": fmt.Sprintf("%T", pk),
			})
		}

		logUnhandledPacket(b, pk, handled)
	}
}

func handleReadError(b *bot.Bot, err error, readStart, lastReadAt time.Time) error {
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

func logReadGap(pk packet.Packet, lastReadAt time.Time, packetsSinceLog *int) {
	if lastReadAt.IsZero() {
		return
	}
	gap := time.Since(lastReadAt)
	*packetsSinceLog++
	if gap <= 200*time.Millisecond && *packetsSinceLog < 500 {
		return
	}
	debuglog.Log("B", "network.go:read_gap", "packet read gap", map[string]any{
		"gapMs":      gap.Milliseconds(),
		"packetId":   pk.ID(),
		"packetType": fmt.Sprintf("%T", pk),
		"sinceBatch": *packetsSinceLog,
	})
	*packetsSinceLog = 0
}

func handlePacket(ctx context.Context, b *bot.Bot, pk packet.Packet) bool {
	handled := world.HandleWorldPacket(b, pk)
	if !handled {
		handled = player.HandlePlayerPacket(b, pk)
	}

	if handleErr := b.Registry.Handle(ctx, pk); handleErr != nil {
		b.Logger.Error("handle packet",
			slog.String("error", handleErr.Error()),
		)
	}
	return handled
}

func logUnhandledPacket(b *bot.Bot, pk packet.Packet, handled bool) {
	if handled || isExpectedUnhandledPacket(pk) {
		return
	}
	b.Logger.Debug("unhandled packet", slog.Uint64("id", uint64(pk.ID())), slog.String("type", fmt.Sprintf("%T", pk)))
}

func isExpectedUnhandledPacket(pk packet.Packet) bool {
	switch pk.ID() {
	case packet.IDUpdateBlock, packet.IDMoveActorDelta, packet.IDSetActorData, packet.IDSetTime:
		return true
	}
	return false
}
