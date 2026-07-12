package house

import (
	"math"
	"sort"

	"bedrock-ai/internal/bot/building/common"
)

func sortLayered(solidBlocks, liquidBlocks []common.BlockEntry, minX, maxX, minY, maxY, minZ, maxZ int) []common.BlockEntry {
	var floorBlocks []common.BlockEntry
	var wallBlocks []common.BlockEntry
	var interiorBlocks []common.BlockEntry
	var roofBlocks []common.BlockEntry

	for _, b := range solidBlocks {
		if b.Y == minY {
			floorBlocks = append(floorBlocks, b)
		} else if b.Y == maxY && maxY > minY {
			roofBlocks = append(roofBlocks, b)
		} else {
			isPerimeter := b.X == minX || b.X == maxX || b.Z == minZ || b.Z == maxZ
			if isPerimeter {
				wallBlocks = append(wallBlocks, b)
			} else {
				interiorBlocks = append(interiorBlocks, b)
			}
		}
	}

	var finalSorted []common.BlockEntry
	finalSorted = append(finalSorted, sortByLayerThenNearest(floorBlocks)...)
	finalSorted = append(finalSorted, sortByLayerThenNearest(wallBlocks)...)
	finalSorted = append(finalSorted, sortByLayerThenNearest(interiorBlocks)...)
	finalSorted = append(finalSorted, sortByLayerThenNearest(roofBlocks)...)
	finalSorted = append(finalSorted, sortByLayerThenNearest(liquidBlocks)...)

	return finalSorted
}

func sortByLayerThenNearest(blocks []common.BlockEntry) []common.BlockEntry {
	if len(blocks) == 0 {
		return []common.BlockEntry{}
	}

	layers := groupByLayer(blocks)
	ys := sortedLayerKeys(layers)

	result := make([]common.BlockEntry, 0, len(blocks))
	var lastBlock *common.BlockEntry
	for _, y := range ys {
		layer := orderLayerNearest(layers[y], lastBlock)
		result = append(result, layer...)
		lastBlock = &layer[len(layer)-1]
	}
	return result
}

func groupByLayer(blocks []common.BlockEntry) map[int][]common.BlockEntry {
	layers := make(map[int][]common.BlockEntry)
	for _, b := range blocks {
		layers[b.Y] = append(layers[b.Y], b)
	}
	return layers
}

func sortedLayerKeys(layers map[int][]common.BlockEntry) []int {
	ys := make([]int, 0, len(layers))
	for y := range layers {
		ys = append(ys, y)
	}
	sort.Ints(ys)
	return ys
}

func orderLayerNearest(blocks []common.BlockEntry, startFrom *common.BlockEntry) []common.BlockEntry {
	unvisited := make(map[int]common.BlockEntry, len(blocks))
	for i, b := range blocks {
		unvisited[i] = b
	}

	current := pickStartBlock(unvisited, startFrom)
	if current == nil {
		return nil
	}

	result := make([]common.BlockEntry, 0, len(blocks))
	for {
		result = append(result, *current)
		delete(unvisited, currentIndexFor(unvisited, *current))
		if len(unvisited) == 0 {
			break
		}
		current = findNearestBlock(unvisited, current)
	}
	return result
}

func pickStartBlock(unvisited map[int]common.BlockEntry, startFrom *common.BlockEntry) *common.BlockEntry {
	if len(unvisited) == 0 {
		return nil
	}
	if startFrom != nil {
		idx, _ := findNearest(unvisited, startFrom.X, startFrom.Z)
		b := unvisited[idx]
		return &b
	}
	bestIdx := 0
	for idx, b := range unvisited {
		best := unvisited[bestIdx]
		if b.X < best.X || (b.X == best.X && b.Z < best.Z) {
			bestIdx = idx
		}
	}
	b := unvisited[bestIdx]
	return &b
}

func currentIndexFor(unvisited map[int]common.BlockEntry, current common.BlockEntry) int {
	for idx, b := range unvisited {
		if b == current {
			return idx
		}
	}
	return -1
}

func findNearestBlock(unvisited map[int]common.BlockEntry, current *common.BlockEntry) *common.BlockEntry {
	idx, _ := findNearest(unvisited, current.X, current.Z)
	b := unvisited[idx]
	return &b
}

func findNearest(unvisited map[int]common.BlockEntry, x, z int) (int, common.BlockEntry) {
	minDist := math.MaxFloat64
	var bestIdx int
	var best common.BlockEntry
	for idx, b := range unvisited {
		dx := b.X - x
		dz := b.Z - z
		dist := float64(dx*dx + dz*dz)
		if dist < minDist {
			minDist = dist
			bestIdx = idx
			best = b
		}
	}
	return bestIdx, best
}
