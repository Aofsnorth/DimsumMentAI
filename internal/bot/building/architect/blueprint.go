package architect

import (
	"encoding/json"
	"fmt"
	"time"

	"bedrock-ai/internal/bot/building/common"
)

// GenerateDetailedBlueprint runs Stage 2, generating a complete block-by-block blueprint.
func (ea *EnhancedAIArchitect) GenerateDetailedBlueprint(concept *common.Concept, context map[string]interface{}) ([]common.BlockEntry, error) {
	time.Sleep(2 * time.Second)

	materials, _ := context["materials"].(string)
	if materials == "" {
		materials = "oak_planks, cobblestone, glass"
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
		return []common.BlockEntry{}, fmt.Errorf("AI client not configured")
	}

	sysPrompt := ea.aiClient.BuildSystemPrompt("Architect", "", "", "", "")
	resp, err := ea.aiClient.Ask("owner", sysPrompt, prompt)
	if err != nil {
		return []common.BlockEntry{}, err
	}

	clean := ea.extractJsonArray(resp)
	var blueprint []common.BlockEntry
	if err := json.Unmarshal([]byte(clean), &blueprint); err != nil {
		ea.logger.Error("Failed to parse AI blueprint JSON", "err", err.Error())
		return []common.BlockEntry{}, err
	}

	seen := make(map[string]bool)
	var unique []common.BlockEntry
	for _, b := range blueprint {
		key := fmt.Sprintf("%d,%d,%d", b.X, b.Y, b.Z)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, b)
		}
	}

	return unique, nil
}
