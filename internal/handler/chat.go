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

// PlayerResolver resolves candidate player names or formatted strings to known player names.
type PlayerResolver interface {
	FindKnownPlayer(query string) (string, bool)
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
		var resolver func(string) (string, bool)
		if pr, ok := h.bot.(PlayerResolver); ok {
			resolver = pr.FindKnownPlayer
		}
		if sender, parsedMsg, ok := ExtractChatSenderAndMessage(cleanMessage, resolver); ok {
			cleanSource = sender
			cleanMessage = parsedMsg
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

// ExtractChatSenderAndMessage parses formatted chat lines (e.g. "MEMBER Synterix> msg",
// "<Synterix> msg", "[VIP] Synterix: msg") into sender username and message body.
// If resolver is non-nil, it is consulted to verify or resolve candidate player names.
func ExtractChatSenderAndMessage(cleanMessage string, resolver func(string) (string, bool)) (string, string, bool) {
	clean := strings.TrimSpace(StripColorCodes(cleanMessage))
	if clean == "" {
		return "", "", false
	}

	// Strip leading timestamp if present, e.g. "[12:34:56] " or "[12:34] "
	if strings.HasPrefix(clean, "[") {
		if endBracket := strings.Index(clean, "]"); endBracket > 1 && endBracket < 12 {
			inner := clean[1:endBracket]
			if isTimestamp(inner) {
				clean = strings.TrimSpace(clean[endBracket+1:])
			}
		}
	}

	// Case 1: Vanilla angle-bracket format: <Player> message or <[Rank] Player> message
	if strings.HasPrefix(clean, "<") {
		endIdx := strings.Index(clean, ">")
		if endIdx > 1 {
			left := strings.TrimSpace(clean[1:endIdx])
			right := strings.TrimSpace(clean[endIdx+1:])
			if right != "" {
				if resolver != nil {
					if canonical, ok := resolver(left); ok {
						return canonical, right, true
					}
				}
				candidate := extractCandidatePlayerName(left)
				if candidate != "" && !isSystemSender(candidate) && isValidMinecraftUsername(candidate) {
					return candidate, right, true
				}
			}
		}
	}

	// Case 2: Separator-based format: Rank Player> msg, [Rank] Player: msg, Player » msg
	sepIdx, sepLen := findChatSeparator(clean)
	if sepIdx <= 0 {
		return "", "", false
	}

	runes := []rune(clean)
	left := strings.TrimSpace(string(runes[:sepIdx]))
	right := strings.TrimSpace(string(runes[sepIdx+sepLen:]))
	if left == "" || right == "" {
		return "", "", false
	}

	// If resolver is available, check if the left side matches any known player
	if resolver != nil {
		if canonical, ok := resolver(left); ok {
			return canonical, right, true
		}
	}

	// Fallback heuristic: extract candidate username from the prefix
	candidate := extractCandidatePlayerName(left)
	if candidate == "" || isSystemSender(candidate) || !isValidMinecraftUsername(candidate) {
		return "", "", false
	}

	return candidate, right, true
}

func findChatSeparator(clean string) (sepIdx int, sepLen int) {
	runes := []rune(clean)
	bracketDepth := 0
	parenDepth := 0

	maxScan := len(runes)
	if maxScan > 60 {
		maxScan = 60
	}

	for i := 0; i < maxScan; i++ {
		r := runes[i]
		if r == '[' {
			bracketDepth++
			continue
		}
		if r == ']' {
			if bracketDepth > 0 {
				bracketDepth--
			}
			continue
		}
		if r == '(' {
			parenDepth++
			continue
		}
		if r == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			continue
		}

		if bracketDepth == 0 && parenDepth == 0 {
			if r == '>' || r == ':' || r == '»' || r == '▶' || r == '➤' {
				return i, 1
			}
			if r == '-' && i+1 < len(runes) && runes[i+1] == '>' {
				return i, 2
			}
		}
	}
	return -1, 0
}

func extractCandidatePlayerName(s string) string {
	s = strings.TrimSpace(s)
	// Strip bracketed prefixes, e.g. "[VIP]", "[Member]", "[Guild]"
	for strings.Contains(s, "[") && strings.Contains(s, "]") {
		start := strings.Index(s, "[")
		end := strings.Index(s, "]")
		if start < end {
			s = strings.TrimSpace(s[:start] + s[end+1:])
		} else {
			break
		}
	}
	// Strip parentheses, e.g. "(VIP)"
	for strings.Contains(s, "(") && strings.Contains(s, ")") {
		start := strings.Index(s, "(")
		end := strings.Index(s, ")")
		if start < end {
			s = strings.TrimSpace(s[:start] + s[end+1:])
		} else {
			break
		}
	}
	// Remove common decorative symbols like |, », >, #
	s = strings.Trim(s, " |»>#*")

	words := strings.Fields(s)
	if len(words) == 0 || len(words) > 3 {
		return ""
	}
	// The username is normally the last token in the prefix block
	lastWord := words[len(words)-1]
	lastWord = strings.Trim(lastWord, " |»>#*:[]()\"'")
	return lastWord
}

func isSystemSender(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "daily", "server", "system", "broadcast", "notice", "alert",
		"announcement", "info", "tip", "discord", "warning", "error",
		"console", "admin", "welcome", "lobby", "hub", "proxy", "network", "bot":
		return true
	default:
		return false
	}
}

func isValidMinecraftUsername(name string) bool {
	if len(name) < 3 || len(name) > 17 {
		return false
	}
	clean := strings.TrimPrefix(strings.TrimPrefix(name, "."), "*")
	if len(clean) < 2 || len(clean) > 16 {
		return false
	}
	for _, r := range clean {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func isTimestamp(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 2 {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
