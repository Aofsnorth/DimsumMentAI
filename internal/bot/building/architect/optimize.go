package architect

import (
	"fmt"
	"strings"

	"bedrock-ai/internal/bot/building/common"
)

// OptimizeBuildingOrder runs Stage 3, organizing blueprint blocks logically.
func (ea *EnhancedAIArchitect) OptimizeBuildingOrder(blueprint []common.BlockEntry, concept *common.Concept) []common.BlockEntry {
	if len(blueprint) == 0 {
		return []common.BlockEntry{}
	}

	flow := strings.ToLower(concept.BuildingFlow)
	if flow == "layer" {
		var sorted []common.BlockEntry
		for _, b := range blueprint {
			sorted = append(sorted, b)
		}
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[i].Y > sorted[j].Y || (sorted[i].Y == sorted[j].Y && sorted[i].Z > sorted[j].Z) || (sorted[i].Y == sorted[j].Y && sorted[i].Z == sorted[j].Z && sorted[i].X > sorted[j].X) {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		return sorted
	}

	var floor []common.BlockEntry
	var walls []common.BlockEntry
	var interior []common.BlockEntry
	var roof []common.BlockEntry

	width := concept.Dimensions.X
	depth := concept.Dimensions.Z
	height := concept.Dimensions.Y

	for _, b := range blueprint {
		if b.Y == 0 {
			floor = append(floor, b)
		} else if b.Y == height {
			roof = append(roof, b)
		} else {
			isPerimeter := b.X == 0 || b.X == width-1 || b.Z == 0 || b.Z == depth-1
			if isPerimeter {
				walls = append(walls, b)
			} else {
				interior = append(interior, b)
			}
		}
	}

	for i := 0; i < len(floor); i++ {
		for j := i + 1; j < len(floor); j++ {
			if floor[i].Z > floor[j].Z || (floor[i].Z == floor[j].Z && floor[i].X > floor[j].X) {
				floor[i], floor[j] = floor[j], floor[i]
			}
		}
	}

	sortedWalls := ea.sortWallsLayers(walls, width, depth, height)

	for i := 0; i < len(interior); i++ {
		for j := i + 1; j < len(interior); j++ {
			if interior[i].Y > interior[j].Y || (interior[i].Y == interior[j].Y && interior[i].Z > interior[j].Z) {
				interior[i], interior[j] = interior[j], interior[i]
			}
		}
	}

	for i := 0; i < len(roof); i++ {
		for j := i + 1; j < len(roof); j++ {
			if roof[i].Z > roof[j].Z || (roof[i].Z == roof[j].Z && roof[i].X > roof[j].X) {
				roof[i], roof[j] = roof[j], roof[i]
			}
		}
	}

	var optimized []common.BlockEntry
	optimized = append(optimized, floor...)
	optimized = append(optimized, sortedWalls...)
	optimized = append(optimized, interior...)
	optimized = append(optimized, roof...)

	placed := make(map[string]bool)
	for _, b := range optimized {
		placed[fmt.Sprintf("%d,%d,%d", b.X, b.Y, b.Z)] = true
	}
	for _, b := range blueprint {
		key := fmt.Sprintf("%d,%d,%d", b.X, b.Y, b.Z)
		if !placed[key] {
			optimized = append(optimized, b)
		}
	}

	ea.logger.Info("Enhanced building order optimized", "count", len(optimized))
	return optimized
}

func (ea *EnhancedAIArchitect) sortWallsLayers(walls []common.BlockEntry, width, depth, height int) []common.BlockEntry {
	var sortedWalls []common.BlockEntry
	for y := 1; y < height; y++ {
		var front, left, back, right []common.BlockEntry
		for _, b := range walls {
			if b.Y == y {
				if b.Z == 0 {
					front = append(front, b)
				} else if b.X == 0 {
					left = append(left, b)
				} else if b.Z == depth-1 {
					back = append(back, b)
				} else if b.X == width-1 {
					right = append(right, b)
				}
			}
		}
		for i := 0; i < len(front); i++ {
			for j := i + 1; j < len(front); j++ {
				if front[i].X > front[j].X {
					front[i], front[j] = front[j], front[i]
				}
			}
		}
		for i := 0; i < len(left); i++ {
			for j := i + 1; j < len(left); j++ {
				if left[i].Z > left[j].Z {
					left[i], left[j] = left[j], left[i]
				}
			}
		}
		for i := 0; i < len(back); i++ {
			for j := i + 1; j < len(back); j++ {
				if back[i].X < back[j].X {
					back[i], back[j] = back[j], back[i]
				}
			}
		}
		for i := 0; i < len(right); i++ {
			for j := i + 1; j < len(right); j++ {
				if right[i].Z < right[j].Z {
					right[i], right[j] = right[j], right[i]
				}
			}
		}
		sortedWalls = append(sortedWalls, front...)
		sortedWalls = append(sortedWalls, left...)
		sortedWalls = append(sortedWalls, back...)
		sortedWalls = append(sortedWalls, right...)
	}
	return sortedWalls
}
