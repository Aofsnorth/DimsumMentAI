package coordinator

import (
	"context"
	"fmt"
	"strings"

	"bedrock-ai/internal/bot/building/common"
	"bedrock-ai/internal/bot/building/schematic"
)

// Build triggers the building pipeline from a user request.
func (ba *BuilderAgent) Build(ctx context.Context, user string, request string) {
	ba.mu.Lock()
	if ba.isBuilding {
		ba.mu.Unlock()
		ba.bot.SendSafeChat("Aku lagi sibuk bangun yang lain sekarang. Sebentar ya!")
		return
	}

	buildCtx, cancel := context.WithCancel(ctx)
	ba.cancelFn = cancel
	ba.isBuilding = true
	ba.status = "Planning..."
	ba.blocksPlaced = 0
	ba.totalBlocks = 0
	ba.mu.Unlock()

	go func() {
		defer func() {
			ba.mu.Lock()
			ba.isBuilding = false
			ba.status = "Ready"
			ba.cancelFn = nil
			ba.mu.Unlock()
		}()

		ba.logger.Info("Builder agent started build flow", "request", request, "user", user)
		ba.bot.SendSafeChat("Oke, aku denger request kamu. Bentar ya, aku rencanain dulu...")

		inv := ba.bot.GetInventorySlots()
		names := ba.bot.GetItemNames()
		available := schematic.GetBuildItems(inv, names)

		cx, cy, cz := ba.resolveBuildSpot(request)
		ba.acquisition.HandleFullInventory(buildCtx, common.Vec3i{X: cx, Y: cy, Z: cz})

		nearby := ba.scanner.ScanNearbyStructures()
		nearbyStr := ba.formatNearbyStructures(nearby)

		planCtx := map[string]interface{}{
			"materials":        ba.getMaterialsSummary(available),
			"nearbyStructures": nearbyStr,
			"position":         common.Vec3i{X: cx, Y: cy, Z: cz},
		}

		plan, err := ba.planner.GeneratePlan(user, request, planCtx)
		if err != nil {
			ba.logger.Warn("AI Plan Generation failed, using default template plan", "err", err.Error())
			plan = ba.planner.GetDefaultPlan(planCtx)
		}

		size := ba.resolvePlanSize(plan.SizeCategory)
		bx, by, bz := ba.scanner.FindFlatArea(cx, cy, cz, size)
		plan.Position = common.Vec3i{X: bx, Y: by, Z: bz}

		ba.status = "Leveling area..."
		ba.scanner.LevelArea(buildCtx, bx, by, bz, size)

		ba.status = "Preparing materials..."
		ba.acquisition.CraftToolsIfNeeded()
		ba.acquisition.CraftPlanksIfNeeded()

		inv = ba.bot.GetInventorySlots()
		available = schematic.GetBuildItems(inv, names)

		var blocksToPlace []common.BlockEntry

		if plan.Mode == "hybrid" && ba.library.HasTemplate(plan.StructureType, plan.SizeCategory) {
			layout, err := ba.executor.ExecuteTemplate(plan)
			if err == nil && len(layout) > 0 {
				blocksToPlace = layout
			}
		}

		if len(blocksToPlace) == 0 && (strings.Contains(strings.ToLower(request), "rumah") || strings.Contains(strings.ToLower(request), "house")) {
			blocksToPlace = ba.generateFallbackHouse(request, bx, by, bz)
		}

		if len(blocksToPlace) == 0 {
			blocksToPlace = ba.generateAIBlueprint(user, request, available, bx, by, bz)
		}

		if len(blocksToPlace) == 0 {
			blocksToPlace = ba.generateFallbackHouse("minimalist", bx, by, bz)
		}

		ba.mu.Lock()
		ba.totalBlocks = len(blocksToPlace)
		ba.status = "Building..."
		ba.mu.Unlock()

		ba.bot.SendSafeChat(fmt.Sprintf("Mulai membangun! Total ada %d blok.", len(blocksToPlace)))
		success := ba.executeBlockList(buildCtx, blocksToPlace, bx, bz)

		ba.placer.CleanupScaffolds(buildCtx)

		if success {
			ba.bot.SendSafeChat("Hore! Rumah kamu udah selesai aku bangun. Silakan dilihat!")
		} else {
			ba.bot.SendSafeChat("Maaf ya, pembangunannya kepotong atau ada masalah di jalan.")
		}
	}()
}
