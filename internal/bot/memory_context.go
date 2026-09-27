package bot

// MemoryContext renders the bot's curated long-term memories for LLM system
// prompts. It returns "" when no memory store is attached or the store is
// empty, so callers can append it unconditionally.
func (b *Bot) MemoryContext() string {
	if b == nil || b.Memory == nil {
		return ""
	}
	return b.Memory.RenderForPrompt(10)
}
