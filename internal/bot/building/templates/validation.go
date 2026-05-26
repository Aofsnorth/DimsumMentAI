package templates

import (
	"fmt"
	"math"

	"bedrock-ai/internal/bot/building/common"
)

// ValidateTemplate runs safety checks on structural connectivity and dimensions.
func (tl *TemplateLibrary) ValidateTemplate(tmpl *common.Template) common.ValidationResult {
	result := common.ValidationResult{Valid: true}

	if tmpl == nil {
		result.AddError("Template is nil")
		return result
	}

	if tmpl.StructureType == "" || tmpl.SizeCategory == "" {
		result.AddError("Template must have structureType and sizeCategory")
	}

	if len(tmpl.Blocks) == 0 {
		result.AddError("Template must have a blocks array")
		return result
	}

	coords := make(map[string]bool)
	minX, minY, minZ := math.MaxInt, math.MaxInt, math.MaxInt
	maxX, maxY, maxZ := math.MinInt, math.MinInt, math.MinInt

	for _, block := range tmpl.Blocks {
		minX = min(minX, block.X)
		minY = min(minY, block.Y)
		minZ = min(minZ, block.Z)

		maxX = max(maxX, block.X)
		maxY = max(maxY, block.Y)
		maxZ = max(maxZ, block.Z)

		if block.Type == "" {
			result.AddError(fmt.Sprintf("Block at (%d,%d,%d) has invalid empty type", block.X, block.Y, block.Z))
		}

		key := fmt.Sprintf("%d,%d,%d", block.X, block.Y, block.Z)
		if coords[key] {
			result.AddError(fmt.Sprintf("Duplicate block at coordinates: %s", key))
		}
		coords[key] = true
	}

	if tmpl.Dimensions.X > 0 {
		spanX := maxX - minX + 1
		spanY := maxY - minY + 1
		spanZ := maxZ - minZ + 1

		if spanX > tmpl.Dimensions.X || spanY > tmpl.Dimensions.Y || spanZ > tmpl.Dimensions.Z {
			result.AddError("Template blocks exceed declared dimensions")
		}
	}

	if !tl.isConnected(tmpl.Blocks) {
		result.AddWarning("Template contains disconnected blocks (floating blocks detected)")
	}

	return result
}

func (tl *TemplateLibrary) isConnected(blocks []common.TemplateBlock) bool {
	if len(blocks) <= 1 {
		return true
	}

	blockMap := make(map[string]bool)
	for _, b := range blocks {
		blockMap[fmt.Sprintf("%d,%d,%d", b.X, b.Y, b.Z)] = true
	}

	visited := make(map[string]bool)
	start := blocks[0]
	startKey := fmt.Sprintf("%d,%d,%d", start.X, start.Y, start.Z)
	queue := []string{startKey}
	visited[startKey] = true

	directions := [][]int{
		{1, 0, 0}, {-1, 0, 0},
		{0, 1, 0}, {0, -1, 0},
		{0, 0, 1}, {0, 0, -1},
	}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		var x, y, z int
		_, _ = fmt.Sscanf(curr, "%d,%d,%d", &x, &y, &z)

		for _, dir := range directions {
			nx := x + dir[0]
			ny := y + dir[1]
			nz := z + dir[2]
			nKey := fmt.Sprintf("%d,%d,%d", nx, ny, nz)

			if blockMap[nKey] && !visited[nKey] {
				visited[nKey] = true
				queue = append(queue, nKey)
			}
		}
	}

	return len(visited) == len(blockMap)
}
