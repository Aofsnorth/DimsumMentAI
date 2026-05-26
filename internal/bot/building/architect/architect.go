package architect

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot/building/common"
)

// EnhancedAIArchitect drives Stage 1 concept analysis and Stage 2 raw blueprint generation.
type EnhancedAIArchitect struct {
	aiClient *ai.NvidiaClient
	logger   *slog.Logger
}

// NewEnhancedAIArchitect creates a new EnhancedAIArchitect.
func NewEnhancedAIArchitect(aiClient *ai.NvidiaClient, logger *slog.Logger) *EnhancedAIArchitect {
	return &EnhancedAIArchitect{
		aiClient: aiClient,
		logger:   logger,
	}
}

// AnalyzeRequest runs Stage 1 concept generation using structural keywords.
func (ea *EnhancedAIArchitect) AnalyzeRequest(user, request string, context map[string]interface{}) (*common.Concept, error) {
	materials, _ := context["materials"].(string)
	if materials == "" {
		materials = "oak_planks, cobblestone"
	}
	totalBlocks, _ := context["totalBlocks"].(int)
	if totalBlocks == 0 {
		totalBlocks = 100
	}

	prompt := `Analyze this Minecraft building request and respond with ONLY a JSON object.

User request: "%s"

Context:
- Available materials: %s
- Block budget: %d blocks

Respond with ONLY this JSON structure (no other text, no markdown block):
{"structureType":"house","houseType":"medieval","style":"small medieval cottage","complexity":"small","dimensions":{"x":8,"y":6,"z":10},"sections":[],"features":["door","windows","roof","chimney"],"materials":{"primary":"oak_planks","secondary":"cobblestone","accent":"stone_bricks","roof":"oak_stairs","pillar":"oak_log","window":"glass","floor":"oak_planks","foundation":"cobblestone","door":"oak_door","fence":"oak_fence"},"buildingFlow":"natural","specialRequests":["peaked_roof","chimney"]}

Rules:
- Select dimensions size category based on block budget:
  * kecil/small (100-200 blocks): 7x5x9 to 9x7x11 (where y is height, e.g. x:8, y:6, z:10)
  * sedang/medium (200-500 blocks): 11x8x13 to 13x10x16
  * besar/large (500-1000 blocks): 15x12x18 to 18x14x20
- Detect style: modern, traditional, castle, tower, medieval, japanese, fantasy
- Output ONLY the JSON, nothing before or after.
`
	prompt = fmt.Sprintf(prompt, request, materials, totalBlocks)

	if ea.aiClient == nil {
		return ea.getDefaultConcept(request, context), nil
	}

	sysPrompt := ea.aiClient.BuildSystemPrompt("Architect", "", "", "", "")
	resp, err := ea.aiClient.Ask(user, sysPrompt, prompt)
	if err != nil {
		return ea.getDefaultConcept(request, context), nil
	}

	clean := ea.extractJson(resp)
	var concept common.Concept
	if err := json.Unmarshal([]byte(clean), &concept); err != nil {
		ea.logger.Error("Failed to parse AI architectural concept", "err", err.Error())
		return ea.getDefaultConcept(request, context), nil
	}

	return &concept, nil
}

func (ea *EnhancedAIArchitect) extractJson(text string) string {
	cleaned := strings.ReplaceAll(text, "```json", "")
	cleaned = strings.ReplaceAll(cleaned, "```", "")
	cleaned = strings.ReplaceAll(cleaned, "json", "")

	start := strings.Index(cleaned, "{")
	if start == -1 {
		start = strings.Index(cleaned, "[")
		if start == -1 {
			return cleaned
		}
		end := strings.LastIndex(cleaned, "]")
		if end != -1 && end > start {
			res := cleaned[start : end+1]
			res = regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(res, "$1")
			return res
		}
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

func (ea *EnhancedAIArchitect) extractJsonArray(text string) string {
	cleaned := strings.ReplaceAll(text, "```json", "")
	cleaned = strings.ReplaceAll(cleaned, "```", "")
	cleaned = strings.ReplaceAll(cleaned, "json", "")

	start := strings.Index(cleaned, "[")
	if start == -1 {
		return cleaned
	}

	bracketCount := 0
	end := -1
	for i := start; i < len(cleaned); i++ {
		if cleaned[i] == '[' {
			bracketCount++
		} else if cleaned[i] == ']' {
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

	lastBrace := strings.LastIndex(cleaned, "}")
	if lastBrace > start {
		res := cleaned[start:lastBrace+1] + "]"
		res = regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(res, "$1")
		return res
	}

	return cleaned
}

func (ea *EnhancedAIArchitect) getDefaultConcept(request string, context map[string]interface{}) *common.Concept {
	return &common.Concept{
		StructureType: "house",
		HouseType:     "minimalist",
		Style:         "standard shelter",
		Complexity:    "small",
		Dimensions:    common.Vec3i{X: 5, Y: 3, Z: 5},
		Features:      []string{"door", "windows", "roof"},
		Materials: map[string]string{
			"primary":   "oak_planks",
			"secondary": "cobblestone",
		},
		BuildingFlow: "natural",
	}
}
