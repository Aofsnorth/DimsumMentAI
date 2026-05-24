package handler

import (
	"context"
	"log/slog"
	"strings"

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

	sourceName := p.SourceName
	message := p.Message

	// Strip color codes for routing checks
	cleanSource := StripColorCodes(sourceName)
	cleanMessage := StripColorCodes(message)

	// Geyser compatibility fallback: extract name if sourceName is empty
	if cleanSource == "" && cleanMessage != "" {
		if strings.Contains(cleanMessage, ":") {
			parts := strings.SplitN(cleanMessage, ":", 2)
			cleanSource = strings.TrimSpace(parts[0])
			cleanMessage = strings.TrimSpace(parts[1])
		} else if strings.HasPrefix(cleanMessage, "<") && strings.Contains(cleanMessage, ">") {
			endIdx := strings.Index(cleanMessage, ">")
			cleanSource = strings.TrimSpace(cleanMessage[1:endIdx])
			cleanMessage = strings.TrimSpace(cleanMessage[endIdx+1:])
		}
	}

	h.logger.Info("chat packet received",
		slog.String("source", cleanSource),
		slog.String("message", cleanMessage),
		slog.Int("type", int(p.TextType)),
	)

	// Publish ChatEvent with cleaned names/messages
	h.bus.Publish(event.ChatEvent{
		Message:    cleanMessage,
		SourceName: cleanSource,
		TextType:   p.TextType,
	})

	return nil
}

// StripColorCodes removes Minecraft § formatting codes from a string
func StripColorCodes(s string) string {
	var res strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '§' {
			if i+1 < len(runes) {
				i++ // skip color character
				continue
			}
		}
		res.WriteRune(runes[i])
	}
	return res.String()
}
