package gathering

import (
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

func (bm *BlockMiner) resolveFuzzyName(name string) string {
	name = strings.ToLower(name)
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

func (bm *BlockMiner) equipBestTool(resolvedName string) {
	bot := bm.rg.bot
	inv := bot.GetInventorySlots()
	names := bot.GetItemNames()

	var requiredType string
	if strings.Contains(resolvedName, "stone") || strings.Contains(resolvedName, "ore") || 
		strings.Contains(resolvedName, "brick") || strings.Contains(resolvedName, "cobble") {
		requiredType = "pickaxe"
	} else if strings.Contains(resolvedName, "dirt") || strings.Contains(resolvedName, "sand") || 
		strings.Contains(resolvedName, "gravel") || strings.Contains(resolvedName, "clay") {
		requiredType = "shovel"
	} else if strings.Contains(resolvedName, "log") || strings.Contains(resolvedName, "wood") || 
		strings.Contains(resolvedName, "plank") {
		requiredType = "axe"
	}

	if requiredType == "" {
		_ = bot.UnequipItem()
		return
	}

	priority := []string{
		"netherite_" + requiredType,
		"diamond_" + requiredType,
		"iron_" + requiredType,
		"stone_" + requiredType,
		"wooden_" + requiredType,
		"golden_" + requiredType,
	}

	for _, toolName := range priority {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := names[item.NetworkID]
			if strings.Contains(strings.ToLower(name), toolName) {
				_ = bot.EquipItem(slot)
				return
			}
		}
	}

	_ = bot.UnequipItem()
}

func (bm *BlockMiner) distance(a mgl32.Vec3, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
