package handler

import (
	"context"
	"log/slog"

	"bedrock-ai/internal/debuglog"
	"bedrock-ai/internal/event"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type DisconnectHandler struct {
	logger *slog.Logger
	bus    *event.Bus
}

func NewDisconnectHandler(logger *slog.Logger, bus *event.Bus) *DisconnectHandler {
	return &DisconnectHandler{logger: logger, bus: bus}
}

func (h *DisconnectHandler) Handle(_ context.Context, pk packet.Packet) error {
	p, ok := pk.(*packet.Disconnect)
	if !ok {
		return nil
	}

	// #region agent log
	debuglog.Log("D", "disconnect.go:Handle", "server disconnect packet", map[string]any{
		"message": p.Message,
	})
	// #endregion

	// The message is already the reason string; wrapping it in a fresh error
	// and asking errors.As for a DisconnectError could never match, so the
	// branch below used to be dead code that looked like a kick special case.
	h.logger.Info("kicked from server",
		slog.String("reason", p.Message),
	)
	h.bus.Publish(event.DisconnectEvent{Reason: p.Message})

	return nil
}
