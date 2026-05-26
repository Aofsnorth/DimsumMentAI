package scanner

import (
	"log/slog"
	"math"
	"strings"

	"bedrock-ai/internal/bot/building/common"
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

	type cand struct{ x, z int }
	var candidates []cand

	for r := 4; r <= 24; r += 4 {
		for angle := 0.0; angle < math.Pi*2; angle += math.Pi / 4 {
			dx := int(math.Round(math.Cos(angle) * float64(r)))
			dz := int(math.Round(math.Sin(angle) * float64(r)))
			candidates = append(candidates, cand{x: cx + dx, z: cz + dz})
		}
	}

	frontDx := 5
	frontDz := 5
	candidates = append([]cand{{x: cx + frontDx, z: cz + frontDz}}, candidates...)

	for _, c := range candidates {
		groundY := cy
		foundGround := false

		for dy := 3; dy >= -5; dy-- {
			ty := cy + dy
			if world.IsSolid(int32(c.x), int32(ty), int32(c.z)) {
				groundY = ty + 1
				foundGround = true
				break
			}
		}

		if !foundGround {
			continue
		}

		flatCount := 0
		checkSize := requiredSize + 2
		totalChecks := (checkSize*2 + 1) * (checkSize*2 + 1)

		for x := -checkSize; x <= checkSize; x++ {
			for z := -checkSize; z <= checkSize; z++ {
				tx := int32(c.x + x)
				tz := int32(c.z + z)

				isGroundSolid := world.IsSolid(tx, int32(groundY-1), tz)
				isAbove1Empty := !world.IsSolid(tx, int32(groundY), tz)
				isAbove2Empty := !world.IsSolid(tx, int32(groundY+1), tz)

				if isGroundSolid && isAbove1Empty && isAbove2Empty {
					flatCount++
				}
			}
		}

		score := flatCount
		dist := math.Sqrt(float64((c.x-cx)*(c.x-cx) + (c.z-cz)*(c.z-cz)))
		if dist > 12 {
			score -= 5
		}

		yDiff := int(math.Abs(float64(groundY - cy)))
		score -= yDiff * 2

		if score > bestScore {
			bestScore = score
			bestSpot = &common.StructureInfo{X: c.x, Y: groundY, Z: c.z}
		}

		if score >= totalChecks-2 {
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

// ScanNearbyStructures returns list of key structure blocks placed by bot or nearby.
func (s *AreaScanner) ScanNearbyStructures() []common.StructureInfo {
	if s.bot == nil {
		return []common.StructureInfo{}
	}

	botPos := s.bot.GetCoords()
	var sorted []common.StructureInfo
	for _, st := range s.placedStructures {
		sorted = append(sorted, st)
	}

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
		"bed":            "bed",
		"kasur":          "bed",
		"tempat tidur":   "bed",
		"chest":          "chest",
		"peti":           "chest",
		"furnace":        "furnace",
		"tungku":         "furnace",
		"crafting":       "crafting_table",
		"meja craft":     "crafting_table",
		"enchant":        "enchanting_table",
		"anvil":          "anvil",
		"beacon":         "beacon",
		"spawner":        "spawner",
		"brewing":        "brewing_stand",
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
