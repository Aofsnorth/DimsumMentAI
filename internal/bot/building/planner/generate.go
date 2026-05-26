package planner

import (
	"encoding/json"
	"time"

	"bedrock-ai/internal/bot/building/common"
)

// GeneratePlan queries the LLM for a BuildPlan.
func (ap *AIPlanner) GeneratePlan(user, request string, context map[string]interface{}) (*common.BuildPlan, error) {
	if ap.aiClient == nil {
		return ap.GetDefaultPlan(context), nil
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

	pos, _ := context["position"].(common.Vec3i)

	plan := &common.BuildPlan{
		StructureType: parsed.StructureType,
		SizeCategory:  parsed.SizeCategory,
		Materials: common.BuildMaterials{
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

func (ap *AIPlanner) retryWithSimplifiedPrompt(user, request string, context map[string]interface{}) (*common.BuildPlan, error) {
	prompt := ap.buildPrompt(user, request, context, true)
	sysPrompt := ap.aiClient.BuildSystemPrompt("Architect", "", "", "", "")

	resp, err := ap.aiClient.Ask(user, sysPrompt, prompt)
	if err != nil {
		return ap.GetDefaultPlan(context), nil
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
		return ap.GetDefaultPlan(context), nil
	}

	pos, _ := context["position"].(common.Vec3i)

	plan := &common.BuildPlan{
		StructureType: parsed.StructureType,
		SizeCategory:  parsed.SizeCategory,
		Materials: common.BuildMaterials{
			Primary:   parsed.Materials.Primary,
			Secondary: parsed.Materials.Secondary,
		},
		Position:    pos,
		Orientation: parsed.Orientation,
		Mode:        "hybrid",
	}

	if !plan.IsValid() {
		return ap.GetDefaultPlan(context), nil
	}

	return plan, nil
}

func (ap *AIPlanner) GetDefaultPlan(context map[string]interface{}) *common.BuildPlan {
	pos, _ := context["position"].(common.Vec3i)
	return &common.BuildPlan{
		StructureType: "house",
		SizeCategory:  "small",
		Materials: common.BuildMaterials{
			Primary:   "oak_planks",
			Secondary: "cobblestone",
		},
		Position:    pos,
		Orientation: "north",
		Mode:        "hybrid",
	}
}
