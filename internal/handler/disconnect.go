package handler

import (
	"context"
	"errors"
	"log/slog"

	"bedrock-ai/internal/event"

	"github.com/sandertv/gophertunnel/minecraft"
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

	var disc minecraft.DisconnectError
	if errors.As(errors.New(p.Message), &disc) {
		h.logger.Info("kicked from server",
			slog.String("reason", disc.Error()),
		)
		h.bus.Publish(event.DisconnectEvent{Reason: disc.Error()})
	} else {
		h.logger.Info("kicked from server",
			slog.String("reason", p.Message),
		)
		h.bus.Publish(event.DisconnectEvent{Reason: p.Message})
	}

	return nil
}
