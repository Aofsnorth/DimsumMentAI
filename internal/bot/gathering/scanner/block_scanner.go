package scanner

import (
	"bedrock-ai/internal/bot/world"
	"math"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/go-gl/mathgl/mgl32"
)

// BlockScanner scans for blocks in the world cache.
type BlockScanner struct {
	worldCache *world.WorldCache
}

// NewBlockScanner creates a new BlockScanner.
func NewBlockScanner(cache *world.WorldCache) *BlockScanner {
	return &BlockScanner{worldCache: cache}
}

// FindNearest finds the nearest block matching blockName within radius.
// Spiral search from origin outward. Returns position and true if found.
func (bs *BlockScanner) FindNearest(origin mgl32.Vec3, blockName string, radius int) (cube.Pos, bool) {
	target := toLower(blockName)
	bestDist := float64(radius*radius + 1)
	var bestPos cube.Pos
	found := false

	ox, oy, oz := int32(math.Floor(float64(origin[0]))), int32(math.Floor(float64(origin[1]))), int32(math.Floor(float64(origin[2])))

	// Spiral outward from origin
	for r := 0; r <= radius; r++ {
		// Iterate shell at distance r
		for dx := -r; dx <= r; dx++ {
			for dy := -r; dy <= r; dy++ {
				for dz := -r; dz <= r; dz++ {
					// Only check shell boundary (at least one coord at ±r)
					if abs(dx) != r && abs(dy) != r && abs(dz) != r {
						continue
					}

					x, y, z := ox+int32(dx), oy+int32(dy), oz+int32(dz)

					// Y bounds
					if y < -64 || y > 319 {
						continue
					}

					// Sphere check
					distSq := dx*dx + dy*dy + dz*dz
					if distSq > radius*radius {
						continue
					}

					rid, ok := bs.worldCache.GetBlockRID(x, y, z)
					if !ok {
						continue
					}

					name, _, ok := chunk.RuntimeIDToState(rid)
					if !ok {
						continue
					}

					if matchesBlockName(name, target) {
						dist := float64(distSq)
						if dist < bestDist {
							bestDist = dist
							bestPos = cube.Pos{int(x), int(y), int(z)}
							found = true
						}
					}
				}
			}
		}

		// Early exit if found in this shell
		if found {
			return bestPos, true
		}
	}

	return bestPos, found
}

// FindAll finds all blocks matching blockName within radius, sorted by distance.
// Returns up to limit results.
func (bs *BlockScanner) FindAll(origin mgl32.Vec3, blockName string, radius int, limit int) []cube.Pos {
	target := toLower(blockName)
	type posWithDist struct {
		pos  cube.Pos
		dist float64
	}
	var results []posWithDist

	ox, oy, oz := int32(math.Floor(float64(origin[0]))), int32(math.Floor(float64(origin[1]))), int32(math.Floor(float64(origin[2])))

	for dx := -radius; dx <= radius; dx++ {
		for dy := -radius; dy <= radius; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				x, y, z := ox+int32(dx), oy+int32(dy), oz+int32(dz)

				// Y bounds
				if y < -64 || y > 319 {
					continue
				}

				// Sphere check
				distSq := dx*dx + dy*dy + dz*dz
				if distSq > radius*radius {
					continue
				}

				rid, ok := bs.worldCache.GetBlockRID(x, y, z)
				if !ok {
					continue
				}

				name, _, ok := chunk.RuntimeIDToState(rid)
				if !ok {
					continue
				}

				if matchesBlockName(name, target) {
					results = append(results, posWithDist{
						pos:  cube.Pos{int(x), int(y), int(z)},
						dist: float64(distSq),
					})
				}
			}
		}
	}

	// Sort by distance (insertion sort, simple and efficient for small lists)
	for i := 1; i < len(results); i++ {
		key := results[i]
		j := i - 1
		for j >= 0 && results[j].dist > key.dist {
			results[j+1] = results[j]
			j--
		}
		results[j+1] = key
	}

	// Apply limit
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	// Extract positions
	positions := make([]cube.Pos, len(results))
	for i, r := range results {
		positions[i] = r.pos
	}

	return positions
}

// matchesBlockName checks if name matches target (case-insensitive substring).
func matchesBlockName(name, target string) bool {
	nameLower := toLower(name)
	return contains(nameLower, target)
}

// toLower converts string to lowercase manually.
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		} else {
			b[i] = c
		}
	}
	return string(b)
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// abs returns absolute value of int.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
