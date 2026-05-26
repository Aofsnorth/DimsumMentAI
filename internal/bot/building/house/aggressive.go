package house

import (
	"math"

	"bedrock-ai/internal/bot/building/common"
)

func sortAggressive(blocks []common.BlockEntry, minX, maxX, minY, minZ, maxZ int) []common.BlockEntry {
	if len(blocks) == 0 {
		return []common.BlockEntry{}
	}
	var result []common.BlockEntry
	remaining := make(map[int]bool)
	for i := range blocks {
		remaining[i] = true
	}

	centerX := float64(minX+maxX) / 2
	centerZ := float64(minZ+maxZ) / 2

	bestStart := 0
	bestSD := math.MaxFloat64
	for i, b := range blocks {
		d := math.Abs(float64(b.X)-centerX) + math.Abs(float64(b.Z)-centerZ) + float64(b.Y-minY)*3
		if d < bestSD {
			bestSD = d
			bestStart = i
		}
	}

	delete(remaining, bestStart)
	result = append(result, blocks[bestStart])
	curX, curY, curZ := blocks[bestStart].X, blocks[bestStart].Y, blocks[bestStart].Z

	for len(remaining) > 0 {
		nearest := -1
		nearestD := math.MaxFloat64

		for idx := range remaining {
			b := blocks[idx]
			dy := float64(b.Y - curY)
			yPenalty := dy * 2.0
			if dy < 0 {
				yPenalty = math.Abs(dy) * 0.5
			}
			d := math.Abs(float64(b.X-curX)) + math.Abs(float64(b.Z-curZ)) + yPenalty

			if d < nearestD {
				nearestD = d
				nearest = idx
			}
		}

		if nearest == -1 {
			break
		}
		delete(remaining, nearest)
		result = append(result, blocks[nearest])
		curX, curY, curZ = blocks[nearest].X, blocks[nearest].Y, blocks[nearest].Z
	}
	return result
}
