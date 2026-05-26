package acquisition

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"bedrock-ai/internal/bot/building/common"
	"bedrock-ai/internal/bot/building/scanner"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// InventoryAcquisition handles raw materials checking, emergency crafting, and chest stashing.
type InventoryAcquisition struct {
	bot      common.BotInterface
	logger   *slog.Logger
	chestPos protocol.BlockPos
	hasChest bool
	scanner  *scanner.AreaScanner
}

// NewInventoryAcquisition creates a new InventoryAcquisition instance.
func NewInventoryAcquisition(bot common.BotInterface, logger *slog.Logger, scanner *scanner.AreaScanner) *InventoryAcquisition {
	return &InventoryAcquisition{
		bot:     bot,
		logger:  logger,
		scanner: scanner,
	}
}

// CraftPlanksIfNeeded checks plank counts and processes logs into planks.
func (ia *InventoryAcquisition) CraftPlanksIfNeeded() {
	if ia.bot == nil {
		return
	}

	inv := ia.bot.GetInventorySlots()
	names := ia.bot.GetItemNames()

	plankNames := []string{"oak_planks", "spruce_planks", "birch_planks", "jungle_planks", "acacia_planks", "dark_oak_planks"}
	totalPlanks := 0

	for _, p := range plankNames {
		for _, stack := range inv {
			if stack.Count <= 0 {
				continue
			}
			name := strings.ReplaceAll(names[stack.NetworkID], "minecraft:", "")
			if name == p {
				totalPlanks += int(stack.Count)
			}
		}
	}

	if totalPlanks >= 64 {
		ia.logger.Info("Sufficient planks already in inventory", "count", totalPlanks)
		return
	}

	logNames := []string{"oak_log", "spruce_log", "birch_log", "jungle_log", "acacia_log", "dark_oak_log"}
	recipes := ia.bot.GetRecipes()

	for _, log := range logNames {
		for _, stack := range inv {
			if stack.Count <= 0 {
				continue
			}
			name := strings.ReplaceAll(names[stack.NetworkID], "minecraft:", "")
			if name == log {
				targetPlank := strings.ReplaceAll(log, "_log", "_planks")
				recipeID, ok := recipes[targetPlank]
				if !ok {
					recipeID, ok = recipes[strings.ToLower(targetPlank)]
				}

				if ok {
					ia.logger.Info("Crafting planks from logs", "log", log, "count", stack.Count)
					ia.bot.SendSafeChat(fmt.Sprintf("Aku buat plank dulu ya dari %s.", strings.ReplaceAll(log, "_", " ")))
					
					time.Sleep(200 * time.Millisecond)

					craftCount := int(stack.Count)
					if craftCount > 16 {
						craftCount = 16
					}

					_ = ia.bot.CraftItem(recipeID, craftCount)
					time.Sleep(600 * time.Millisecond)
					return
				}
			}
		}
	}
}

// CraftToolsIfNeeded ensures the bot has basic stone/wood tools for building and leveling.
func (ia *InventoryAcquisition) CraftToolsIfNeeded() {
	if ia.bot == nil {
		return
	}

	inv := ia.bot.GetInventorySlots()
	names := ia.bot.GetItemNames()

	hasAxe := false
	hasPickaxe := false

	for _, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		name := strings.ToLower(strings.ReplaceAll(names[stack.NetworkID], "minecraft:", ""))
		if strings.Contains(name, "axe") && !strings.Contains(name, "pickaxe") {
			hasAxe = true
		}
		if strings.Contains(name, "pickaxe") {
			hasPickaxe = true
		}
	}

	if hasAxe && hasPickaxe {
		return
	}

	planksCount := 0
	sticksCount := 0
	for _, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		name := strings.ReplaceAll(names[stack.NetworkID], "minecraft:", "")
		if strings.Contains(name, "planks") {
			planksCount += int(stack.Count)
		}
		if name == "stick" {
			sticksCount += int(stack.Count)
		}
	}

	recipes := ia.bot.GetRecipes()

	if sticksCount < 4 && planksCount >= 2 {
		recipeID, ok := recipes["stick"]
		if ok {
			ia.logger.Info("Crafting sticks for tool crafting")
			_ = ia.bot.CraftItem(recipeID, 1)
			time.Sleep(500 * time.Millisecond)
			sticksCount += 4
			planksCount -= 2
		}
	}

	if !hasAxe && planksCount >= 3 && sticksCount >= 2 {
		recipeID, ok := recipes["wooden_axe"]
		if ok {
			ia.logger.Info("Crafting wooden axe")
			ia.bot.SendSafeChat("Aku buat kapak kayu dulu.")
			_ = ia.bot.CraftItem(recipeID, 1)
			time.Sleep(500 * time.Millisecond)
			planksCount -= 3
			sticksCount -= 2
		}
	}

	if !hasPickaxe && planksCount >= 3 && sticksCount >= 2 {
		recipeID, ok := recipes["wooden_pickaxe"]
		if ok {
			ia.logger.Info("Crafting wooden pickaxe")
			_ = ia.bot.CraftItem(recipeID, 1)
			time.Sleep(500 * time.Millisecond)
		}
	}
}
