package ai

import (
	"regexp"
	"strings"
)

type Action struct {
	Label string
	Param string
}

type ParsedReply struct {
	CleanReply string
	Actions    []Action
}

var actionRegex = regexp.MustCompile(`(?s)<action>(.*?)</action>`)

// unclosedActionRegex matches an opening <action> tag (and any text after it
// that contains no further '<') that never received its closing </action>.
// Happens when the LLM gets truncated by MaxTokens mid-tag, leaving fragments
// like `<action>drop:crafting_table</` in the reply.
var unclosedActionRegex = regexp.MustCompile(`<action>[^<]*$`)

// thinkRegex matches reasoning blocks emitted by chain-of-thought models such
// as Minimax M2, DeepSeek-R1, etc. These must be stripped before the reply is
// shown in chat (or fed to action extraction) — otherwise the bot dumps its
// inner monologue to other players.
var thinkRegex = regexp.MustCompile(`(?s)<think>.*?</think>`)

// unclosedThinkRegex strips a leading <think>... that was cut by MaxTokens
// before the closing </think> could be emitted. Also strips a leading
// .*</think> when the model emitted only the closing tag (rare but happens).
var unclosedThinkRegex = regexp.MustCompile(`(?s)^\s*<think>.*$`)
var leadingThinkCloseRegex = regexp.MustCompile(`(?s)^.*?</think>\s*`)

// Parse extracts clean text and actions from the AI's reply.
func Parse(reply string) ParsedReply {
	var actions []Action

	// Strip chain-of-thought reasoning blocks first. Some models (Minimax M2,
	// DeepSeek-R1) prepend their internal reasoning between <think>...</think>
	// which would otherwise leak into chat or confuse the action extractor.
	reply = thinkRegex.ReplaceAllString(reply, "")
	// If the closing </think> survived but the opening tag was lost, drop
	// everything up to it. Conversely if the opening survived but closing was
	// truncated by MaxTokens, drop the whole tail.
	reply = leadingThinkCloseRegex.ReplaceAllString(reply, "")
	reply = unclosedThinkRegex.ReplaceAllString(reply, "")
	reply = strings.TrimSpace(reply)

	// Find all <action>...</action> matches
	matches := actionRegex.FindAllStringSubmatch(reply, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		rawAction := strings.TrimSpace(match[1])
		if rawAction == "" {
			continue
		}

		// Split into label and param by the first colon
		parts := strings.SplitN(rawAction, ":", 2)
		var act Action
		if len(parts) > 0 {
			act.Label = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			act.Param = strings.TrimSpace(parts[1])
		}
		actions = append(actions, act)
	}

	// Clean up the reply text by removing all <action>...</action> tags
	cleanReply := actionRegex.ReplaceAllString(reply, "")
	// Strip any trailing unclosed <action>... fragment (LLM token-truncation).
	cleanReply = unclosedActionRegex.ReplaceAllString(cleanReply, "")
	cleanReply = strings.TrimSpace(cleanReply)

	// Clean up any extra whitespace or newlines
	cleanReply = regexp.MustCompile(`\s+`).ReplaceAllString(cleanReply, " ")

	return ParsedReply{
		CleanReply: cleanReply,
		Actions:    actions,
	}
}
