// Package architect provides building blueprint generation and optimization.
package architect

import (
	"fmt"
	"sort"
	"strings"

	"bedrock-ai/internal/bot/building/common"
)

// OptimizeBuildingOrder runs Stage 3, organizing blueprint blocks logically.
func (ea *EnhancedAIArchitect) OptimizeBuildingOrder(blueprint []common.BlockEntry, concept *common.Concept) []common.BlockEntry {
	if len(blueprint) == 0 {
		return []common.BlockEntry{}
	}

	if strings.EqualFold(concept.BuildingFlow, "layer") {
		sorted := append([]common.BlockEntry(nil), blueprint...)
		sortBlocks(sorted, func(a, b common.BlockEntry) bool {
			if a.Y != b.Y {
				return a.Y < b.Y
			}
			if a.Z != b.Z {
				return a.Z < b.Z
			}
			return a.X < b.X
		})
		return sorted
	}

	width := concept.Dimensions.X
	depth := concept.Dimensions.Z
	height := concept.Dimensions.Y

	floor, walls, interior, roof := categorizeBlocks(blueprint, width, depth, height)

	sortBlocks(floor, func(a, b common.BlockEntry) bool {
		if a.Z != b.Z {
			return a.Z < b.Z
		}
		return a.X < b.X
	})
	sortedWalls := ea.sortWallsLayers(walls, width, depth, height)
	sortBlocks(interior, func(a, b common.BlockEntry) bool {
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.Z < b.Z
	})
	sortBlocks(roof, func(a, b common.BlockEntry) bool {
		if a.Z != b.Z {
			return a.Z < b.Z
		}
		return a.X < b.X
	})

	optimized := append([]common.BlockEntry(nil), floor...)
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

// categorizeBlocks splits the blueprint into floor, walls, interior, and roof.
func categorizeBlocks(blueprint []common.BlockEntry, width, depth, height int) (floor, walls, interior, roof []common.BlockEntry) {
	for _, b := range blueprint {
		switch {
		case b.Y == 0:
			floor = append(floor, b)
		case b.Y == height:
			roof = append(roof, b)
		default:
			isPerimeter := b.X == 0 || b.X == width-1 || b.Z == 0 || b.Z == depth-1
			if isPerimeter {
				walls = append(walls, b)
			} else {
				interior = append(interior, b)
			}
		}
	}
	return
}

// sortBlocks sorts a slice of blocks in place using the supplied comparator.
func sortBlocks(blocks []common.BlockEntry, less func(a, b common.BlockEntry) bool) {
	sort.Slice(blocks, func(i, j int) bool { return less(blocks[i], blocks[j]) })
}

func (ea *EnhancedAIArchitect) sortWallsLayers(walls []common.BlockEntry, width, depth, height int) []common.BlockEntry {
	var sortedWalls []common.BlockEntry
	for y := 1; y < height; y++ {
		var front, left, back, right []common.BlockEntry
		for _, b := range walls {
			if b.Y != y {
				continue
			}
			switch {
			case b.Z == 0:
				front = append(front, b)
			case b.X == 0:
				left = append(left, b)
			case b.Z == depth-1:
				back = append(back, b)
			case b.X == width-1:
				right = append(right, b)
			}
		}

		sortBlocks(front, func(a, b common.BlockEntry) bool { return a.X < b.X })
		sortBlocks(left, func(a, b common.BlockEntry) bool { return a.Z < b.Z })
		sortBlocks(back, func(a, b common.BlockEntry) bool { return a.X > b.X })
		sortBlocks(right, func(a, b common.BlockEntry) bool { return a.Z > b.Z })

		sortedWalls = append(sortedWalls, front...)
		sortedWalls = append(sortedWalls, left...)
		sortedWalls = append(sortedWalls, back...)
		sortedWalls = append(sortedWalls, right...)
	}
	return sortedWalls
}
