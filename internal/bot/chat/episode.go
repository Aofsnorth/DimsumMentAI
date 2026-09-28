// Recording briefs, and the human standing next to a bot that is filming itself.
//
// Two jobs live here. One is starting an episode from a chat line, which is how
// a recording actually begins. The other is telling the brain that a person is
// present, which is not the same thing and is easy to leave out: a bot working
// through a twenty-four minute brief does not know anyone is standing there,
// and will cheerfully keep digging while somebody is talking to it.
//
// Neither of these is a command in the usual sense. "episode" is not something
// a stranger should be able to say to a bot on a public server, so both paths
// are gated on who is talking.

package chat

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
)

// handleEpisodeCommand deals with a recording brief, and reports whether the
// message was one.
//
// The "stop" form exists because a brief that has run its course still needs
// an ending that somebody can see. Without it, the only way to end a recording
// is to wait out the clock, and a bot left on an empty brief keeps playing
// through the whole three hours of whatever comes next.
func handleEpisodeCommand(b *bot.Bot, evt event.ChatEvent, msg string) bool {
	head := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(msg), "/"))
	parts := strings.Fields(head)
	if len(parts) == 0 {
		return false
	}
	if parts[0] != "episode" && parts[0] != "ep" {
		return false
	}

	// A brief is an instruction to a recording, and a recording is somebody's
	// content. Only the person the bot is configured to answer to gets to start
	// one, or a stranger on a public server could hand the bot a task.
	if !isOperator(b, evt.SourceName) {
		b.Logger.Warn("episode command ignored: not the configured operator",
			slog.String("from", evt.SourceName))
		return true
	}

	if b.BeginEpisodeFunc == nil {
		b.Logger.Warn("episode command ignored: the autonomy brain is not running")
		return true
	}

	// "episode stop" ends the current brief early.
	if len(parts) >= 2 {
		switch parts[1] {
		case "stop", "end", "cancel":
			if b.EndEpisodeFunc != nil {
				b.EndEpisodeFunc("operator stopped it")
			}
			b.SendSafeChat("Oke, rekaman berhenti.")
			return true
		}
	}

	number, budget, objective, ok := b.BeginEpisodeFunc(msg, time.Now())
	if !ok {
		b.SendSafeChat("Formatnya: /episode <nomor> <durasi> <tujuan>, " +
			"misal /episode 1 24m build a house")
		return true
	}

	// Confirming out loud is not politeness. Whoever is recording needs to know
	// the bot understood the brief, and a bot that silently starts doing
	// something else is indistinguishable from one that ignored them.
	b.Logger.Info("episode started from chat",
		slog.String("from", evt.SourceName),
		slog.Int("episode", number),
		slog.Duration("budget", budget),
		slog.String("objective", objective))
	b.SendSafeChat(fmt.Sprintf("Oke. Episode %d dimulai — %s. Punya %s.", number, objective, speakDuration(budget)))
	return true
}

// noteHumanPresence tells the brain that somebody is talking to the bot.
//
// The clock keeps running while this is true; only the work stops. A recording
// is still being made while somebody chats to the bot, and pretending otherwise
// would mean the episode quietly gained minutes every time the host said hello.
func noteHumanPresence(b *bot.Bot, who string) {
	if b.SuspendFunc == nil {
		return
	}
	b.SuspendFunc(who)
}

// isOperator reports whether a player is the one the bot answers to.
func isOperator(b *bot.Bot, name string) bool {
	b.Mu.Lock()
	main := b.AiCfg.MainPlayer
	b.Mu.Unlock()
	return main != "" && strings.EqualFold(main, name)
}

// speakDuration renders a budget for a chat message, the way a person would say
// it rather than the way a clock would.
func speakDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "waktu yang nggak jelas"
	case d < time.Minute:
		return fmt.Sprintf("%d detik", int(d.Seconds()))
	case d < time.Hour:
		mins := int(d.Minutes())
		if secs := int(d.Seconds()) - mins*60; secs >= 10 {
			return fmt.Sprintf("%d menit %d detik", mins, secs)
		}
		return fmt.Sprintf("%d menit", mins)
	default:
		return fmt.Sprintf("%d jam %d menit", int(d.Hours()), int(d.Minutes())%60)
	}
}
