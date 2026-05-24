package building

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// GenerateMinimalist creates a 5x5x3 oak planks house schematic.
func GenerateMinimalist() []BlockEntry {
	var schematic []BlockEntry
	width := 5
	depth := 5
	height := 3

	// Floor
	for x := 0; x < width; x++ {
		for z := 0; z < depth; z++ {
			schematic = append(schematic, BlockEntry{X: x, Y: 0, Z: z, Block: "oak_planks"})
		}
	}

	// Walls
	for y := 1; y <= height; y++ {
		for x := 0; x < width; x++ {
			for z := 0; z < depth; z++ {
				if x == 0 || x == width-1 || z == 0 || z == depth-1 {
					schematic = append(schematic, BlockEntry{X: x, Y: y, Z: z, Block: "oak_planks"})
				}
			}
		}
	}

	// Ceiling/Roof
	for x := 0; x < width; x++ {
		for z := 0; z < depth; z++ {
			schematic = append(schematic, BlockEntry{X: x, Y: height, Z: z, Block: "oak_planks"})
		}
	}

	// Door opening and door block
	doorMeta := 3
	schematic = append(schematic, BlockEntry{X: 2, Y: 1, Z: 0, Block: "air"})
	schematic = append(schematic, BlockEntry{X: 2, Y: 2, Z: 0, Block: "air"})
	schematic = append(schematic, BlockEntry{X: 2, Y: 1, Z: 0, Block: "oak_door", Metadata: &doorMeta})

	// Window
	schematic = append(schematic, BlockEntry{X: 2, Y: 2, Z: depth - 1, Block: "glass"})

	return CleanSchematic(schematic)
}

// GenerateModern creates a 7x7x4 white concrete and stone bricks house schematic.
func GenerateModern() []BlockEntry {
	var schematic []BlockEntry
	width := 7
	depth := 7
	height := 4

	// Floor
	for x := 0; x < width; x++ {
		for z := 0; z < depth; z++ {
			schematic = append(schematic, BlockEntry{X: x, Y: 0, Z: z, Block: "stone_bricks"})
		}
	}

	// Walls
	for y := 1; y <= height; y++ {
		for x := 0; x < width; x++ {
			for z := 0; z < depth; z++ {
				if x == 0 || x == width-1 || z == 0 || z == depth-1 {
					schematic = append(schematic, BlockEntry{X: x, Y: y, Z: z, Block: "white_concrete"})
				}
			}
		}
	}

	// Flat roof overhang
	for x := -1; x <= width; x++ {
		for z := -1; z <= depth; z++ {
			schematic = append(schematic, BlockEntry{X: x, Y: height, Z: z, Block: "smooth_stone_slab"})
		}
	}

	// Door opening and door block
	doorMeta := 3
	schematic = append(schematic, BlockEntry{X: 3, Y: 1, Z: 0, Block: "air"})
	schematic = append(schematic, BlockEntry{X: 3, Y: 2, Z: 0, Block: "air"})
	schematic = append(schematic, BlockEntry{X: 3, Y: 1, Z: 0, Block: "dark_oak_door", Metadata: &doorMeta})

	// Windows
	schematic = append(schematic, BlockEntry{X: 1, Y: 2, Z: 0, Block: "glass_pane"})
	schematic = append(schematic, BlockEntry{X: 5, Y: 2, Z: 0, Block: "glass_pane"})

	return CleanSchematic(schematic)
}

// GenerateSuperModern creates a 10x10x5 quartz modern villa schematic.
func GenerateSuperModern() []BlockEntry {
	var schematic []BlockEntry
	width := 10
	depth := 10
	height := 5

	// Floor
	for x := 0; x < width; x++ {
		for z := 0; z < depth; z++ {
			schematic = append(schematic, BlockEntry{X: x, Y: 0, Z: z, Block: "quartz_block"})
		}
	}

	// Walls
	for y := 1; y <= height; y++ {
		for x := 0; x < width; x++ {
			for z := 0; z < depth; z++ {
				if x == 0 || x == width-1 || z == 0 || z == depth-1 {
					schematic = append(schematic, BlockEntry{X: x, Y: y, Z: z, Block: "white_concrete"})
				}
			}
		}
	}

	// Balcony overhang
	for x := 2; x < 8; x++ {
		schematic = append(schematic, BlockEntry{X: x, Y: 3, Z: -1, Block: "quartz_slab"})
		schematic = append(schematic, BlockEntry{X: x, Y: 4, Z: -1, Block: "glass_pane"}) // railing
	}

	// Large Windows
	for x := 2; x < 5; x++ {
		schematic = append(schematic, BlockEntry{X: x, Y: 2, Z: 0, Block: "glass"})
	}
	for x := 6; x < 9; x++ {
		schematic = append(schematic, BlockEntry{X: x, Y: 2, Z: 0, Block: "glass"})
	}

	// Roof
	for x := 0; x < width; x++ {
		for z := 0; z < depth; z++ {
			schematic = append(schematic, BlockEntry{X: x, Y: height, Z: z, Block: "quartz_block"})
		}
	}

	return CleanSchematic(schematic)
}

// CleanSchematic removes duplicate coordinates, preserving the last override, and filters out air.
func CleanSchematic(schematic []BlockEntry) []BlockEntry {
	m := make(map[string]BlockEntry)
	for _, b := range schematic {
		key := fmt.Sprintf("%d,%d,%d", b.X, b.Y, b.Z)
		m[key] = b
	}

	var cleaned []BlockEntry
	for _, b := range m {
		if b.Block != "air" {
			cleaned = append(cleaned, b)
		}
	}
	return cleaned
}

// SortSchematic orders the blocks horizontally and vertically for a natural build path.
func SortSchematic(schematic []BlockEntry) []BlockEntry {
	if len(schematic) == 0 {
		return []BlockEntry{}
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

	liquidBlocks := []BlockEntry{}
	solidBlocks := []BlockEntry{}

	for _, b := range schematic {
		if b.Block == "water" || b.Block == "lava" {
			liquidBlocks = append(liquidBlocks, b)
		} else {
			solidBlocks = append(solidBlocks, b)
		}
	}

	if buildMode == "aggressive" {
		aggressiveSort := func(blocks []BlockEntry) []BlockEntry {
			if len(blocks) == 0 {
				return []BlockEntry{}
			}
			var result []BlockEntry
			remaining := make(map[int]bool)
			for i := range blocks {
				remaining[i] = true
			}

			centerX := float64(minX+maxX) / 2
			centerZ := float64(minZ+maxZ) / 2

			// Find initial block: lowest Y, closest to center
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

		sorted := aggressiveSort(solidBlocks)
		sorted = append(sorted, aggressiveSort(liquidBlocks)...)
		return sorted
	}

	// Default Layer-based Sort (floor -> walls -> interior -> roof)
	var floorBlocks []BlockEntry
	var wallBlocks []BlockEntry
	var interiorBlocks []BlockEntry
	var roofBlocks []BlockEntry

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

	sortByLayerThenNearest := func(blocks []BlockEntry) []BlockEntry {
		if len(blocks) == 0 {
			return []BlockEntry{}
		}

		layers := make(map[int][]BlockEntry)
		for _, b := range blocks {
			layers[b.Y] = append(layers[b.Y], b)
		}

		// Collect and sort Y coordinates
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

		var result []BlockEntry
		var lastBlock *BlockEntry

		for _, y := range ys {
			layerBlocks := layers[y]
			unvisited := make(map[int]BlockEntry)
			for i, b := range layerBlocks {
				unvisited[i] = b
			}

			var current *BlockEntry

			if lastBlock != nil {
				// Start from block closest to last block in previous layer
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
				// Corner start
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
				var nearest *BlockEntry
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

	var finalSorted []BlockEntry
	finalSorted = append(finalSorted, sortByLayerThenNearest(floorBlocks)...)
	finalSorted = append(finalSorted, sortByLayerThenNearest(wallBlocks)...)
	finalSorted = append(finalSorted, sortByLayerThenNearest(interiorBlocks)...)
	finalSorted = append(finalSorted, sortByLayerThenNearest(roofBlocks)...)
	finalSorted = append(finalSorted, sortByLayerThenNearest(liquidBlocks)...)

	return finalSorted
}
