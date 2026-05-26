package house

import (
	"math"

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

	layers := make(map[int][]common.BlockEntry)
	for _, b := range blocks {
		layers[b.Y] = append(layers[b.Y], b)
	}

	var ys []int
	for y := range layers {
		ys = append(ys, y)
	}
	for i := 0; i < len(ys); i++ {
		for j := i + 1; j < len(ys); j++ {
			if ys[i] > ys[j] {
				ys[i], ys[j] = ys[j], ys[i]
			}
		}
	}

	var result []common.BlockEntry
	var lastBlock *common.BlockEntry

	for _, y := range ys {
		layerBlocks := layers[y]
		unvisited := make(map[int]common.BlockEntry)
		for i, b := range layerBlocks {
			unvisited[i] = b
		}

		var current *common.BlockEntry

		if lastBlock != nil {
			minDist := math.MaxFloat64
			var bestIdx int
			for idx, b := range unvisited {
				dx := b.X - lastBlock.X
				dz := b.Z - lastBlock.Z
				dist := float64(dx*dx + dz*dz)
				if dist < minDist {
					minDist = dist
					temp := b
					current = &temp
					bestIdx = idx
				}
			}
			delete(unvisited, bestIdx)
		} else {
			bestIdx := 0
			for idx, b := range unvisited {
				if b.X < unvisited[bestIdx].X || (b.X == unvisited[bestIdx].X && b.Z < unvisited[bestIdx].Z) {
					bestIdx = idx
				}
			}
			temp := unvisited[bestIdx]
			current = &temp
			delete(unvisited, bestIdx)
		}

		result = append(result, *current)

		for len(unvisited) > 0 {
			var nearest *common.BlockEntry
			minDist := math.MaxFloat64
			var bestIdx int

			for idx, candidate := range unvisited {
				dx := candidate.X - current.X
				dz := candidate.Z - current.Z
				dist := float64(dx*dx + dz*dz)
				if dist < minDist {
					minDist = dist
					temp := candidate
					nearest = &temp
					bestIdx = idx
				}
			}

			current = nearest
			result = append(result, *current)
			delete(unvisited, bestIdx)
		}

		lastBlock = current
	}
	return result
}
