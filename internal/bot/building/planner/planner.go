package planner

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"bedrock-ai/internal/ai"
)

// AIPlanner handles template selection via LLM prompt queries.
type AIPlanner struct {
	aiClient *ai.NvidiaClient
	logger   *slog.Logger
}

// NewAIPlanner creates a new AIPlanner instance.
func NewAIPlanner(aiClient *ai.NvidiaClient, logger *slog.Logger) *AIPlanner {
	return &AIPlanner{
		aiClient: aiClient,
		logger:   logger,
	}
}

func (ap *AIPlanner) buildPrompt(user, request string, context map[string]interface{}, simplify bool) string {
	materials, _ := context["materials"].(string)
	if materials == "" {
		materials = "oak_planks"
	}
	nearby, _ := context["nearbyStructures"].(string)
	if nearby == "" {
		nearby = "none"
	}

	prompt := "You are a Minecraft building planner. Analyze the request and output a JSON plan.\n"
	prompt += fmt.Sprintf("\nREQUEST: \"%s\"\n", request)
	prompt += fmt.Sprintf("AVAILABLE MATERIALS: %s\n", materials)
	prompt += fmt.Sprintf("CONTEXT: %s\n", nearby)

	if simplify {
		prompt += "\nCRITICAL: YOU MUST OUTPUT ONLY VALID JSON. NO MARKDOWN. NO EXPLANATIONS. START WITH { AND END WITH }.\n"
	}

	prompt += `
OUTPUT FORMAT (JSON only):
{
  "structureType": "house|tower|wall|bridge|custom",
  "sizeCategory": "super_small|small|medium|large",
  "materials": {
    "primary": "oak_planks",
    "secondary": "cobblestone",
    "roof": "oak_planks"
  },
  "orientation": "north|south|east|west"
}

RULES:
1. Choose structureType based on request keywords
2. Select sizeCategory based on request and materials (super_small: <20 blocks, small: <50 blocks, medium: 50-150, large: 150+)
3. Use only available materials
4. Output ONLY valid JSON, no markdown
`
	return prompt
}

func (ap *AIPlanner) extractJson(text string) string {
	cleaned := strings.ReplaceAll(text, "```json", "")
	cleaned = strings.ReplaceAll(cleaned, "```", "")
	cleaned = strings.ReplaceAll(cleaned, "json", "")

	start := strings.Index(cleaned, "{")
	if start == -1 {
		return cleaned
	}

	bracketCount := 0
	end := -1
	for i := start; i < len(cleaned); i++ {
		if cleaned[i] == '{' {
			bracketCount++
		} else if cleaned[i] == '}' {
			bracketCount--
			if bracketCount == 0 {
				end = i
				break
			}
		}
	}

	if end != -1 {
		res := cleaned[start : end+1]
		res = regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(res, "$1")
		return res
	}

	return cleaned
}
