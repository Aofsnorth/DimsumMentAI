package chat

import (
	"regexp"
	"strings"

	"bedrock-ai/internal/bot/action"
)

var coordinateIntentRegex = regexp.MustCompile(`(?i)(?:koordinat|kordinat|coords?|coordinate|goto|jalan\s+ke|pergi\s+ke|ke)[^-+0-9]*([-+]?\d+(?:\.\d+)?)\s+([-+]?\d+(?:\.\d+)?)\s+([-+]?\d+(?:\.\d+)?)`)

func fallbackMovementActions(msg string) []action.Step {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil
	}
	if match := coordinateIntentRegex.FindStringSubmatch(msg); len(match) == 4 {
		return []action.Step{{
			Label: "goto",
			Param: match[1] + "," + match[2] + "," + match[3],
		}}
	}

	lower := strings.ToLower(msg)
	if strings.Contains(lower, "kesini") ||
		strings.Contains(lower, "ke sini") ||
		strings.Contains(lower, "come here") ||
		strings.Contains(lower, "datang ke sini") {
		return []action.Step{{Label: "come"}}
	}
	return nil
}
