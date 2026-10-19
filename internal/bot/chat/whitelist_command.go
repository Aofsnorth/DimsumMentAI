package chat

import (
	"log/slog"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
)

// handleWhitelistCommand parses the in-game access grant, "Luna, whitelist
// nama \"Budi\"", and its revocation "unwhitelist", spoken in chat.
//
// It runs after shouldProcessChat — which is exactly the gate this command
// widens — so a player the gate drops can never reach it. The one way a
// non-main player's request gets here is a config without
// RespondOnlyToLinkedPlayer; that case is refused here, because the command
// belongs to linked players no matter how open the chat gate is.
//
// The grant itself is unconditional code. The sentence about it goes through
// replyViaAI like every other state change: the state change belongs to the
// bot, the words about it belong to the model.
func handleWhitelistCommand(b *bot.Bot, evt event.ChatEvent, msg string) bool {
	name, remove, ok := parseWhitelistIntent(msg)
	if !ok {
		return false
	}
	if !b.AiCfg.IsMainPlayer(evt.SourceName) {
		b.Logger.Info("chat ignored: whitelist command from non-main player",
			slog.String("source", evt.SourceName),
			slog.String("target", name),
			slog.Bool("remove", remove),
		)
		return true
	}

	var change string
	if remove {
		if b.UnwhitelistPlayer(name) {
			change = "You removed " + name + " from the player whitelist; they can no longer get your attention by tagging you."
		} else {
			change = name + " was not on the player whitelist, so nothing changed."
		}
	} else {
		if b.WhitelistPlayer(name) {
			change = "You added " + name + " to the player whitelist; they can now tag you and get answers."
		} else {
			change = name + " is already on the player whitelist, so nothing changed."
		}
	}
	b.Logger.Info("chat: whitelist command",
		slog.String("from", evt.SourceName),
		slog.String("target", name),
		slog.Bool("remove", remove),
	)
	replyViaAI(b, evt.SourceName, msg, change)
	return true
}

// parseWhitelistIntent extracts the player name from a whitelist request.
//
// Accepted forms, case-insensitive:
//
//	whitelist nama "Budi"       unwhitelist nama "Budi"
//	whitelist nama Budi         unwhitelist nama Budi
//	whitelist player Budi       whitelist "Budi Susanto"
//
// The keyword must be followed by something name-shaped. That is the
// false-positive guard: "kamu nggak di whitelist" contains the word but
// nothing follows it, and such a line belongs to the conversation, not to a
// config change. A quoted name may contain spaces; a bare name may not —
// "whitelist nama Budi susanto" grants "Budi", and a spaced gamertag has to
// be quoted.
func parseWhitelistIntent(msg string) (name string, remove bool, ok bool) {
	lower := strings.ToLower(msg)
	keyword := "whitelist"
	// "unwhitelist" and "un-whitelist" both mean remove, but the hyphenated
	// spelling has to be matched as written: searching the lowered message for
	// the bare keyword finds nothing after "un-", so the request silently fell
	// through to the AI as conversation instead of changing the whitelist.
	if idx := strings.Index(lower, "unwhitelist"); idx >= 0 {
		remove = true
		keyword = "unwhitelist"
	} else if idx := strings.Index(lower, "un-whitelist"); idx >= 0 {
		remove = true
		keyword = "un-whitelist"
	}
	idx := strings.Index(lower, keyword)
	if idx < 0 {
		return "", false, false
	}
	rest := strings.TrimSpace(msg[idx+len(keyword):])
	// Skip a direction word the player put between the keyword and the name.
	restLower := strings.ToLower(rest)
	for _, word := range []string{"nama", "player", "pemain", "nick"} {
		if strings.HasPrefix(restLower, word+" ") {
			rest = strings.TrimSpace(rest[len(word)+1:])
			break
		}
	}
	if len(rest) >= 2 && (rest[0] == '"' || rest[0] == '\'') {
		quote := rest[0]
		if end := strings.IndexByte(rest[1:], quote); end >= 0 {
			rest = rest[1 : 1+end]
		}
	} else {
		if i := strings.IndexAny(rest, " \t\n"); i >= 0 {
			rest = rest[:i]
		}
		// A bare name that is really an Indonesian particle — "whitelist
		// dong", "whitelist ya" — is a request with the target left out, not
		// a grant of the player "Dong". Quoted names skip this check: quotes
		// are the explicit form.
		switch strings.ToLower(strings.TrimSpace(rest)) {
		case "dong", "donk", "dunk", "ya", "aja", "saja", "deh", "yah",
			"pls", "plis", "cuy", "bang", "kak":
			return "", false, false
		}
	}
	name = strings.Trim(rest, "\"'“”‘’")
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 32 || strings.ContainsAny(name, "\n\"") {
		return "", false, false
	}
	return name, remove, true
}
