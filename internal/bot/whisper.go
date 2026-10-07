// Replying to a whisper.
//
// The bot has to answer a private message privately. The reason is not
// politeness: a server relays a whisper to its other clients as a visible chat
// line, so a reply sent as ordinary chat puts the content of a private
// conversation in front of everybody on the server. A player who discovers that
// once stops using whispers entirely, and there is no way to undo that.
//
// The transport is the server's own whisper command rather than a protocol-level
// whisper packet, because the server is what relays whispers to Bedrock clients
// in this game mode: the inbound line arrives as an ordinary system Text packet
// with the participants written into the body, so the outbound path has to speak
// the same dialect.

package bot

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// whisperWindow is how long a whisper conversation stays open after the last
// message in it.
//
// Long enough for the follow-up exchanges a whisper conversation actually has —
// the model takes seconds to answer, and a player who whispers, waits, then
// whispers again has not started a new conversation. Short enough that a public
// message many minutes later is not swallowed into a DM that ended long ago.
const whisperWindow = 3 * time.Minute

// whisperMaxRunes is the per-message ceiling for a whispered reply.
//
// A command line is capped at 256 bytes by the protocol and the server counts a
// name plus the wrapper against that, so a whisper gets a much smaller budget
// than public chat's 220. Messages over it are split rather than dropped,
// because a reply that arrives cut off mid-sentence is worse than a short one.
const whisperMaxRunes = 120

// NoteWhisperFrom records that a private conversation with this player is open,
// so replies to them go back privately.
func (b *Bot) NoteWhisperFrom(player string) {
	if b == nil || strings.TrimSpace(player) == "" {
		return
	}
	b.Mu.Lock()
	b.whisperPartner = player
	b.whisperPartnerAt = time.Now()
	b.Mu.Unlock()
	if b.Logger != nil {
		b.Logger.Info("private conversation opened", slog.String("with", player))
	}
}

// ClearWhisperWith ends the private conversation, so the next reply is public
// again. Used when the player returns to public chat.
func (b *Bot) ClearWhisperWith(player string) {
	if b == nil {
		return
	}
	b.Mu.Lock()
	closed := b.whisperPartner != "" && strings.EqualFold(b.whisperPartner, player)
	if closed {
		b.whisperPartner = ""
		b.whisperPartnerAt = time.Time{}
	}
	b.Mu.Unlock()
	if closed && b.Logger != nil {
		b.Logger.Info("private conversation closed", slog.String("with", player))
	}
}

// WhisperPartner reports the player the bot is currently talking to privately,
// or "" when the last conversation was public.
func (b *Bot) WhisperPartner() string {
	if b == nil {
		return ""
	}
	b.Mu.Lock()
	partner, at := b.whisperPartner, b.whisperPartnerAt
	b.Mu.Unlock()
	if partner == "" || at.IsZero() || time.Since(at) > whisperWindow {
		return ""
	}
	return partner
}

// whisperMatches reports whether this line was addressed to the given player in
// the current private conversation.
func (b *Bot) whisperMatches(user string) bool {
	if user == "" {
		return false
	}
	return strings.EqualFold(b.WhisperPartner(), user)
}

// SendWhisper delivers a private message to one player.
//
// It goes through the command channel because that is how the server expects a
// whisper on this front-end. Each message is split to fit the tighter budget,
// and a failure on the wire is reported rather than swallowed: a dropped private
// reply is invisible to the sender, who concludes the bot ignored them.
func (b *Bot) SendWhisper(player, msg string) error {
	if b == nil {
		return errors.New("bot nil")
	}
	target := strings.TrimSpace(player)
	if target == "" {
		return errors.New("whisper tanpa penerima")
	}
	if b.Conn == nil {
		return errors.New("bot belum terhubung")
	}

	chunks := splitRunes(msg, whisperMaxRunes)
	if len(chunks) == 0 {
		return nil
	}
	for _, chunk := range chunks {
		line, err := NormalizeCommandLine(fmt.Sprintf("/tell %s %s", target, chunk))
		if err != nil {
			return fmt.Errorf("whisper ke %s: %w", target, err)
		}
		// Bypass the auto-detect retry: it waits for the server to answer, and a
		// whisper command is answered with silence because the reply goes to the
		// recipient, not to the sender. Waiting would cost a second per message
		// and then send the same line twice.
		if err := b.writeCommandLine(commandWireLine(line, b.commandPrefixStyle())); err != nil {
			return fmt.Errorf("whisper ke %s: %w", target, err)
		}
		if b.Logger != nil {
			b.Logger.Info("whisper sent", slog.String("to", target), slog.String("message", chunk))
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

// SendReply sends a chat message to one player, privately if that is how they
// are currently talking to the bot, publicly otherwise.
//
// This is the send path every reply should use. Calling SendSafeChat directly
// from a reply site is the bug this exists to prevent: it has no idea a whisper
// is open, so the answer lands in public.
func (b *Bot) SendReply(user, msg string) {
	if b == nil || strings.TrimSpace(msg) == "" {
		return
	}
	if b.whisperMatches(user) {
		if err := b.SendWhisper(user, msg); err != nil {
			if b.Logger != nil {
				b.Logger.Warn("whisper reply failed, falling back to public chat",
					slog.String("to", user), slog.String("error", err.Error()))
			}
			b.SendSafeChat(msg)
			return
		}
		return
	}
	b.SendSafeChat(msg)
}

// splitRunes slices a string to at most n runes, preferring a space boundary so
// a message is not cut mid-word.
func splitRunes(msg string, n int) []string {
	trimmed := strings.TrimSpace(msg)
	if trimmed == "" {
		return nil
	}
	runes := []rune(trimmed)
	if len(runes) <= n {
		return []string{trimmed}
	}
	chunks := make([]string, 0, len(runes)/n+1)
	for len(runes) > 0 {
		if len(runes) <= n {
			chunks = append(chunks, string(runes))
			break
		}
		cut := n
		for i := n; i > n/2; i-- {
			if runes[i] == ' ' {
				cut = i
				break
			}
		}
		chunks = append(chunks, strings.TrimSpace(string(runes[:cut])))
		runes = runes[cut:]
	}
	return chunks
}
