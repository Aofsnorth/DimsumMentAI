package gathering

import (
	"math"
	"strings"

	"bedrock-ai/internal/bot/durability"

	"github.com/go-gl/mathgl/mgl32"
)

func (bm *BlockMiner) resolveFuzzyName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.TrimPrefix(name, "minecraft:")
	fuzzyMap := map[string]string{
		"grass":  "grass_block",
		"stone":  "stone",
		"cobble": "cobblestone",
		"plank":  "oak_planks",
		"seeds":  "wheat_seeds",
		"seed":   "wheat_seeds",
		"wood":   "oak_log",
		"log":    "oak_log",
	}
	if resolved, ok := fuzzyMap[name]; ok {
		return resolved
	}
	return name
}

func blockNameMatches(blockName, requested string) bool {
	blockName = strings.ToLower(strings.TrimPrefix(blockName, "minecraft:"))
	requested = strings.ToLower(strings.TrimPrefix(requested, "minecraft:"))
	if blockName == requested {
		return true
	}
	if strings.Contains(blockName, requested) {
		return true
	}
	if requested == "oak_log" {
		return isLogBlockName(blockName)
	}
	return false
}

func (bm *BlockMiner) equipBestTool(resolvedName string) {
	requiredType := bm.requiredToolType(resolvedName)
	if requiredType == "" {
		_ = bm.rg.bot.UnequipItem()
		return
	}
	if !bm.equipToolByType(requiredType) {
		if err := bm.rg.bot.UnequipItem(); err != nil {
			// The hand keeps whatever it held — a hoe digging dirt is exactly
			// what a silent failure here looked like in the live log.
			bm.logger.Warn("equipBestTool: no tool found and unequip failed",
				"type", requiredType, "block", resolvedName, "err", err.Error())
		}
	}
}

// ToolTypeForBlock maps a block name to the tool class that mines it well.
// Exported so the mapping rule is testable without a live bot.
func ToolTypeForBlock(resolvedName string) string {
	if strings.Contains(resolvedName, "stone") || strings.Contains(resolvedName, "ore") ||
		strings.Contains(resolvedName, "brick") || strings.Contains(resolvedName, "cobble") {
		return "pickaxe"
	}
	if strings.Contains(resolvedName, "dirt") || strings.Contains(resolvedName, "sand") ||
		strings.Contains(resolvedName, "gravel") || strings.Contains(resolvedName, "clay") ||
		strings.Contains(resolvedName, "grass") {
		return "shovel"
	}
	if strings.Contains(resolvedName, "log") || strings.Contains(resolvedName, "wood") ||
		strings.Contains(resolvedName, "plank") {
		return "axe"
	}
	return ""
}

func (bm *BlockMiner) requiredToolType(resolvedName string) string {
	return ToolTypeForBlock(resolvedName)
}

func (bm *BlockMiner) equipToolByType(requiredType string) bool {
	bot := bm.rg.bot
	inv := bot.GetInventorySlots()
	names := bot.GetItemNames()

	priority := []string{
		"netherite_" + requiredType,
		"diamond_" + requiredType,
		"iron_" + requiredType,
		"stone_" + requiredType,
		"wooden_" + requiredType,
		"golden_" + requiredType,
	}

	for _, toolName := range priority {
		// Slots are walked in index order rather than map order, so two picks
		// of the same tier always resolve to the same one. A bot that alternates
		// between two identical picks looks like it is thinking.
		for slot := uint32(0); slot < 64; slot++ {
			item, held := inv[slot]
			if !held || item.Count <= 0 {
				continue
			}
			name := names[item.NetworkID]
			if !strings.Contains(strings.ToLower(name), toolName) {
				continue
			}
			// A tool that is nearly spent is skipped in favour of the next tier
			// down. Grinding the best pickaxe to breaking and then mining nothing
			// for the rest of the vein is the failure this exists to prevent —
			// and the gather still reports success, because nothing in the
			// pipeline knows the block never came out.
			if tracker, ok := any(bot).(interface{ DurabilityTracker() *durability.Tracker }); ok {
				if tracker.DurabilityTracker().ShouldReplace(slot, name) {
					continue
				}
			}
			_ = bot.EquipItem(slot)
			return true
		}
	}
	// Custom servers rename tools, so after the vanilla tiers fail, accept any
	// name shaped like the required tool at all. A bot holding only a renamed
	// shovel used to dig with whatever was in its hand instead.
	fallback := []string{requiredType}
	if requiredType == "shovel" {
		fallback = append(fallback, "sekop")
	}
	for _, toolName := range fallback {
		for slot := uint32(0); slot < 64; slot++ {
			item, held := inv[slot]
			if !held || item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			if strings.Contains(name, "hoe") || strings.Contains(name, "cangkul") {
				// A hoe is never an acceptable stand-in: it is the one tool that
				// looks tool-shaped in the log and digs worst.
				continue
			}
			if strings.Contains(name, toolName) {
				_ = bot.EquipItem(slot)
				return true
			}
		}
	}
	return false
}

func (bm *BlockMiner) distance(a mgl32.Vec3, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
