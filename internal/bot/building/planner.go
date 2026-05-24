package building

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"bedrock-ai/internal/ai"
)

// Concept represents the AI's high-level stylistic and architectural design.
type Concept struct {
	StructureType   string            `json:"structureType"`
	HouseType       string            `json:"houseType"`
	Style           string            `json:"style"`
	Complexity      string            `json:"complexity"`
	Dimensions      Vec3i             `json:"dimensions"`
	Sections        []string          `json:"sections"`
	Features        []string          `json:"features"`
	Materials       map[string]string `json:"materials"`
	BuildingFlow    string            `json:"buildingFlow"`
	SpecialRequests []string          `json:"specialRequests"`
}

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
		// Clean trailing commas
		res = regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(res, "$1")
		return res
	}

	return cleaned
}

// GeneratePlan queries the LLM for a BuildPlan.
func (ap *AIPlanner) GeneratePlan(user, request string, context map[string]interface{}) (*BuildPlan, error) {
	if ap.aiClient == nil {
		return ap.getDefaultPlan(context), nil
	}

	start := time.Now()
	prompt := ap.buildPrompt(user, request, context, false)
	sysPrompt := ap.aiClient.BuildSystemPrompt("Architect", "", "", "", "")

	resp, err := ap.aiClient.Ask(user, sysPrompt, prompt)
	if err != nil {
		ap.logger.Warn("Failed to query AI architect, using retry layout", "err", err.Error())
		return ap.retryWithSimplifiedPrompt(user, request, context)
	}

	clean := ap.extractJson(resp)
	var parsed struct {
		StructureType string `json:"structureType"`
		SizeCategory  string `json:"sizeCategory"`
		Materials     struct {
			Primary   string `json:"primary"`
			Secondary string `json:"secondary"`
			Roof      string `json:"roof"`
		} `json:"materials"`
		Orientation string `json:"orientation"`
	}

	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		ap.logger.Warn("Failed to parse AI plan JSON, retrying with simplified instructions", "err", err.Error())
		return ap.retryWithSimplifiedPrompt(user, request, context)
	}

	pos, _ := context["position"].(Vec3i)

	plan := &BuildPlan{
		StructureType: parsed.StructureType,
		SizeCategory:  parsed.SizeCategory,
		Materials: BuildMaterials{
			Primary:   parsed.Materials.Primary,
			Secondary: parsed.Materials.Secondary,
		},
		Position:    pos,
		Orientation: parsed.Orientation,
		Mode:        "hybrid",
	}

	if !plan.IsValid() {
		return ap.retryWithSimplifiedPrompt(user, request, context)
	}

	ap.logger.Info("AI template plan resolved", "type", plan.StructureType, "size", plan.SizeCategory, "time", time.Since(start).String())
	return plan, nil
}

func (ap *AIPlanner) retryWithSimplifiedPrompt(user, request string, context map[string]interface{}) (*BuildPlan, error) {
	prompt := ap.buildPrompt(user, request, context, true)
	sysPrompt := ap.aiClient.BuildSystemPrompt("Architect", "", "", "", "")

	resp, err := ap.aiClient.Ask(user, sysPrompt, prompt)
	if err != nil {
		return ap.getDefaultPlan(context), nil
	}

	clean := ap.extractJson(resp)
	var parsed struct {
		StructureType string `json:"structureType"`
		SizeCategory  string `json:"sizeCategory"`
		Materials     struct {
			Primary   string `json:"primary"`
			Secondary string `json:"secondary"`
		} `json:"materials"`
		Orientation string `json:"orientation"`
	}

	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return ap.getDefaultPlan(context), nil
	}

	pos, _ := context["position"].(Vec3i)

	plan := &BuildPlan{
		StructureType: parsed.StructureType,
		SizeCategory:  parsed.SizeCategory,
		Materials: BuildMaterials{
			Primary:   parsed.Materials.Primary,
			Secondary: parsed.Materials.Secondary,
		},
		Position:    pos,
		Orientation: parsed.Orientation,
		Mode:        "hybrid",
	}

	if !plan.IsValid() {
		return ap.getDefaultPlan(context), nil
	}

	return plan, nil
}

func (ap *AIPlanner) getDefaultPlan(context map[string]interface{}) *BuildPlan {
	pos, _ := context["position"].(Vec3i)
	return &BuildPlan{
		StructureType: "house",
		SizeCategory:  "small",
		Materials: BuildMaterials{
			Primary:   "oak_planks",
			Secondary: "cobblestone",
		},
		Position:    pos,
		Orientation: "north",
		Mode:        "hybrid",
	}
}

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
func (ea *EnhancedAIArchitect) AnalyzeRequest(user, request string, context map[string]interface{}) (*Concept, error) {
	materials, _ := context["materials"].(string)
	if materials == "" {
		materials = "oak_planks, cobblestone"
	}
	totalBlocks, _ := context["totalBlocks"].(int)
	if totalBlocks == 0 {
		totalBlocks = 100
	}
	nearby, _ := context["nearbyStructures"].(string)
	if nearby == "" {
		nearby = "none"
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
	var concept Concept
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
		// Look for array instead
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

	// Try repair if unclosed
	lastBrace := strings.LastIndex(cleaned, "}")
	if lastBrace > start {
		res := cleaned[start:lastBrace+1] + "]"
		res = regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(res, "$1")
		return res
	}

	return cleaned
}

// GenerateDetailedBlueprint runs Stage 2, generating a complete block-by-block blueprint.
func (ea *EnhancedAIArchitect) GenerateDetailedBlueprint(concept *Concept, context map[string]interface{}) ([]BlockEntry, error) {
	time.Sleep(2 * time.Second) // Delay to prevent API rate limiting

	materials, _ := context["materials"].(string)
	if materials == "" {
		materials = "oak_planks, cobblestone, glass"
	}
	totalBlocks, _ := context["totalBlocks"].(int)
	if totalBlocks == 0 {
		totalBlocks = 100
	}

	width := concept.Dimensions.X
	depth := concept.Dimensions.Z
	height := concept.Dimensions.Y

	prompt := `You are a Minecraft building expert. Generate a creative and functional house blueprint.

=== HOUSE CONCEPT ===
%s

=== AVAILABLE MATERIALS ===
%s

=== CRITICAL RULES ===
1. NEVER place two blocks at the same (x,y,z) position - each coordinate must be unique.
2. FLOOR must be COMPLETE - place floor blocks at ALL positions y=0 (no holes in floor!).
3. WALLS are HOLLOW - only place blocks at perimeter (edges), NOT in middle.
4. INTERIOR at y=1 should have FURNITURE against walls (crafting_table, chest, furnace, etc).
5. CENTER of interior (y=1) must be EMPTY AIR for walking.
6. MUST have a door - place oak_door or spruce_door at y=1 in front wall with "facing":"south".
7. Add windows (glass) in walls at y=2.
8. Roof at y=height should cover entire top.

=== BUILDING INSTRUCTIONS ===
1. FLOOR (y=0): Complete coverage from x=0 to %d, z=0 to %d using flooring wood/stone.
2. WALLS (y=1 to %d): Perimeter only (x=0, x=%d, z=0, z=%d). Add door gap at front center (y=1, z=0). Add glass panes at y=2.
3. ROOF (y=%d): Complete coverage.

Output ONLY a JSON array: [{"x":0,"y":0,"z":0,"block":"stone_bricks","facing":"north"}, ...]
`

	conceptJSON, _ := json.MarshalIndent(concept, "", "  ")
	prompt = fmt.Sprintf(prompt, string(conceptJSON), materials, width-1, depth-1, height-1, width-1, depth-1, height)

	if ea.aiClient == nil {
		return []BlockEntry{}, fmt.Errorf("AI client not configured")
	}

	sysPrompt := ea.aiClient.BuildSystemPrompt("Architect", "", "", "", "")
	resp, err := ea.aiClient.Ask("owner", sysPrompt, prompt)
	if err != nil {
		return []BlockEntry{}, err
	}

	clean := ea.extractJsonArray(resp)
	var blueprint []BlockEntry
	if err := json.Unmarshal([]byte(clean), &blueprint); err != nil {
		ea.logger.Error("Failed to parse AI blueprint JSON", "err", err.Error())
		return []BlockEntry{}, err
	}

	// Filter out duplicate positions
	seen := make(map[string]bool)
	var unique []BlockEntry
	for _, b := range blueprint {
		key := fmt.Sprintf("%d,%d,%d", b.X, b.Y, b.Z)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, b)
		}
	}

	return unique, nil
}

// OptimizeBuildingOrder runs Stage 3, organizing blueprint blocks logically.
func (ea *EnhancedAIArchitect) OptimizeBuildingOrder(blueprint []BlockEntry, concept *Concept) []BlockEntry {
	if len(blueprint) == 0 {
		return []BlockEntry{}
	}

	flow := strings.ToLower(concept.BuildingFlow)
	if flow == "layer" {
		var sorted []BlockEntry
		for _, b := range blueprint {
			sorted = append(sorted, b)
		}
		// Sort: bottom-up Y, then Z, then X
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[i].Y > sorted[j].Y || (sorted[i].Y == sorted[j].Y && sorted[i].Z > sorted[j].Z) || (sorted[i].Y == sorted[j].Y && sorted[i].Z == sorted[j].Z && sorted[i].X > sorted[j].X) {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		return sorted
	}

	// Default Natural Flow: Floor -> Walls Side-by-Side -> Interior decorations -> Roof
	var floor []BlockEntry
	var walls []BlockEntry
	var interior []BlockEntry
	var roof []BlockEntry

	width := concept.Dimensions.X
	depth := concept.Dimensions.Z
	height := concept.Dimensions.Y

	for _, b := range blueprint {
		if b.Y == 0 {
			floor = append(floor, b)
		} else if b.Y == height {
			roof = append(roof, b)
		} else {
			isPerimeter := b.X == 0 || b.X == width-1 || b.Z == 0 || b.Z == depth-1
			if isPerimeter {
				walls = append(walls, b)
			} else {
				interior = append(interior, b)
			}
		}
	}

	// Sort Floor: X then Z
	for i := 0; i < len(floor); i++ {
		for j := i + 1; j < len(floor); j++ {
			if floor[i].Z > floor[j].Z || (floor[i].Z == floor[j].Z && floor[i].X > floor[j].X) {
				floor[i], floor[j] = floor[j], floor[i]
			}
		}
	}

	// Sort Walls per side, layer by layer
	var sortedWalls []BlockEntry
	for y := 1; y < height; y++ {
		var front, left, back, right []BlockEntry
		for _, b := range walls {
			if b.Y == y {
				if b.Z == 0 {
					front = append(front, b)
				} else if b.X == 0 {
					left = append(left, b)
				} else if b.Z == depth-1 {
					back = append(back, b)
				} else if b.X == width-1 {
					right = append(right, b)
				}
			}
		}
		// Sort front wall left-to-right
		for i := 0; i < len(front); i++ {
			for j := i + 1; j < len(front); j++ {
				if front[i].X > front[j].X {
					front[i], front[j] = front[j], front[i]
				}
			}
		}
		// Sort left wall front-to-back
		for i := 0; i < len(left); i++ {
			for j := i + 1; j < len(left); j++ {
				if left[i].Z > left[j].Z {
					left[i], left[j] = left[j], left[i]
				}
			}
		}
		// Sort back wall right-to-left
		for i := 0; i < len(back); i++ {
			for j := i + 1; j < len(back); j++ {
				if back[i].X < back[j].X {
					back[i], back[j] = back[j], back[i]
				}
			}
		}
		// Sort right wall back-to-front
		for i := 0; i < len(right); i++ {
			for j := i + 1; j < len(right); j++ {
				if right[i].Z < right[j].Z {
					right[i], right[j] = right[j], right[i]
				}
			}
		}
		sortedWalls = append(sortedWalls, front...)
		sortedWalls = append(sortedWalls, left...)
		sortedWalls = append(sortedWalls, back...)
		sortedWalls = append(sortedWalls, right...)
	}

	// Sort Interior: bottom-up
	for i := 0; i < len(interior); i++ {
		for j := i + 1; j < len(interior); j++ {
			if interior[i].Y > interior[j].Y || (interior[i].Y == interior[j].Y && interior[i].Z > interior[j].Z) {
				interior[i], interior[j] = interior[j], interior[i]
			}
		}
	}

	// Sort Roof: X then Z
	for i := 0; i < len(roof); i++ {
		for j := i + 1; j < len(roof); j++ {
			if roof[i].Z > roof[j].Z || (roof[i].Z == roof[j].Z && roof[i].X > roof[j].X) {
				roof[i], roof[j] = roof[j], roof[i]
			}
		}
	}

	var optimized []BlockEntry
	optimized = append(optimized, floor...)
	optimized = append(optimized, sortedWalls...)
	optimized = append(optimized, interior...)
	optimized = append(optimized, roof...)

	// Append any leftover blocks not captured
	placed := make(map[string]bool)
	for _, b := range optimized {
		placed[fmt.Sprintf("%d,%d,%d", b.X, b.Y, b.Z)] = true
	}
	for _, b := range blueprint {
		key := fmt.Sprintf("%d,%d,%d", b.X, b.Y, b.Z)
		if !placed[key] {
			optimized = append(optimized, b)
		}
	}

	ea.logger.Info("Enhanced building order optimized", "count", len(optimized))
	return optimized
}

func (ea *EnhancedAIArchitect) getDefaultConcept(request string, context map[string]interface{}) *Concept {
	return &Concept{
		StructureType: "house",
		HouseType:     "minimalist",
		Style:         "standard shelter",
		Complexity:    "small",
		Dimensions:    Vec3i{X: 5, Y: 3, Z: 5},
		Features:      []string{"door", "windows", "roof"},
		Materials: map[string]string{
			"primary":   "oak_planks",
			"secondary": "cobblestone",
		},
		BuildingFlow: "natural",
	}
}
