package scanner

import (
	"log/slog"
	"math"
	"strings"

	"bedrock-ai/internal/bot/building/common"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/safecast"
)

// AreaScanner provides functionality to find suitable building locations and level terrain.
type AreaScanner struct {
	bot              common.BotInterface
	logger           *slog.Logger
	placedStructures []common.StructureInfo
}

// NewAreaScanner creates a new AreaScanner instance.
func NewAreaScanner(bot common.BotInterface, logger *slog.Logger) *AreaScanner {
	return &AreaScanner{
		bot:    bot,
		logger: logger,
	}
}

// TrackStructure registers a structure placed by the bot.
func (s *AreaScanner) TrackStructure(name string, x, y, z int) {
	s.placedStructures = append(s.placedStructures, common.StructureInfo{
		Name: name,
		X:    x,
		Y:    y,
		Z:    z,
	})
}

// FindFlatArea finds a suitable flat area of the required size.
func (s *AreaScanner) FindFlatArea(cx, cy, cz, requiredSize int) (int, int, int) {
	if s.bot == nil {
		return cx + 3, cy, cz + 3
	}

	world := s.bot.GetLocalWorldModel()
	var bestSpot *common.StructureInfo
	bestScore := -1

	candidates := flatAreaCandidates(cx, cz)
	frontDx, frontDz := 5, 5

	for _, c := range candidates {
		groundY, found := s.findGroundY(c.x, cy, c.z, world)
		if !found {
			continue
		}

		score, total := s.scoreFlatArea(c.x, groundY, c.z, cx, cy, cz, requiredSize, world)
		if score > bestScore {
			bestScore = score
			bestSpot = &common.StructureInfo{X: c.x, Y: groundY, Z: c.z}
		}

		if score >= total-2 {
			break
		}
	}

	if bestSpot != nil && bestScore >= 15 {
		s.logger.Info("Found flat building area", "x", bestSpot.X, "y", bestSpot.Y, "z", bestSpot.Z, "score", bestScore)
		return bestSpot.X, bestSpot.Y, bestSpot.Z
	}

	s.logger.Warn("Could not find optimal flat area, using front default", "x", cx+frontDx, "y", cy, "z", cz+frontDz)
	return cx + frontDx, cy, cz + frontDz
}

func flatAreaCandidates(cx, cz int) []flatAreaCand {
	candidates := []flatAreaCand{{x: cx + 5, z: cz + 5}}
	for r := 4; r <= 24; r += 4 {
		for angle := 0.0; angle < math.Pi*2; angle += math.Pi / 4 {
			dx := int(math.Round(math.Cos(angle) * float64(r)))
			dz := int(math.Round(math.Sin(angle) * float64(r)))
			candidates = append(candidates, flatAreaCand{x: cx + dx, z: cz + dz})
		}
	}
	return candidates
}

func (s *AreaScanner) findGroundY(x, cy, z int, world entity.WorldModel) (int, bool) {
	for dy := 3; dy >= -5; dy-- {
		ty := cy + dy
		if world.IsSolid(safecast.To[int32](x), safecast.To[int32](ty), safecast.To[int32](z)) {
			return ty + 1, true
		}
	}
	return cy, false
}

func (s *AreaScanner) scoreFlatArea(x, groundY, z, cx, cy, cz, requiredSize int, world entity.WorldModel) (int, int) {
	flatCount := 0
	checkSize := requiredSize + 2
	total := (checkSize*2 + 1) * (checkSize*2 + 1)

	for dx := -checkSize; dx <= checkSize; dx++ {
		for dz := -checkSize; dz <= checkSize; dz++ {
			if s.isFlatSpot(safecast.To[int32](x+dx), groundY, safecast.To[int32](z+dz), world) {
				flatCount++
			}
		}
	}

	score := flatCount
	dist := math.Sqrt(float64((x-cx)*(x-cx) + (z-cz)*(z-cz)))
	if dist > 12 {
		score -= 5
	}
	yDiff := int(math.Abs(float64(groundY - cy)))
	score -= yDiff * 2
	return score, total
}

func (s *AreaScanner) isFlatSpot(x int32, groundY int, z int32, world entity.WorldModel) bool {
	return world.IsSolid(x, safecast.To[int32](groundY-1), z) &&
		!world.IsSolid(x, safecast.To[int32](groundY), z) &&
		!world.IsSolid(x, safecast.To[int32](groundY+1), z)
}

type flatAreaCand struct{ x, z int }

// ScanNearbyStructures returns list of key structure blocks placed by bot or nearby.
func (s *AreaScanner) ScanNearbyStructures() []common.StructureInfo {
	if s.bot == nil {
		return []common.StructureInfo{}
	}

	botPos := s.bot.GetCoords()
	var sorted []common.StructureInfo
	sorted = append(sorted, s.placedStructures...)

	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			di := math.Sqrt(float64((sorted[i].X-int(botPos.X()))*(sorted[i].X-int(botPos.X())) + (sorted[i].Z-int(botPos.Z()))*(sorted[i].Z-int(botPos.Z()))))
			dj := math.Sqrt(float64((sorted[j].X-int(botPos.X()))*(sorted[j].X-int(botPos.X())) + (sorted[j].Z-int(botPos.Z()))*(sorted[j].Z-int(botPos.Z()))))
			if dj < di {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	if len(sorted) > 10 {
		return sorted[:10]
	}
	return sorted
}

// FindTargetStructure matches structure request keywords to nearby scanned structures.
func (s *AreaScanner) FindTargetStructure(request string, nearby []common.StructureInfo) *common.StructureInfo {
	if len(nearby) == 0 {
		return nil
	}
	lower := strings.ToLower(request)

	keywords := map[string]string{
		"bed":          "bed",
		"kasur":        "bed",
		"tempat tidur": "bed",
		"chest":        "chest",
		"peti":         "chest",
		"furnace":      "furnace",
		"tungku":       "furnace",
		"crafting":     "crafting_table",
		"meja craft":   "crafting_table",
		"enchant":      "enchanting_table",
		"anvil":        "anvil",
		"beacon":       "beacon",
		"spawner":      "spawner",
		"brewing":      "brewing_stand",
	}

	for kw, targetName := range keywords {
		if strings.Contains(lower, kw) {
			for _, st := range nearby {
				if st.Name == targetName || strings.Contains(st.Name, targetName) {
					return &st
				}
			}
		}
	}
	return nil
}
