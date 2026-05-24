package handler

import (
	"context"
	"log/slog"

	"bedrock-ai/internal/event"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type ChatHandler struct {
	logger *slog.Logger
	bus    *event.Bus
}

func NewChatHandler(logger *slog.Logger, bus *event.Bus) *ChatHandler {
	return &ChatHandler{logger: logger, bus: bus}
}

func (h *ChatHandler) Handle(_ context.Context, pk packet.Packet) error {
	p, ok := pk.(*packet.Text)
	if !ok {
		return nil
	}

	h.logger.Info("chat",
		slog.String("source", p.SourceName),
		slog.String("message", p.Message),
	)

	h.bus.Publish(event.ChatEvent{
		Message:    p.Message,
		SourceName: p.SourceName,
		TextType:   p.TextType,
	})

	return nil
}
