package handler

import (
	"context"
	"log/slog"
	"strings"

	"bedrock-ai/internal/event"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// ServerReplier is the slice of *bot.Bot this package needs. Declared as an
// interface so internal/handler does not import internal/bot, which would be an
// import cycle: bot already imports handler.
type ServerReplier interface {
	NoteServerReply(text string)
}

type ChatHandler struct {
	logger *slog.Logger
	bus    *event.Bus
	bot    ServerReplier
}

func NewChatHandler(logger *slog.Logger, bus *event.Bus, replier ...ServerReplier) *ChatHandler {
	h := &ChatHandler{logger: logger, bus: bus}
	if len(replier) > 0 {
		h.bot = replier[0]
	}
	return h
}

// SetServerReplier attaches the bot after construction. The registry is wired up
// before the bot exists, so this is how the handler gets told where to report
// command replies.
func (h *ChatHandler) SetServerReplier(replier ServerReplier) {
	h.bot = replier
}

func (h *ChatHandler) Handle(_ context.Context, pk packet.Packet) error {
	p, ok := pk.(*packet.Text)
	if !ok {
		return nil
	}
	// A bot is needed to record the reply, so the handler carries an optional
	// pointer rather than importing internal/bot (which would be a cycle).
	if h.bot != nil {
		h.bot.NoteServerReply(p.Message)
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
