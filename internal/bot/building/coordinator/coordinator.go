package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot/building/acquisition"
	"bedrock-ai/internal/bot/building/architect"
	"bedrock-ai/internal/bot/building/common"
	"bedrock-ai/internal/bot/building/placer"
	"bedrock-ai/internal/bot/building/planner"
	"bedrock-ai/internal/bot/building/scanner"
	"bedrock-ai/internal/bot/building/templates"
)

// BuilderAgent coordinates the high-level planning, scanning, materials preparation, and execution of build projects.
type BuilderAgent struct {
	bot         BotInterface
	logger      *slog.Logger
	aiClient    *ai.NvidiaClient
	
	// Sub-components
	library     *templates.TemplateLibrary
	executor    *templates.TemplateExecutor
	planner     *planner.AIPlanner
	architect   *architect.EnhancedAIArchitect
	scanner     *scanner.AreaScanner
	acquisition *acquisition.InventoryAcquisition
	placer      *placer.BlockPlacer

	// Build State
	mu                  sync.Mutex
	isBuilding          bool
	cancelFn            context.CancelFunc
	placedHistory       []common.BlockEntry
	status              string
	blocksPlaced        int
	totalBlocks         int
}

// NewBuilderAgent creates a new BuilderAgent orchestrator.
func NewBuilderAgent(bot BotInterface, logger *slog.Logger, aiClient *ai.NvidiaClient) *BuilderAgent {
	lib := templates.NewTemplateLibrary()
	_ = lib.LoadEmbeddedTemplates()

	scan := scanner.NewAreaScanner(bot, logger)
	acq := acquisition.NewInventoryAcquisition(bot, logger, scan)
	plac := placer.NewBlockPlacer(bot, logger)

	return &BuilderAgent{
		bot:         bot,
		logger:      logger,
		aiClient:    aiClient,
		library:     lib,
		executor:    templates.NewTemplateExecutor(lib, bot),
		planner:     planner.NewAIPlanner(aiClient, logger),
		architect:   architect.NewEnhancedAIArchitect(aiClient, logger),
		scanner:     scan,
		acquisition: acq,
		placer:      plac,
		status:      "Ready",
	}
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

func (ba *BuilderAgent) getMaterialsSummary(items []common.BuildItem) string {
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
