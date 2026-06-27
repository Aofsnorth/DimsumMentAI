package coordinator

import (
	"fmt"
	"math"
	"strings"

	"bedrock-ai/internal/bot/building/common"
	"bedrock-ai/internal/bot/building/house"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
)

func (ba *BuilderAgent) resolveBuildSpot(request string) (int, int, int) {
	botPos := ba.bot.GetCoords()
	cx := int(math.Floor(float64(botPos.X())))
	cy := int(math.Floor(float64(botPos.Y())))
	cz := int(math.Floor(float64(botPos.Z())))

	var targetPlayerPos mgl32.Vec3
	foundPlayer := false
	words := strings.Fields(request)
	for _, w := range words {
		pPos, found := ba.bot.GetPlayerCoords(strings.ReplaceAll(w, "@", ""))
		if found {
			targetPlayerPos = pPos
			foundPlayer = true
			break
		}
	}

	if foundPlayer {
		cx = int(math.Floor(float64(targetPlayerPos.X())))
		cy = int(math.Floor(float64(targetPlayerPos.Y())))
		cz = int(math.Floor(float64(targetPlayerPos.Z())))
	}
	return cx, cy, cz
}

func (ba *BuilderAgent) formatNearbyStructures(nearby []common.StructureInfo) string {
	if len(nearby) == 0 {
		return ""
	}
	var list []string
	for _, st := range nearby {
		list = append(list, fmt.Sprintf("%s(%d,%d,%d)", st.Name, st.X, st.Y, st.Z))
	}
	return strings.Join(list, ", ")
}

func (ba *BuilderAgent) resolvePlanSize(sizeCategory string) int {
	switch sizeCategory {
	case "super_small":
		return 4
	case "small":
		return 6
	case "medium":
		return 9
	case "large":
		return 13
	}
	return 6
}

func (ba *BuilderAgent) generateFallbackHouse(request string, bx, by, bz int) []common.BlockEntry {
	var blocksToPlace []common.BlockEntry
	if strings.Contains(strings.ToLower(request), "mewah") || strings.Contains(strings.ToLower(request), "super") {
		blocksToPlace = house.GenerateSuperModern()
		ba.bot.ReportActionStatus("", event.ActionStatus{Action: "build", Item: "super_modern_villa", Success: true})
	} else if strings.Contains(strings.ToLower(request), "modern") {
		blocksToPlace = house.GenerateModern()
		ba.bot.ReportActionStatus("", event.ActionStatus{Action: "build", Item: "modern_house", Success: true})
	} else {
		blocksToPlace = house.GenerateMinimalist()
		ba.bot.ReportActionStatus("", event.ActionStatus{Action: "build", Item: "minimalist_house", Success: true})
	}

	for i := range blocksToPlace {
		blocksToPlace[i].X += bx
		blocksToPlace[i].Y += by
		blocksToPlace[i].Z += bz
	}
	return house.SortSchematic(blocksToPlace)
}

func (ba *BuilderAgent) generateAIBlueprint(user, request string, available []common.BuildItem, bx, by, bz int) []common.BlockEntry {
	ba.bot.ReportActionStatus("", event.ActionStatus{Action: "build", Item: "custom_blueprint", Success: true})

	analysisCtx := map[string]interface{}{
		"materials":   ba.getMaterialsSummary(available),
		"totalBlocks": 150,
	}
	concept, err := ba.architect.AnalyzeRequest(user, request, analysisCtx)
	if err == nil {
		blueprint, err := ba.architect.GenerateDetailedBlueprint(concept, analysisCtx)
		if err == nil && len(blueprint) > 0 {
			for i := range blueprint {
				blueprint[i].X += bx
				blueprint[i].Y += by
				blueprint[i].Z += bz
			}
			return ba.architect.OptimizeBuildingOrder(blueprint, concept)
		}
	}
	return nil
}
