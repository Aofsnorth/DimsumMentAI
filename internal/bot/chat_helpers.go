package bot

import (
	"strings"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// TrackBotMessage records a message sent by the bot to prevent echo-loops
func (b *Bot) TrackBotMessage(text string) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.RecentBotMessages[strings.ToLower(strings.TrimSpace(text))] = time.Now()
}

// IsBotEcho checks if the text matches a recently sent bot message (within 5 seconds)
func (b *Bot) IsBotEcho(text string) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	clean := strings.ToLower(strings.TrimSpace(text))
	t, ok := b.RecentBotMessages[clean]
	if !ok {
		return false
	}

	if time.Since(t) < 5*time.Second {
		return true
	}

	// Clean up old entries
	for k, v := range b.RecentBotMessages {
		if time.Since(v) > 10*time.Second {
			delete(b.RecentBotMessages, k)
		}
	}
	return false
}

// SendSafeChat sends a message in chunks if it exceeds 250 characters.
func (b *Bot) SendSafeChat(msg string) {
	chunks := splitMessage(msg, 220)
	for _, chunk := range chunks {
		if chunk == "" {
			continue
		}
		// Track to avoid echo loops
		b.TrackBotMessage(chunk)

		b.Mu.Lock()
		botName := b.Name
		b.Mu.Unlock()

		pk := &packet.Text{
			TextType:         packet.TextTypeChat,
			SourceName:       botName,
			Message:          chunk,
			NeedsTranslation: false,
			XUID:             "",
			PlatformChatID:   "",
		}
		_ = b.Conn.WritePacket(pk)
		b.Logger.Info("sent chat message", "message", chunk)
		time.Sleep(300 * time.Millisecond) // brief delay to prevent packet flooding
	}
}

// splitMessage splits a long string into chunks at punctuation bounds or spaces
func splitMessage(msg string, maxLen int) []string {
	if len(msg) <= maxLen {
		return []string{msg}
	}

	var chunks []string
	runes := []rune(msg)
	for len(runes) > 0 {
		if len(runes) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}

		// Look for a punctuation split point in the last 40 runes of the window
		splitIdx := maxLen - 1
		found := false
		for i := maxLen - 1; i >= maxLen-40 && i > 0; i-- {
			r := runes[i]
			if r == '.' || r == '!' || r == '?' || r == ';' {
				splitIdx = i + 1
				found = true
				break
			}
		}

		if !found {
			// Look for space boundary
			for i := maxLen - 1; i >= maxLen-20 && i > 0; i-- {
				if runes[i] == ' ' {
					splitIdx = i
					found = true
					break
				}
			}
		}

		chunks = append(chunks, strings.TrimSpace(string(runes[:splitIdx])))
		runes = runes[splitIdx:]
	}

	return chunks
}
