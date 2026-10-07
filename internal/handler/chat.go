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

// BotNamer reports whether a name refers to this bot. A relayed whisper names
// its recipient, and only the bot knows its own current in-game name.
type BotNamer interface {
	IsBotName(name string) bool
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

	var resolver func(string) (string, bool)
	if pr, ok := h.bot.(PlayerResolver); ok {
		resolver = pr.FindKnownPlayer
	}

	// A whisper reaches a Bedrock client as a plain system message with both
	// participants written into the body and no source name, so it is checked
	// before the generic sender extraction: that one would read the arrow in
	// "Synterix ➺ .ShigaLucifer" as a chat separator and attribute the message to
	// a player called "✉⬇ MSG (Synterix".
	whisper := false
	if sender, recipient, body, ok := ExtractWhisper(cleanMessage, resolver); ok && h.isBotRecipient(recipient) {
		cleanSource = sender
		cleanMessage = body
		whisper = true
	} else if cleanSource == "" && cleanMessage != "" {
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
		Whisper:    whisper,
	})

	return nil
}

// isBotRecipient reports whether a relayed whisper was addressed to this bot.
//
// The banner names both participants, and the same banner is used for a whisper
// between two other players. Answering one of those would put the bot into a
// conversation it was never part of, so the recipient has to be checked.
func (h *ChatHandler) isBotRecipient(name string) bool {
	if name == "" {
		return false
	}
	if nr, ok := h.bot.(BotNamer); ok {
		return nr.IsBotName(name)
	}
	// Without a name source there is nothing to compare against, so a relayed
	// whisper is left alone rather than guessed at.
	return false
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

// whisperArrowRunes are the arrow characters servers use between the two
// participants of a relayed whisper.
const whisperArrowRunes = "➺➤➔►▶»›->→"

// ExtractWhisper recognises the form a server sends a private message to a
// Bedrock client in: an icon, a label, then "(sender ➺ recipient) body" — for
// example "✉⬇ MSG (Synterix ➺ .ShigaLucifer) halo".
//
// The whisper arrives as an ordinary system message with an empty source name,
// so without this the line is dropped: there is no sender to match against the
// linked-player list, and the log shows the whole banner as ignored chat.
//
// It reports the sender and the body. Whether the recipient is actually this bot
// is not decided here — the caller knows the bot's own name, and this function
// deliberately does not assume one, because a whisper between two other players
// also relays through the same banner and must not be answered as if it were
// addressed to the bot.
func ExtractWhisper(cleanMessage string, resolver func(string) (string, bool)) (sender, recipient, body string, ok bool) {
	clean := strings.TrimSpace(StripColorCodes(cleanMessage))
	if clean == "" {
		return "", "", "", false
	}

	open := strings.Index(clean, "(")
	if open < 0 || open > 24 {
		// The banner is a fixed shape; a "(" far into the line is ordinary prose.
		return "", "", "", false
	}
	shut := strings.Index(clean[open:], ")")
	if shut < 0 {
		return "", "", "", false
	}
	shut += open

	left, right, found := splitWhisperArrow(clean[open+1 : shut])
	if !found {
		return "", "", "", false
	}

	// The label before the parenthesis has to look like a message banner, and a
	// space has to separate the body from the closing parenthesis. Together they
	// keep "Halo (Synterix ➺ Papua)Apa kabar?" from being read as a whisper: the
	// shape matches, but there is no label and no gap before the body.
	if !looksLikeWhisperBanner(clean[:open]) {
		return "", "", "", false
	}
	if !strings.HasPrefix(clean[shut+1:], " ") {
		return "", "", "", false
	}

	candidate := strings.Trim(strings.TrimSpace(left), " |»>#*")
	if candidate == "" {
		return "", "", "", false
	}

	canonical := candidate
	if resolved, isKnown := callResolver(resolver, candidate); isKnown {
		canonical = resolved
	} else if !isValidMinecraftUsername(candidate) || isSystemSender(candidate) {
		return "", "", "", false
	}

	to := strings.Trim(strings.TrimSpace(right), " |»>#*")
	body = strings.TrimSpace(clean[shut+1:])
	if to == "" || body == "" {
		return "", "", "", false
	}
	return canonical, to, body, true
}

func callResolver(resolver func(string) (string, bool), name string) (string, bool) {
	if resolver == nil {
		return "", false
	}
	return resolver(name)
}

// splitWhisperArrow divides "sender ➺ recipient" on its arrow. "->" is two
// runes and is consumed as one, so neither half is left looking like a name.
func splitWhisperArrow(inside string) (left, right string, ok bool) {
	runes := []rune(inside)
	for i, r := range runes {
		if !strings.ContainsRune(whisperArrowRunes, r) {
			continue
		}
		l := strings.TrimSpace(string(runes[:i]))
		rest := i + 1
		if r == '-' && rest < len(runes) && runes[rest] == '>' {
			rest++
		}
		rr := strings.TrimSpace(string(runes[rest:]))
		if l == "" || rr == "" {
			return "", "", false
		}
		return l, rr, true
	}
	return "", "", false
}

// whisperBannerRunes is the icon-and-label shape seen in front of the
// parenthesis on a relayed whisper ("✉⬇ MSG ", "✉ MSG", "MSG ").
func looksLikeWhisperBanner(prefix string) bool {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" {
		return false
	}
	fields := strings.Fields(trimmed)
	last := strings.ToUpper(strings.TrimSpace(fields[len(fields)-1]))
	switch last {
	case "MSG", "MESSAGE", "DM", "PRIVATE", "WHISPER", "TELL", "CHAT", "W":
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
