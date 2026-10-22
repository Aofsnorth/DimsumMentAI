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

var actionRegex = regexp.MustCompile(`(?is)<action(?:\s+[^>]*)?>(.*?)</action>`)

// selfClosingActionRegex and bracketActionRegex catch malformed action tags
// occasionally emitted by models (<action label="come"/> or [action:come]).
var selfClosingActionRegex = regexp.MustCompile(`(?is)<action\s+(?:label|name|type)=["']?([^"'/>\s]+)["']?\s*/>`)
var bracketActionRegex = regexp.MustCompile(`(?i)\[action:\s*([^\]]+?)\s*\]`)

// unclosedActionRegex matches an opening <action> tag that never received its
// closing </action> due to MaxTokens truncation.
var unclosedActionRegex = regexp.MustCompile(`(?is)<action(?:\s+[^>]*)?>.*$`)

// planRegex matches a <plan>...</plan> block containing <step> tags.
var planRegex = regexp.MustCompile(`(?is)<plan>(.*?)</plan>`)
var unclosedPlanRegex = regexp.MustCompile(`(?is)<plan>.*$`)
var planStepRegex = regexp.MustCompile(`(?is)<step>(.*?)</step>`)

// replanRegex matches a <replan>...</replan> block (same inner format as
// <plan>). Used by the planner's agentic loop when the LLM adjusts remaining
// steps mid-execution.
var replanRegex = regexp.MustCompile(`(?is)<replan>(.*?)</replan>`)
var unclosedReplanRegex = regexp.MustCompile(`(?is)<replan>.*$`)

// followupRegex matches a <followup>N</followup> tag where N is the delay in
// seconds before the bot should send a follow-up message.
var followupRegex = regexp.MustCompile(`(?i)<followup>\s*(\d+)\s*</followup>`)
var anyFollowupTagRegex = regexp.MustCompile(`(?is)</?followup[^>]*>`)

// thinkRegex matches reasoning blocks emitted by chain-of-thought models such
// as Minimax M2, DeepSeek-R1, etc.
var thinkRegex = regexp.MustCompile(`(?is)<think>.*?</think>`)

// unclosedThinkRegex strips a leading <think>... that was cut by MaxTokens
// before the closing </think> could be emitted. Also strips a leading
// </think> when the model emitted only the closing tag.
var unclosedThinkRegex = regexp.MustCompile(`(?is)^\s*<think>.*$`)
var leadingThinkCloseRegex = regexp.MustCompile(`(?is)^.*?</think>\s*`)

// emDashCleaner replaces typographic em-dashes and en-dashes (classic LLM
// punctuation tells that real Minecraft players never type) with natural commas.
var emDashReplacer = strings.NewReplacer(
	" — ", ", ",
	" – ", ", ",
	"—", ", ",
	"–", ", ",
)

// Parse extracts clean text, actions, and plan steps from the AI's reply.
func Parse(reply string) ParsedReply {
	var planSteps []string

	// Strip chain-of-thought reasoning blocks first.
	reply = thinkRegex.ReplaceAllString(reply, "")
	reply = leadingThinkCloseRegex.ReplaceAllString(reply, "")
	reply = unclosedThinkRegex.ReplaceAllString(reply, "")
	reply = strings.TrimSpace(reply)

	// A reply carrying the <silent/> marker means "say nothing".
	if strings.Contains(strings.ToLower(reply), "<silent") {
		return ParsedReply{}
	}

	// Extract <plan> or <replan> steps BEFORE stripping tags.
	if planMatch := planRegex.FindStringSubmatch(reply); len(planMatch) >= 2 {
		planSteps = extractSteps(planMatch[1])
	} else if replanMatch := replanRegex.FindStringSubmatch(reply); len(replanMatch) >= 2 {
		planSteps = extractSteps(replanMatch[1])
	} else if bareSteps := extractSteps(reply); len(bareSteps) > 0 {
		planSteps = bareSteps
	}

	// Extract <followup>N</followup> delay (0 = no followup, capped at 300s).
	followupSec := 0
	if fm := followupRegex.FindStringSubmatch(reply); len(fm) >= 2 {
		_, _ = fmt.Sscanf(fm[1], "%d", &followupSec)
		if followupSec < 0 {
			followupSec = 0
		} else if followupSec > 300 {
			followupSec = 300
		}
	}

	actions := extractActions(reply)

	// Clean up the reply text by removing all action/plan/followup tags.
	cleanReply := actionRegex.ReplaceAllString(reply, "")
	cleanReply = selfClosingActionRegex.ReplaceAllString(cleanReply, "")
	cleanReply = bracketActionRegex.ReplaceAllString(cleanReply, "")
	cleanReply = unclosedActionRegex.ReplaceAllString(cleanReply, "")
	cleanReply = planRegex.ReplaceAllString(cleanReply, "")
	cleanReply = unclosedPlanRegex.ReplaceAllString(cleanReply, "")
	cleanReply = replanRegex.ReplaceAllString(cleanReply, "")
	cleanReply = unclosedReplanRegex.ReplaceAllString(cleanReply, "")
	cleanReply = planStepRegex.ReplaceAllString(cleanReply, "")
	cleanReply = followupRegex.ReplaceAllString(cleanReply, "")
	cleanReply = anyFollowupTagRegex.ReplaceAllString(cleanReply, "")
	cleanReply = sanitizeChatText(cleanReply)

	// Drop completion claims made while the action they describe is still pending.
	cleanReply, _ = FilterCompletionClaims(cleanReply, len(actions) > 0)

	return ParsedReply{
		CleanReply:  cleanReply,
		Actions:     actions,
		PlanSteps:   planSteps,
		FollowupSec: followupSec,
	}
}

func extractActions(reply string) []Action {
	matches := actionRegex.FindAllStringSubmatch(reply, -1)
	if len(matches) == 0 {
		matches = selfClosingActionRegex.FindAllStringSubmatch(reply, -1)
	}
	if len(matches) == 0 {
		matches = bracketActionRegex.FindAllStringSubmatch(reply, -1)
	}
	actions := make([]Action, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		if act, ok := parseSingleAction(match[1]); ok {
			actions = append(actions, act)
		}
	}
	return actions
}

func parseSingleAction(raw string) (Action, bool) {
	rawAction := strings.TrimSpace(raw)
	if rawAction == "" {
		return Action{}, false
	}
	parts := strings.SplitN(rawAction, ":", 2)
	var act Action
	if len(parts) > 0 {
		act.Label = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		act.Param = strings.TrimSpace(parts[1])
	}
	if act.Label == "" {
		return Action{}, false
	}
	return act, true
}

// sanitizeChatText removes typographic em-dashes/en-dashes unless the reply is
// a self-correction draft (which downstream filters detect and replace), and
// collapses whitespace.
func sanitizeChatText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !IsSelfCorrection(s) {
		s = emDashCleanerApply(s)
	}
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func emDashCleanerApply(s string) string {
	s = emDashReplacer.Replace(s)
	s = strings.ReplaceAll(s, ",,", ",")
	s = strings.ReplaceAll(s, ", ,", ",")
	return s
}

// StripUserPrefix removes a stiff leading "<PlayerName>, " vocative prefix
// (e.g. "Synterix, kok malah mukul?" -> "kok malah mukul?") when the LLM
// addresses the player by their full username at the start of a chat line.
func StripUserPrefix(reply, user string) string {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || user == "" {
		return trimmed
	}
	candidates := []string{user}
	if withoutDot := strings.TrimPrefix(user, "."); withoutDot != "" && withoutDot != user {
		candidates = append(candidates, withoutDot)
	}
	lower := strings.ToLower(trimmed)
	for _, name := range candidates {
		prefix := strings.ToLower(name) + ","
		if strings.HasPrefix(lower, prefix) {
			rest := strings.TrimSpace(trimmed[len(prefix):])
			if rest != "" {
				return rest
			}
		}
	}
	return trimmed
}

// ParsePlanSteps extracts <step> contents from either a <plan> or <replan>
// block in the reply. Returns nil if no plan/replan block is present.
func ParsePlanSteps(reply string) []string {
	reply = thinkRegex.ReplaceAllString(reply, "")
	reply = leadingThinkCloseRegex.ReplaceAllString(reply, "")
	reply = unclosedThinkRegex.ReplaceAllString(reply, "")
	reply = strings.TrimSpace(reply)
	if m := replanRegex.FindStringSubmatch(reply); len(m) >= 2 {
		return extractSteps(m[1])
	}
	if m := planRegex.FindStringSubmatch(reply); len(m) >= 2 {
		return extractSteps(m[1])
	}
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
