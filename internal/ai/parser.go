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

// Parse extracts clean text and actions from the AI's reply.
func Parse(reply string) ParsedReply {
	var actions []Action

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
	cleanReply = strings.TrimSpace(cleanReply)

	// Clean up any extra whitespace or newlines
	cleanReply = regexp.MustCompile(`\s+`).ReplaceAllString(cleanReply, " ")

	return ParsedReply{
		CleanReply: cleanReply,
		Actions:    actions,
	}
}
