package building

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/ai"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// BuilderAgent coordinates the high-level planning, scanning, materials preparation, and execution of build projects.
type BuilderAgent struct {
	bot         BotInterface
	logger      *slog.Logger
	aiClient    *ai.NvidiaClient
	
	// Sub-components
	library     *TemplateLibrary
	executor    *TemplateExecutor
	planner     *AIPlanner
	architect   *EnhancedAIArchitect
	scanner     *AreaScanner
	acquisition *InventoryAcquisition
	placer      *BlockPlacer

	// Build State
	mu                  sync.Mutex
	isBuilding          bool
	cancelFn            context.CancelFunc
	placedHistory       []BlockEntry
	status              string
	blocksPlaced        int
	totalBlocks         int
}

// NewBuilderAgent creates a new BuilderAgent orchestrator.
func NewBuilderAgent(bot BotInterface, logger *slog.Logger, aiClient *ai.NvidiaClient) *BuilderAgent {
	lib := NewTemplateLibrary()
	_ = lib.LoadEmbeddedTemplates()

	scanner := NewAreaScanner(bot, logger)
	acq := NewInventoryAcquisition(bot, logger, scanner)
	placer := NewBlockPlacer(bot, logger)

	return &BuilderAgent{
		bot:         bot,
		logger:      logger,
		aiClient:    aiClient,
		library:     lib,
		executor:    NewTemplateExecutor(lib, bot),
		planner:     NewAIPlanner(aiClient, logger),
		architect:   NewEnhancedAIArchitect(aiClient, logger),
		scanner:     scanner,
		acquisition: acq,
		placer:      placer,
		status:      "Ready",
	}
}

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

		// 1. Check inventory blocks
		inv := ba.bot.GetInventorySlots()
		names := ba.bot.GetItemNames()
		available := GetBuildItems(inv, names)
		ba.logger.Info("Cached placeable inventory items", "count", len(available))

		// 2. Resolve target coordinate (build area)
		botPos := ba.bot.GetCoords()
		cx := int(math.Floor(float64(botPos.X())))
		cy := int(math.Floor(float64(botPos.Y())))
		cz := int(math.Floor(float64(botPos.Z())))

		// If user requested near a player
		var targetPlayerPos mgl32.Vec3
		var foundPlayer bool
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
			ba.logger.Info("Build spot set near tracked player coordinate", "pos", targetPlayerPos)
		}

		// 3. Scan structures nearby for reference
		nearby := ba.scanner.ScanNearbyStructures()
		nearbyStr := ""
		if len(nearby) > 0 {
			var list []string
			for _, st := range nearby {
				list = append(list, fmt.Sprintf("%s(%d,%d,%d)", st.Name, st.X, st.Y, st.Z))
			}
			nearbyStr = strings.Join(list, ", ")
		}

		// Pre-build inventory check: if low on space, stash junk
		ba.acquisition.HandleFullInventory(buildCtx, Vec3i{X: cx, Y: cy, Z: cz})

		// 4. Generate AI Build Plan
		planCtx := map[string]interface{}{
			"materials":        ba.getMaterialsSummary(available),
			"nearbyStructures": nearbyStr,
			"position":         Vec3i{X: cx, Y: cy, Z: cz},
		}

		plan, err := ba.planner.GeneratePlan(user, request, planCtx)
		if err != nil {
			ba.logger.Warn("AI Plan Generation failed, using default template plan", "err", err.Error())
			plan = ba.planner.getDefaultPlan(planCtx)
		}

		ba.logger.Info("Resolved build plan", "mode", plan.Mode, "type", plan.StructureType, "size", plan.SizeCategory)

		// 5. Select build area using AreaScanner
		size := 6
		switch plan.SizeCategory {
		case "super_small":
			size = 4
		case "small":
			size = 6
		case "medium":
			size = 9
		case "large":
			size = 13
		}

		bx, by, bz := ba.scanner.FindFlatArea(cx, cy, cz, size)
		plan.Position = Vec3i{X: bx, Y: by, Z: bz}

		// 6. Foundation Leveling
		ba.status = "Leveling area..."
		ba.scanner.LevelArea(buildCtx, bx, by, bz, size)

		// 7. Acquire tools/materials if needed
		ba.status = "Preparing materials..."
		ba.acquisition.CraftToolsIfNeeded()
		ba.acquisition.CraftPlanksIfNeeded()

		// Refresh inventory
		inv = ba.bot.GetInventorySlots()
		available = GetBuildItems(inv, names)

		// 8. Generate block layout list
		var blocksToPlace []BlockEntry

		// Hybrid Mode: use pre-defined template executor
		if plan.Mode == "hybrid" && ba.library.HasTemplate(plan.StructureType, plan.SizeCategory) {
			ba.logger.Info("Executing hybrid template building layout")
			layout, err := ba.executor.ExecuteTemplate(plan)
			if err == nil && len(layout) > 0 {
				blocksToPlace = layout
			}
		}

		// Fallback: Custom House Generators
		if len(blocksToPlace) == 0 && (strings.Contains(strings.ToLower(request), "rumah") || strings.Contains(strings.ToLower(request), "house")) {
			ba.logger.Info("Executing default hardcoded house generators")
			if strings.Contains(strings.ToLower(request), "mewah") || strings.Contains(strings.ToLower(request), "super") {
				blocksToPlace = GenerateSuperModern()
				ba.bot.SendSafeChat("Aku buatin Villa Quartz Super Modern yang mewah ya!")
			} else if strings.Contains(strings.ToLower(request), "modern") {
				blocksToPlace = GenerateModern()
				ba.bot.SendSafeChat("Aku buatin rumah modern dari concrete ya!")
			} else {
				blocksToPlace = GenerateMinimalist()
				ba.bot.SendSafeChat("Aku buatin rumah kayu minimalis aja ya!")
			}

			// Apply global coordinate offset for house generators
			for i := range blocksToPlace {
				blocksToPlace[i].X += bx
				blocksToPlace[i].Y += by
				blocksToPlace[i].Z += bz
			}
			blocksToPlace = SortSchematic(blocksToPlace)
		}

		// Enhanced AI Stage 1 Concept -> Stage 2 Detailed Blueprint Flow
		if len(blocksToPlace) == 0 {
			ba.logger.Info("Executing Stage 2 Enhanced AI Blueprint Architect")
			ba.bot.SendSafeChat("Aku rancang blueprint custom pake AI dulu ya...")
			
			analysisCtx := map[string]interface{}{
				"materials":   ba.getMaterialsSummary(available),
				"totalBlocks": 150,
			}
			concept, err := ba.architect.AnalyzeRequest(user, request, analysisCtx)
			if err == nil {
				ba.logger.Info("AI Architectural Concept Resolved", "house", concept.HouseType, "w", concept.Dimensions.X, "h", concept.Dimensions.Y, "d", concept.Dimensions.Z)
				
				blueprint, err := ba.architect.GenerateDetailedBlueprint(concept, analysisCtx)
				if err == nil && len(blueprint) > 0 {
					// Translate schematic index to world space
					for i := range blueprint {
						blueprint[i].X += bx
						blueprint[i].Y += by
						blueprint[i].Z += bz
					}
					blocksToPlace = ba.architect.OptimizeBuildingOrder(blueprint, concept)
				}
			}
		}

		// Extreme fallback if everything failed
		if len(blocksToPlace) == 0 {
			ba.logger.Warn("All schematic layout flows failed, building emergency shelter")
			blocksToPlace = GenerateMinimalist()
			for i := range blocksToPlace {
				blocksToPlace[i].X += bx
				blocksToPlace[i].Y += by
				blocksToPlace[i].Z += bz
			}
			blocksToPlace = SortSchematic(blocksToPlace)
		}

		// 9. Execute Block Placement
		ba.mu.Lock()
		ba.totalBlocks = len(blocksToPlace)
		ba.status = "Building..."
		ba.mu.Unlock()

		ba.bot.SendSafeChat(fmt.Sprintf("Mulai membangun! Total ada %d blok.", len(blocksToPlace)))
		success := ba.executeBlockList(buildCtx, blocksToPlace, bx, bz)

		// 10. Post-Build Cleanup and notification
		ba.placer.CleanupScaffolds(buildCtx)

		if success {
			ba.bot.SendSafeChat("Hore! Rumah kamu udah selesai aku bangun. Silakan dilihat!")
		} else {
			ba.bot.SendSafeChat("Maaf ya, pembangunannya kepotong atau ada masalah di jalan.")
		}
	}()
}

// executeBlockList runs through the sorted block entries, placing each.
func (ba *BuilderAgent) executeBlockList(ctx context.Context, blocks []BlockEntry, cx, cz int) bool {
	ba.logger.Info("Starting execution of block list layout", "total", len(blocks))

	var failedList []BlockEntry

	for idx, entry := range blocks {
		select {
		case <-ctx.Done():
			ba.logger.Warn("Block placement loop cancelled by context")
			return false
		default:
		}

		ba.mu.Lock()
		ba.blocksPlaced = idx + 1
		ba.status = fmt.Sprintf("Building (%d/%d)", ba.blocksPlaced, ba.totalBlocks)
		ba.mu.Unlock()

		ok := ba.placer.PlaceBlockAt(ctx, entry.X, entry.Y, entry.Z, entry.Block, cx, cz, entry.Metadata)
		if ok {
			ba.mu.Lock()
			ba.placedHistory = append(ba.placedHistory, entry)
			ba.mu.Unlock()
		} else {
			ba.logger.Warn("Failed to place block, queued for retry pass", "block", entry.Block, "x", entry.X, "y", entry.Y, "z", entry.Z)
			failedList = append(failedList, entry)
		}

		if (idx+1)%15 == 0 {
			ba.bot.SendSafeChat(fmt.Sprintf("Progress pembangunan: %d%% (%d/%d)", int(float32(idx+1)/float32(len(blocks))*100), idx+1, len(blocks)))
		}
	}

	// Retry failed blocks once
	if len(failedList) > 0 {
		ba.logger.Info("Starting retry pass for failed blocks", "count", len(failedList))
		for _, entry := range failedList {
			select {
			case <-ctx.Done():
				return false
			default:
			}
			ok := ba.placer.PlaceBlockAt(ctx, entry.X, entry.Y, entry.Z, entry.Block, cx, cz, entry.Metadata)
			if ok {
				ba.mu.Lock()
				ba.placedHistory = append(ba.placedHistory, entry)
				ba.mu.Unlock()
			}
		}
	}

	return true
}

// UndoBuild removes the last N blocks placed by the bot.
func (ba *BuilderAgent) UndoBuild(ctx context.Context, count int) {
	ba.mu.Lock()
	if ba.isBuilding {
		ba.mu.Unlock()
		ba.bot.SendSafeChat("Aku lagi sibuk membangun. Hentikan dulu pake 'stopbuild' sebelum undo!")
		return
	}

	if len(ba.placedHistory) == 0 {
		ba.mu.Unlock()
		ba.bot.SendSafeChat("Belum ada blok yang aku tempatkan untuk dibatalkan.")
		return
	}

	if count <= 0 || count > len(ba.placedHistory) {
		count = len(ba.placedHistory)
	}

	ba.isBuilding = true
	ba.status = "Undoing..."
	ba.mu.Unlock()

	go func() {
		defer func() {
			ba.mu.Lock()
			ba.isBuilding = false
			ba.status = "Ready"
			ba.mu.Unlock()
		}()

		ba.bot.SendSafeChat(fmt.Sprintf("Membatalkan %d blok terakhir...", count))

		for i := 0; i < count; i++ {
			select {
			case <-ctx.Done():
				return
			default:
			}

			ba.mu.Lock()
			idx := len(ba.placedHistory) - 1
			entry := ba.placedHistory[idx]
			ba.placedHistory = ba.placedHistory[:idx]
			ba.mu.Unlock()

			// Navigate if far
			botPos := ba.bot.GetCoords()
			dx := float32(entry.X) + 0.5 - botPos.X()
			dy := float32(entry.Y) + 0.5 - botPos.Y()
			dz := float32(entry.Z) + 0.5 - botPos.Z()
			dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

			if dist > 4.5 {
				ba.bot.NavigateToBlock(int32(entry.X), int32(entry.Y), int32(entry.Z), 3.0)
				time.Sleep(300 * time.Millisecond)
			}

			// Dig/Undo placement
			pos := protocol.BlockPos{int32(entry.X), int32(entry.Y), int32(entry.Z)}
			ba.bot.LookAt(mgl32.Vec3{float32(entry.X) + 0.5, float32(entry.Y) + 0.5, float32(entry.Z) + 0.5})
			time.Sleep(100 * time.Millisecond)

			_ = ba.bot.WritePacket(&packet.Animate{
				ActionType:      packet.AnimateActionSwingArm,
				EntityRuntimeID: ba.bot.GetEntityRuntimeID(),
			})
			_ = ba.bot.WritePacket(&packet.PlayerAction{
				EntityRuntimeID: ba.bot.GetEntityRuntimeID(),
				ActionType:      protocol.PlayerActionStartBreak,
				BlockPosition:   pos,
				BlockFace:       1,
			})
			time.Sleep(300 * time.Millisecond)
			_ = ba.bot.WritePacket(&packet.PlayerAction{
				EntityRuntimeID: ba.bot.GetEntityRuntimeID(),
				ActionType:      protocol.PlayerActionCrackBreak,
				BlockPosition:   pos,
				BlockFace:       1,
			})
			_ = ba.bot.WritePacket(&packet.PlayerAction{
				EntityRuntimeID: ba.bot.GetEntityRuntimeID(),
				ActionType:      protocol.PlayerActionPredictDestroyBlock,
				BlockPosition:   pos,
				BlockFace:       1,
			})

			ba.bot.GetLocalWorldModel().SetSolid(int32(entry.X), int32(entry.Y), int32(entry.Z), false)
			time.Sleep(150 * time.Millisecond)
		}

		ba.bot.SendSafeChat("Undo selesai!")
	}()
}

// StopBuilding cancels the active build project.
func (ba *BuilderAgent) StopBuilding() {
	ba.mu.Lock()
	defer ba.mu.Unlock()

	if ba.isBuilding && ba.cancelFn != nil {
		ba.cancelFn()
		ba.isBuilding = false
		ba.status = "Ready"
		ba.bot.SendSafeChat("Pembangunan dihentikan!")
	} else {
		ba.bot.SendSafeChat("Aku nggak lagi bangun apa-apa kok.")
	}
}

// GetBuildStatus returns the current building progress report.
func (ba *BuilderAgent) GetBuildStatus() string {
	ba.mu.Lock()
	defer ba.mu.Unlock()
	return ba.status
}

func (ba *BuilderAgent) getMaterialsSummary(items []BuildItem) string {
	var summary []string
	counts := make(map[string]int)
	for _, it := range items {
		counts[it.Name] += it.Count
	}
	for name, count := range counts {
		summary = append(summary, fmt.Sprintf("%s:%d", name, count))
	}
	return strings.Join(summary, ", ")
}
