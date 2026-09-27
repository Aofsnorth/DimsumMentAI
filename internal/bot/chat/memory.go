package chat

import (
	"bedrock-ai/internal/bot"
)

// appendMemoryContext appends the bot's curated long-term memories to a
// system prompt. It returns the prompt unchanged when no memory store is
// attached or the store is empty.
func appendMemoryContext(b *bot.Bot, systemPrompt string) string {
	if b == nil {
		return systemPrompt
	}
	if mem := b.MemoryContext(); mem != "" {
		systemPrompt += "\n\n" + mem
	}
	return systemPrompt
}
