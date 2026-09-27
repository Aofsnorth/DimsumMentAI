package ai

import (
	"strings"
	"unicode/utf8"
)

// contextBudgetRatio is the fraction of a model's context window the bot is
// allowed to use for a single request. Kept at 25% so requests stay small and
// fast and never approach the model's hard limit.
const contextBudgetRatio = 0.25

// defaultContextWindow is the assumed context window (tokens) for models not
// present in modelContextWindows. Conservative on purpose: overestimating the
// budget risks overflowing the real window, underestimating only trims earlier
// history.
const defaultContextWindow = 32780

// modelContextWindows maps lowercase model-name substrings to their context
// window size in tokens. Ordered most-specific first so substring matching
// picks the tightest entry.
var modelContextWindows = []struct {
	substr string
	window int
}{
	{"gemini-1.5-pro", 2097152},
	{"gemini-2.5", 1048576},
	{"gemini-2.0", 1048576},
	{"gemini-1.5", 1048576},
	{"gemini", 1048576},
	{"minimax-m1", 1048576},
	{"minimax", 1048576},
	{"claude-3", 200000},
	{"claude", 200000},
	{"gpt-oss-120b", 131072},
	{"gpt-oss-20b", 131072},
	{"gpt-oss", 131072},
	{"gpt-4o", 128000},
	{"gpt-4-turbo", 128000},
	{"gpt-4", 8192},
	{"gpt-3.5", 16385},
	{"nemotron-super-49b", 131072},
	{"llama-3.3", 128000},
	{"llama-3.1", 128000},
	{"llama-3", 8192},
	{"deepseek", 131072},
	{"mistral-large", 128000},
	{"mixtral", 32768},
}

// lookupContextWindow returns the context window size in tokens for a model
// name, falling back to defaultContextWindow when the model is unknown.
func lookupContextWindow(model string) int {
	m := strings.ToLower(model)
	for _, entry := range modelContextWindows {
		if strings.Contains(m, entry.substr) {
			return entry.window
		}
	}
	return defaultContextWindow
}

// estimateTokens approximates the token count of a string. ~4 characters per
// token is a language-agnostic heuristic; the 25% budget margin absorbs the
// estimation error.
func estimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return n/4 + 1
}

// estimateMessagesTokens sums the estimated tokens of a message list, adding a
// small per-message overhead for role/structure framing.
func estimateMessagesTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateTokens(m.Content) + 4
	}
	return total
}

// SetContextWindow overrides the auto-detected model context window (tokens).
// Pass 0 (the default) to use the built-in per-model registry.
func (nc *NvidiaClient) SetContextWindow(tokens int) {
	nc.contextWindowOverride = tokens
}

// ContextWindow returns the effective model context window in tokens.
func (nc *NvidiaClient) ContextWindow() int {
	if nc.contextWindowOverride > 0 {
		return nc.contextWindowOverride
	}
	return lookupContextWindow(nc.model)
}

// ContextBudget returns the maximum number of tokens the bot may send in a
// single request: 25% of the effective context window.
func (nc *NvidiaClient) ContextBudget() int {
	return int(float64(nc.ContextWindow()) * contextBudgetRatio)
}

// compactToBudget trims the message list so its estimated token count fits the
// budget. Leading system messages and the trailing (newest) message are always
// kept; the oldest history messages are dropped first.
//
// ponytail: this is trim-to-fit compaction — it drops old turns outright.
// Ceiling: lossy for long conversations. Upgrade path: call the LLM once to
// summarize the dropped turns into a single condensed system note when finer
// retention matters.
func (nc *NvidiaClient) compactToBudget(messages []Message, budget int) []Message {
	if budget <= 0 || estimateMessagesTokens(messages) <= budget {
		return messages
	}

	// Peel leading system messages and the mandatory trailing message.
	sysEnd := 0
	for sysEnd < len(messages) && messages[sysEnd].Role == "system" {
		sysEnd++
	}
	system := messages[:sysEnd]
	rest := messages[sysEnd:]

	var last Message
	hasLast := len(rest) > 0
	if hasLast {
		last = rest[len(rest)-1]
		rest = rest[:len(rest)-1]
	}

	lastTokens := estimateMessagesTokens([]Message{last})
	used := estimateMessagesTokens(system) + lastTokens

	// If the fixed parts alone exceed the budget, truncate the system prompt.
	if used > budget && len(system) > 0 {
		system = truncateMessages(system, budget-lastTokens)
		used = estimateMessagesTokens(system) + lastTokens
	}

	// Keep as many recent history messages as fit, scanning newest-first.
	kept := make([]Message, 0, len(rest))
	for i := len(rest) - 1; i >= 0; i-- {
		cost := estimateMessagesTokens([]Message{rest[i]})
		if used+cost > budget {
			break
		}
		kept = append([]Message{rest[i]}, kept...)
		used += cost
	}

	out := make([]Message, 0, len(system)+len(kept)+1)
	out = append(out, system...)
	out = append(out, kept...)
	if hasLast {
		out = append(out, last)
	}
	return out
}

// truncateMessages shortens a message list so its total estimated tokens fit
// allow, cutting the last included message's content if needed.
func truncateMessages(msgs []Message, allow int) []Message {
	if allow <= 0 {
		return nil
	}
	out := make([]Message, 0, len(msgs))
	used := 0
	for _, m := range msgs {
		cost := estimateTokens(m.Content) + 4
		if used+cost > allow {
			roomChars := (allow - used - 4) * 4
			if roomChars > 0 && roomChars < len(m.Content) {
				m.Content = m.Content[:roomChars] + "...[truncated]"
				out = append(out, m)
			}
			break
		}
		out = append(out, m)
		used += cost
	}
	return out
}
