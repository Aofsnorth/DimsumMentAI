package chat

import (
	"log/slog"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
)

// handleGuardCommand parses the in-game guardian toggle — "Luna kalo ada mob
// jahat, bunuh aja ya, jangan didiemin" and its shutdown "matiin guard" —
// spoken in chat.
//
// It runs before the AI matchers because it is a behaviour configuration, not a
// conversation: the reflex it changes lives inside the threat detector, which
// the model can describe but cannot reach. Like the whitelist command it is a
// main-player switch — a stranger must not be able to re-arm the bot — and like
// every state change the sentence about it goes through replyViaAI, with a
// factual English change sentence, because the words belong to the model.
func handleGuardCommand(b *bot.Bot, evt event.ChatEvent, msg string) bool {
	enable, ok := parseGuardIntent(msg)
	if !ok {
		return false
	}
	if !b.AiCfg.IsMainPlayer(evt.SourceName) {
		b.Logger.Info("chat ignored: guard command from non-main player",
			slog.String("source", evt.SourceName),
			slog.Bool("enable", enable),
		)
		return true
	}
	if b.ThreatDet == nil {
		b.Logger.Info("chat: guard command ignored, threat detector not configured",
			slog.String("from", evt.SourceName))
		return true
	}

	protector := evt.SourceName
	wasOn, _ := b.ThreatDet.GuardMode()
	var change string
	switch {
	case enable:
		b.ThreatDet.SetGuardMode(true, protector)
		if wasOn {
			change = "Guard mode was already on; hostile mobs that come near you are still attacked automatically."
		} else {
			change = "Guard mode is on: you will now attack hostile mobs that come near " + protector + " instead of just alerting about them."
		}
	case wasOn:
		b.ThreatDet.SetGuardMode(false, "")
		change = "Guard mode is off: nearby hostile mobs are only alerted about again, not automatically attacked."
	default:
		change = "Guard mode was already off, so nothing changed."
	}

	b.Logger.Info("chat: guard command",
		slog.String("from", evt.SourceName),
		slog.String("protector", protector),
		slog.Bool("enable", enable),
	)
	replyViaAI(b, evt.SourceName, msg, change)
	return true
}

// guardDisablePhrases are the shutdown forms, checked before any enable form.
// Every entry is a full bigram or trigram on purpose: the user's enable phrase
// contains "jangan didiemin", so the bare word "jangan" must never disable —
// only "jangan" next to a kill, spare or guard word does.
var guardDisablePhrases = []string{
	"jangan bunuh", "jangan serang", "jangan kill", "jangan attack",
	"nggak usah bunuh", "nggak usah serang", "nggak usah kill", "nggak usah attack",
	"gak usah bunuh", "gak usah serang", "gak usah kill", "gak usah attack",
	"ga usah bunuh", "ga usah serang", "ga usah kill", "ga usah attack",
	"nggak usah jaga", "gak usah jaga", "ga usah jaga",
	"nggak perlu jaga", "gak perlu jaga", "ga perlu jaga",
	"jangan jaga", "jangan lindungi", "jangan guard",
	"matiin guard", "matikan guard", "guard off", "stop guard",
	"berhenti guard", "berhenti jaga",
}

// guardMobWords name the enemies, guardKillWords the violence, and
// guardAutoWords the standing order. The enable phrase needs one of each: the
// triple is the false-positive guard that keeps "killing" chatter — an anecdote
// about a zombie, a command to attack a specific player — from silently
// re-arming the bot.
var guardMobWords = []string{
	"mob", "monster", "hostile", "zombie", "skeleton", "creeper", "husk",
	"drowned", "spider", "enderman", "witch", "slime", "phantom", "pillager",
	"vindicator", "blaze", "ghast", "warden", "silverfish", "piglin",
}
var guardKillWords = []string{"bunuh", "kill", "serang", "attack", "habisin", "basmi"}
var guardAutoWords = []string{
	"kalo", "kalau", "jika", "auto", "otomatis", "jaga", "lindungi",
	"guard", "diemin", "didiemin", "selalu",
}

// parseGuardIntent extracts the guardian toggle from a chat line.
//
//	enable:  "kalo ada mob jahat bunuh aja"   (mob + kill + standing order)
//	         "guard" / "guard mode"           (the word on its own)
//	disable: "matiin guard" / "jangan bunuh" / "guard off" / "berhenti jaga"
//
// Matching is plain substring on the lowercased line, like the whitelist
// parser: chat is short and sloppy, and the disable check runs first so an
// enable-shaped sentence with a shutdown inside it still shuts down.
func parseGuardIntent(msg string) (enable bool, ok bool) {
	lower := strings.ToLower(msg)

	if containsAnyWord(lower, guardDisablePhrases) {
		return false, true
	}
	// The bare word: "guard", "guard mode", "luna guard". Whole-word so an
	// aside about a "guardian" armor stand is not a re-arm.
	if containsGuardWord(lower) {
		return true, true
	}
	if containsAnyWord(lower, guardMobWords) &&
		containsAnyWord(lower, guardKillWords) &&
		containsAnyWord(lower, guardAutoWords) {
		return true, true
	}
	return false, false
}

func containsAnyWord(lower string, words []string) bool {
	for _, w := range words {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// containsGuardWord finds "guard" as a standalone word — bounded by non-letters
// on both sides — so "guardian" and "bodyguard" do not match.
func containsGuardWord(lower string) bool {
	for i := 0; ; {
		idx := strings.Index(lower[i:], "guard")
		if idx < 0 {
			return false
		}
		start, end := i+idx, i+idx+len("guard")
		if wordBoundaryAt(lower, start-1) && wordBoundaryAt(lower, end) {
			return true
		}
		i = end
	}
}

func wordBoundaryAt(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	c := s[i]
	return c < 'a' || c > 'z'
}
