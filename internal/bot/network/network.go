// Package network manages the bot's connection, packet dispatch, and chunk requests.
package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
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
			fmt.Fprintln(os.Stderr, "DEBUG: PacketLoop saw ctx.Done(), err=", ctx.Err())
			b.Logger.Info("shutting down", slog.String("reason", ctx.Err().Error()))
			return nil
		default:
		}

		readStart := time.Now()
		pk, err := b.Conn.ReadPacket()
		if err != nil {
			fmt.Fprintln(os.Stderr, "DEBUG: PacketLoop ReadPacket err=", err)
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

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// A read that ends in context.Canceled with a long silence behind it is
		// NOT a server kick — a kick arrives as a DisconnectError carrying a
		// reason. This is the UDP path going quiet until RakNet gives up: the
		// server or an intermediary proxy stopped answering. Telling the two
		// apart matters, because only a kick is something the bot can act on.
		if gapMs > 3000 {
			b.Logger.Warn("connection lost after a long silence (UDP timeout, not a server kick)",
				slog.String("reason", err.Error()),
				slog.Int64("silent_for_ms", gapMs),
				slog.String("hypothesis", "server or proxy stopped responding; the bot was not rejected with a reason"),
			)
		} else {
			b.Logger.Info("connection closed during shutdown",
				slog.String("reason", err.Error()),
			)
		}
		return nil
	}

	var disc minecraft.DisconnectError
	if errors.As(err, &disc) {
		reason := strings.TrimSpace(disc.Error())

		// An empty reason is the host walking away without a goodbye: a
		// single-player world closing, or the host leaving. Logging that as a
		// plain "disconnected by server" with reason="" left the run looking like
		// a clean shutdown, so the bot died silently mid-walk.
		if reason == "" {
			reason = "host closed the world (no disconnect reason sent)"
		}
		b.Logger.Warn("disconnected by server",
			slog.String("reason", reason),
		)
		// Same event the kick path publishes, so every consumer of disconnects
		// sees this one too. The previous SpawnEvent here had no subscribers at
		// all and said the opposite of what happened.
		b.Bus.Publish(event.DisconnectEvent{Reason: reason})
		return &bot.ServerDisconnect{Reason: disc.Error()}
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
	// The SetTime packet is now handled (it drives the day/night behaviour in
	// the autonomy brain), so it is no longer expected-unhandled.
	switch pk.ID() {
	case packet.IDUpdateBlock, packet.IDMoveActorDelta, packet.IDSetActorData:
		return true
	}
	return false
}
