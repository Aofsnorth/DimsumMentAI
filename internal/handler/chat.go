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
	if !isRoutableChatText(p.TextType) {
		h.logger.Debug("ignored non-chat text packet", slog.Int("type", int(p.TextType)))
		return nil
	}

	sourceName := p.SourceName
	message := p.Message

	cleanSource := StripColorCodes(sourceName)
	cleanMessage := StripColorCodes(message)

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
		slog.Int("text_type", int(p.TextType)),
		slog.String("raw_source", sourceName),
	)

	if cleanMessage == "" {
		h.logger.Info("chat ignored: empty message after parse")
		return nil
	}

	h.bus.Publish(event.ChatEvent{
		Message:    cleanMessage,
		SourceName: cleanSource,
		TextType:   p.TextType,
	})

	return nil
}

func isRoutableChatText(textType byte) bool {
	switch textType {
	case packet.TextTypeChat,
		packet.TextTypeWhisper,
		packet.TextTypeAnnouncement,
		packet.TextTypeRaw,
		packet.TextTypeSystem:
		return true
	default:
		return false
	}
}

// StripColorCodes removes Minecraft § formatting codes from a string
func StripColorCodes(s string) string {
	var res strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '§' {
			if i+1 < len(runes) {
				i++
				continue
			}
		}
		res.WriteRune(runes[i])
	}
	return res.String()
}
