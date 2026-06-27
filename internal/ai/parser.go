package ai

import (
	"fmt"
	"regexp"
	"strings"
)

type Action struct {
	Label string
	Param string
}

type ParsedReply struct {
	CleanReply  string
	Actions     []Action
	PlanSteps   []string // raw "label:param" strings from a <plan> block, if present
	FollowupSec int      // delay in seconds before a follow-up message, from <followup>N</followup>
}

var actionRegex = regexp.MustCompile(`(?s)<action>(.*?)</action>`)

// unclosedActionRegex matches an opening <action> tag (and any text after it
// that contains no further '<') that never received its closing </action>.
// Happens when the LLM gets truncated by MaxTokens mid-tag, leaving fragments
// like `<action>drop:crafting_table</` in the reply.
var unclosedActionRegex = regexp.MustCompile(`<action>[^<]*$`)

// planRegex matches a <plan>...</plan> block containing <step> tags.
var planRegex = regexp.MustCompile(`(?s)<plan>(.*?)</plan>`)
var planStepRegex = regexp.MustCompile(`(?s)<step>(.*?)</step>`)

// replanRegex matches a <replan>...</replan> block (same inner format as
// <plan>). Used by the planner's agentic loop when the LLM adjusts remaining
// steps mid-execution.
var replanRegex = regexp.MustCompile(`(?s)<replan>(.*?)</replan>`)

// followupRegex matches a <followup>N</followup> tag where N is the delay in
// seconds before the bot should send a follow-up message. This enables the
// LLM to split a response into an immediate acknowledgment and a delayed
// follow-up (e.g. "Oke aku cek dulu" → 2s → "Aku punya kayu 4, batu 12...").
var followupRegex = regexp.MustCompile(`<followup>\s*(\d+)\s*</followup>`)

// thinkRegex matches reasoning blocks emitted by chain-of-thought models such
// as Minimax M2, DeepSeek-R1, etc. These must be stripped before the reply is
// shown in chat (or fed to action extraction) — otherwise the bot dumps its
// inner monologue to other players.
var thinkRegex = regexp.MustCompile("(?s)<think>.*?</think>")

// unclosedThinkRegex strips a leading <think>... that was cut by MaxTokens
// before the closing </think> could be emitted. Also strips a leading
// </think> when the model emitted only the closing tag (rare but happens).
var unclosedThinkRegex = regexp.MustCompile(`(?s)^\s*<think>.*$`)
var leadingThinkCloseRegex = regexp.MustCompile(`(?s)^.*?</think>\s*`)

// Parse extracts clean text, actions, and plan steps from the AI's reply.
func Parse(reply string) ParsedReply {
	var actions []Action
	var planSteps []string

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

	// Extract <plan> steps BEFORE stripping tags, so we can detect a plan
	// even when the LLM also emits <action> tags.
	planMatch := planRegex.FindStringSubmatch(reply)
	if len(planMatch) >= 2 {
		planSteps = extractSteps(planMatch[1])
	}

	// Extract <followup>N</followup> delay (0 = no followup).
	followupSec := 0
	if fm := followupRegex.FindStringSubmatch(reply); len(fm) >= 2 {
		fmt.Sscanf(fm[1], "%d", &followupSec)
	}

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
	// Remove <plan>...</plan> blocks from the visible reply — the planner
	// handles them; the player shouldn't see raw step XML.
	cleanReply = planRegex.ReplaceAllString(cleanReply, "")
	// Remove <followup>...</followup> tags from visible reply.
	cleanReply = followupRegex.ReplaceAllString(cleanReply, "")
	cleanReply = strings.TrimSpace(cleanReply)

	// Clean up any extra whitespace or newlines
	cleanReply = regexp.MustCompile(`\s+`).ReplaceAllString(cleanReply, " ")

	return ParsedReply{
		CleanReply:  cleanReply,
		Actions:     actions,
		PlanSteps:   planSteps,
		FollowupSec: followupSec,
	}
}

// ParsePlanSteps extracts <step> contents from either a <plan> or <replan>
// block in the reply. Returns nil if no plan/replan block is present.
// Used by the planner to detect replan decisions during the agentic loop.
func ParsePlanSteps(reply string) []string {
	// Try <replan> first (used during re-evaluation).
	if m := replanRegex.FindStringSubmatch(reply); len(m) >= 2 {
		return extractSteps(m[1])
	}
	// Then try <plan>.
	if m := planRegex.FindStringSubmatch(reply); len(m) >= 2 {
		return extractSteps(m[1])
	}
	// Fallback: bare <step> tags without a wrapping block.
	if steps := extractSteps(reply); len(steps) > 0 {
		return steps
	}
	return nil
}

func extractSteps(content string) []string {
	stepMatches := planStepRegex.FindAllStringSubmatch(content, -1)
	var steps []string
	for _, sm := range stepMatches {
		if len(sm) >= 2 {
			step := strings.TrimSpace(sm[1])
			if step != "" {
				steps = append(steps, step)
			}
		}
	}
	return steps
}
