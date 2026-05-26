package house

import (
	"math"
	"os"
	"strings"

	"bedrock-ai/internal/bot/building/common"
)

// SortSchematic orders the blocks horizontally and vertically for a natural build path.
func SortSchematic(schematic []common.BlockEntry) []common.BlockEntry {
	if len(schematic) == 0 {
		return []common.BlockEntry{}
	}

	buildMode := strings.ToLower(os.Getenv("AI_BUILD_MODE"))
	if buildMode == "" {
		buildMode = "aggressive"
	}

	minX, maxX := math.MaxInt, math.MinInt
	minY, maxY := math.MaxInt, math.MinInt
	minZ, maxZ := math.MaxInt, math.MinInt

	for _, b := range schematic {
		minX = min(minX, b.X)
		maxX = max(maxX, b.X)
		minY = min(minY, b.Y)
		maxY = max(maxY, b.Y)
		minZ = min(minZ, b.Z)
		maxZ = max(maxZ, b.Z)
	}

	liquidBlocks := []common.BlockEntry{}
	solidBlocks := []common.BlockEntry{}

	for _, b := range schematic {
		if b.Block == "water" || b.Block == "lava" {
			liquidBlocks = append(liquidBlocks, b)
		} else {
			solidBlocks = append(solidBlocks, b)
		}
	}

	if buildMode == "aggressive" {
		sorted := sortAggressive(solidBlocks, minX, maxX, minY, minZ, maxZ)
		sorted = append(sorted, sortAggressive(liquidBlocks, minX, maxX, minY, minZ, maxZ)...)
		return sorted
	}

	return sortLayered(solidBlocks, liquidBlocks, minX, maxX, minY, maxY, minZ, maxZ)
}
