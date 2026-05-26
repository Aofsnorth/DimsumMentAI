package house

import (
	"fmt"

	"bedrock-ai/internal/bot/building/common"
)

// GenerateMinimalist creates a 5x5x3 oak planks house schematic.
func GenerateMinimalist() []common.BlockEntry {
	var schematic []common.BlockEntry
	width := 5
	depth := 5
	height := 3

	// Floor
	for x := 0; x < width; x++ {
		for z := 0; z < depth; z++ {
			schematic = append(schematic, common.BlockEntry{X: x, Y: 0, Z: z, Block: "oak_planks"})
		}
	}

	// Walls
	for y := 1; y <= height; y++ {
		for x := 0; x < width; x++ {
			for z := 0; z < depth; z++ {
				if x == 0 || x == width-1 || z == 0 || z == depth-1 {
					schematic = append(schematic, common.BlockEntry{X: x, Y: y, Z: z, Block: "oak_planks"})
				}
			}
		}
	}

	// Ceiling/Roof
	for x := 0; x < width; x++ {
		for z := 0; z < depth; z++ {
			schematic = append(schematic, common.BlockEntry{X: x, Y: height, Z: z, Block: "oak_planks"})
		}
	}

	// Door opening and door block
	doorMeta := 3
	schematic = append(schematic, common.BlockEntry{X: 2, Y: 1, Z: 0, Block: "air"})
	schematic = append(schematic, common.BlockEntry{X: 2, Y: 2, Z: 0, Block: "air"})
	schematic = append(schematic, common.BlockEntry{X: 2, Y: 1, Z: 0, Block: "oak_door", Metadata: &doorMeta})

	// Window
	schematic = append(schematic, common.BlockEntry{X: 2, Y: 2, Z: depth - 1, Block: "glass"})

	return CleanSchematic(schematic)
}

// GenerateModern creates a 7x7x4 white concrete and stone bricks house schematic.
func GenerateModern() []common.BlockEntry {
	var schematic []common.BlockEntry
	width := 7
	depth := 7
	height := 4

	// Floor
	for x := 0; x < width; x++ {
		for z := 0; z < depth; z++ {
			schematic = append(schematic, common.BlockEntry{X: x, Y: 0, Z: z, Block: "stone_bricks"})
		}
	}

	// Walls
	for y := 1; y <= height; y++ {
		for x := 0; x < width; x++ {
			for z := 0; z < depth; z++ {
				if x == 0 || x == width-1 || z == 0 || z == depth-1 {
					schematic = append(schematic, common.BlockEntry{X: x, Y: y, Z: z, Block: "white_concrete"})
				}
			}
		}
	}

	// Flat roof overhang
	for x := -1; x <= width; x++ {
		for z := -1; z <= depth; z++ {
			schematic = append(schematic, common.BlockEntry{X: x, Y: height, Z: z, Block: "smooth_stone_slab"})
		}
	}

	// Door opening and door block
	doorMeta := 3
	schematic = append(schematic, common.BlockEntry{X: 3, Y: 1, Z: 0, Block: "air"})
	schematic = append(schematic, common.BlockEntry{X: 3, Y: 2, Z: 0, Block: "air"})
	schematic = append(schematic, common.BlockEntry{X: 3, Y: 1, Z: 0, Block: "dark_oak_door", Metadata: &doorMeta})

	// Windows
	schematic = append(schematic, common.BlockEntry{X: 1, Y: 2, Z: 0, Block: "glass_pane"})
	schematic = append(schematic, common.BlockEntry{X: 5, Y: 2, Z: 0, Block: "glass_pane"})

	return CleanSchematic(schematic)
}

// GenerateSuperModern creates a 10x10x5 quartz modern villa schematic.
func GenerateSuperModern() []common.BlockEntry {
	var schematic []common.BlockEntry
	width := 10
	depth := 10
	height := 5

	// Floor
	for x := 0; x < width; x++ {
		for z := 0; z < depth; z++ {
			schematic = append(schematic, common.BlockEntry{X: x, Y: 0, Z: z, Block: "quartz_block"})
		}
	}

	// Walls
	for y := 1; y <= height; y++ {
		for x := 0; x < width; x++ {
			for z := 0; z < depth; z++ {
				if x == 0 || x == width-1 || z == 0 || z == depth-1 {
					schematic = append(schematic, common.BlockEntry{X: x, Y: y, Z: z, Block: "white_concrete"})
				}
			}
		}
	}

	// Balcony overhang
	for x := 2; x < 8; x++ {
		schematic = append(schematic, common.BlockEntry{X: x, Y: 3, Z: -1, Block: "quartz_slab"})
		schematic = append(schematic, common.BlockEntry{X: x, Y: 4, Z: -1, Block: "glass_pane"}) // railing
	}

	// Large Windows
	for x := 2; x < 5; x++ {
		schematic = append(schematic, common.BlockEntry{X: x, Y: 2, Z: 0, Block: "glass"})
	}
	for x := 6; x < 9; x++ {
		schematic = append(schematic, common.BlockEntry{X: x, Y: 2, Z: 0, Block: "glass"})
	}

	// Roof
	for x := 0; x < width; x++ {
		for z := 0; z < depth; z++ {
			schematic = append(schematic, common.BlockEntry{X: x, Y: height, Z: z, Block: "quartz_block"})
		}
	}

	return CleanSchematic(schematic)
}

// CleanSchematic removes duplicate coordinates, preserving the last override, and filters out air.
func CleanSchematic(schematic []common.BlockEntry) []common.BlockEntry {
	m := make(map[string]common.BlockEntry)
	for _, b := range schematic {
		key := fmt.Sprintf("%d,%d,%d", b.X, b.Y, b.Z)
		m[key] = b
	}

	var cleaned []common.BlockEntry
	for _, b := range m {
		if b.Block != "air" {
			cleaned = append(cleaned, b)
		}
	}
	return cleaned
}
