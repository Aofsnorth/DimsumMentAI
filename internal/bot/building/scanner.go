package building

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// StructureInfo represents a scanned structure block in the world.
type StructureInfo struct {
	Name string
	X    int
	Y    int
	Z    int
}

// AreaScanner provides functionality to find suitable building locations and level terrain.
type AreaScanner struct {
	bot    BotInterface
	logger *slog.Logger
	// Track structures placed by the bot (e.g. chest) to scan them later
	placedStructures []StructureInfo
}

// NewAreaScanner creates a new AreaScanner instance.
func NewAreaScanner(bot BotInterface, logger *slog.Logger) *AreaScanner {
	return &AreaScanner{
		bot:    bot,
		logger: logger,
	}
}

// TrackStructure registers a structure placed by the bot.
func (s *AreaScanner) TrackStructure(name string, x, y, z int) {
	s.placedStructures = append(s.placedStructures, StructureInfo{
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

	var bestSpot *StructureInfo
	bestScore := -1

	type cand struct{ x, z int }
	var candidates []cand

	// Radial candidates
	for r := 4; r <= 24; r += 4 {
		for angle := 0.0; angle < math.Pi*2; angle += math.Pi / 4 {
			dx := int(math.Round(math.Cos(angle) * float64(r)))
			dz := int(math.Round(math.Sin(angle) * float64(r)))
			candidates = append(candidates, cand{x: cx + dx, z: cz + dz})
		}
	}

	// Candidate directly in front of the bot
	// Bedrock yaw: 0 is South, 90 is West, 180 is North, 270 is East
	// We can estimate the yaw and place candidate in front
	// Let's assume bot's yaw is in degrees.
	// Since we don't have direct access to yaw from the interface, we can just use a default offset
	frontDx := 5
	frontDz := 5
	candidates = append([]cand{{x: cx + frontDx, z: cz + frontDz}}, candidates...)

	for _, c := range candidates {
		groundY := cy
		foundGround := false

		// Search from +3 down to -5 for ground level
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
		checkSize := requiredSize + 2 // footprint buffer
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

		// Distance penalty
		dist := math.Sqrt(float64((c.x-cx)*(c.x-cx) + (c.z-cz)*(c.z-cz)))
		if dist > 12 {
			score -= 5
		}

		// Y difference penalty
		yDiff := int(math.Abs(float64(groundY - cy)))
		score -= yDiff * 2

		// Score normalization check
		if score > bestScore {
			bestScore = score
			bestSpot = &StructureInfo{X: c.x, Y: groundY, Z: c.z}
		}

		// If it's a perfect flat spot, short-circuit
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
func (s *AreaScanner) ScanNearbyStructures() []StructureInfo {
	if s.bot == nil {
		return []StructureInfo{}
	}

	botPos := s.bot.GetCoords()

	// Sort tracked structures by distance to bot
	var sorted []StructureInfo
	for _, st := range s.placedStructures {
		sorted = append(sorted, st)
	}

	// In Bedrock, we only know structures the bot placed or that are in the immediate vicinity
	// we sort by horizontal distance
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
func (s *AreaScanner) FindTargetStructure(request string, nearby []StructureInfo) *StructureInfo {
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

// LevelArea clears obstructions and fills holes around the build spot.
func (s *AreaScanner) LevelArea(ctx context.Context, cx, cy, cz, requiredSize int) {
	if s.bot == nil {
		return
	}
	world := s.bot.GetLocalWorldModel()
	clearSize := requiredSize + 2

	var blocksToClear []protocol.BlockPos
	var blocksToFill []protocol.BlockPos

	for x := -clearSize; x <= clearSize; x++ {
		for z := -clearSize; z <= clearSize; z++ {
			tx := int32(cx + x)
			tz := int32(cz + z)

			// Fill holes
			if !world.IsSolid(tx, int32(cy-1), tz) {
				blocksToFill = append(blocksToFill, protocol.BlockPos{tx, int32(cy - 1), tz})
			}

			// Clear blocks above ground up to 4 meters
			for dy := 0; dy <= 4; dy++ {
				ty := int32(cy + dy)
				if world.IsSolid(tx, ty, tz) {
					blocksToClear = append(blocksToClear, protocol.BlockPos{tx, ty, tz})
				}
			}
		}
	}

	if len(blocksToClear) > 5 || len(blocksToFill) > 5 {
		s.logger.Info("Leveling terrain", "to_clear", len(blocksToClear), "to_fill", len(blocksToFill))
		s.bot.SendSafeChat("Aku ratain tanahnya dulu biar rumahnya muat.")

		// Break blocks from top to bottom
		for i := 0; i < len(blocksToClear); i++ {
			for j := i + 1; j < len(blocksToClear); j++ {
				if blocksToClear[j].Y() > blocksToClear[i].Y() {
					blocksToClear[i], blocksToClear[j] = blocksToClear[j], blocksToClear[i]
				}
			}
		}

		clearedCount := 0
		for _, b := range blocksToClear {
			select {
			case <-ctx.Done():
				return
			default:
			}

			curCoords := s.bot.GetCoords()
			dx := float32(b.X()) + 0.5 - curCoords.X()
			dy := float32(b.Y()) + 0.5 - curCoords.Y()
			dz := float32(b.Z()) + 0.5 - curCoords.Z()
			dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

			if dist > 4.5 {
				s.bot.NavigateToBlock(b.X(), b.Y(), b.Z(), 3.0)
				time.Sleep(300 * time.Millisecond)
			}

			// Face block
			targetCenter := mgl32.Vec3{float32(b.X()) + 0.5, float32(b.Y()) + 0.5, float32(b.Z()) + 0.5}
			s.bot.LookAt(targetCenter)
			time.Sleep(100 * time.Millisecond)

			// Dig
			_ = s.bot.WritePacket(&packet.Animate{
				ActionType:      packet.AnimateActionSwingArm,
				EntityRuntimeID: s.bot.GetEntityRuntimeID(),
			})
			_ = s.bot.WritePacket(&packet.PlayerAction{
				EntityRuntimeID: s.bot.GetEntityRuntimeID(),
				ActionType:      protocol.PlayerActionStartBreak,
				BlockPosition:   b,
				BlockFace:       1,
			})
			time.Sleep(500 * time.Millisecond)
			_ = s.bot.WritePacket(&packet.PlayerAction{
				EntityRuntimeID: s.bot.GetEntityRuntimeID(),
				ActionType:      protocol.PlayerActionCrackBreak,
				BlockPosition:   b,
				BlockFace:       1,
			})
			_ = s.bot.WritePacket(&packet.PlayerAction{
				EntityRuntimeID: s.bot.GetEntityRuntimeID(),
				ActionType:      protocol.PlayerActionPredictDestroyBlock,
				BlockPosition:   b,
				BlockFace:       1,
			})

			world.SetSolid(b.X(), b.Y(), b.Z(), false)
			clearedCount++
			if clearedCount%10 == 0 {
				time.Sleep(200 * time.Millisecond)
			}
		}

		// Fill holes with dirt or cobblestone
		if len(blocksToFill) > 0 {
			inv := s.bot.GetInventorySlots()
			names := s.bot.GetItemNames()

			fillSlot, found := FindItemInSlots(inv, names, "dirt")
			if !found {
				fillSlot, found = FindItemInSlots(inv, names, "cobblestone")
			}

			if found {
				_ = s.bot.EquipItem(fillSlot)
				time.Sleep(100 * time.Millisecond)

				for _, pos := range blocksToFill {
					select {
					case <-ctx.Done():
						return
					default:
					}

					curCoords := s.bot.GetCoords()
					dx := float32(pos.X()) + 0.5 - curCoords.X()
					dy := float32(pos.Y()) + 0.5 - curCoords.Y()
					dz := float32(pos.Z()) + 0.5 - curCoords.Z()
					dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

					if dist > 4.5 {
						s.bot.NavigateToBlock(pos.X(), pos.Y(), pos.Z(), 3.0)
						time.Sleep(300 * time.Millisecond)
					}

					// Look at block position
					targetCenter := mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5}
					s.bot.LookAt(targetCenter)
					time.Sleep(100 * time.Millisecond)

					// Try to place block against adjacent faces
					faces := []struct {
						dir  protocol.BlockPos
						face int32
					}{
						{protocol.BlockPos{0, -1, 0}, 1}, // Place against block below
						{protocol.BlockPos{1, 0, 0}, 4},  // East (click west face)
						{protocol.BlockPos{-1, 0, 0}, 5}, // West (click east face)
						{protocol.BlockPos{0, 0, 1}, 2},  // South (click north face)
						{protocol.BlockPos{0, 0, -1}, 3}, // North (click south face)
					}

					invSlots := s.bot.GetInventorySlots()
					itemStack := invSlots[fillSlot]

					for _, f := range faces {
						adjX := pos.X() + f.dir.X()
						adjY := pos.Y() + f.dir.Y()
						adjZ := pos.Z() + f.dir.Z()

						if world.IsSolid(adjX, adjY, adjZ) {
							tx := &packet.InventoryTransaction{
								TransactionData: &protocol.UseItemTransactionData{
									ActionType:      protocol.UseItemActionClickBlock,
									BlockPosition:   protocol.BlockPos{adjX, adjY, adjZ},
									BlockFace:       f.face,
									HotBarSlot:      int32(s.bot.GetHeldItemSlot()),
									HeldItem:        protocol.ItemInstance{Stack: itemStack},
									Position:        s.bot.GetCoords(),
									ClickedPosition: mgl32.Vec3{0.5, 0.5, 0.5},
								},
							}
							_ = s.bot.WritePacket(tx)
							world.SetSolid(pos.X(), pos.Y(), pos.Z(), true)
							break
						}
					}
					time.Sleep(150 * time.Millisecond)
				}
			}
		}
	}
}
