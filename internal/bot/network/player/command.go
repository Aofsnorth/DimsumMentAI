package player

import (
	"log/slog"
	"strings"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// handleCommandOutput records what the server said in reply to a command.
//
// Most servers answer a command with an ordinary Text packet, which the chat
// handler already routes. The ones that use CommandOutput instead would
// otherwise be silent from the bot's point of view: it would have sent
// "/register …" and learned nothing about whether it worked.
func handleCommandOutput(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.CommandOutput)
	text := RenderCommandOutput(p.OutputMessages)
	if text == "" {
		return true
	}

	b.RecordCommandOutput(text)
	b.Logger.Info("server command output",
		slog.String("output", text),
		slog.Uint64("success_count", uint64(p.SuccessCount)),
	)
	return true
}

// RenderCommandOutput flattens a command reply into one readable line.
//
// OutputMessage.Message is either literal text or a translation key such as
// "commands.teleport.success" with the interesting values in Parameters. A key
// is not something a person or the LLM can read, so the parameters are appended
// and the key kept only as a label — dropping it would lose which command
// answered, and dropping the parameters would lose the answer.
func RenderCommandOutput(messages []protocol.CommandOutputMessage) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		text := strings.TrimSpace(msg.Message)
		if text == "" {
			continue
		}
		if len(msg.Parameters) > 0 {
			text = strings.TrimSpace(text + " " + strings.Join(msg.Parameters, " "))
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " | ")
}

// handleAvailableCommands records the command list the server advertises. It is
// the only ground truth for "can this server run /register", so it is logged at
// info once and kept for diagnostics.
func handleAvailableCommands(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.AvailableCommands)
	names := make([]string, 0, len(p.Commands))
	for _, c := range p.Commands {
		if c.Name != "" {
			names = append(names, c.Name)
		}
	}
	b.Logger.Info("server command list received", slog.Int("count", len(names)))
	b.Logger.Debug("available commands", slog.Any("names", names))
	return true
}
