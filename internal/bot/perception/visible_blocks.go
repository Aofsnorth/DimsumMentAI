// Package perception summarises what the bot can actually see at the block
// level. It exists because the LLM's grounded context listed mobs and players
// but no blocks at all: asked "is there a button near you?" the model could
// only guess, and answered "no" while standing in front of one.
//
// Everything here is line-of-sight — a ray is walked from the bot's eyes to
// each block, and anything behind a loaded solid cell is dropped. That is the
// "not xray" rule: a button on the far side of a wall is never reported.
package perception

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/interact"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	// losStep is the distance between samples along a sight line. Coarse enough
	// that a 12m ray costs ~30 lookups, fine enough that a one-block wall
	// cannot be stepped over.
	losStep = 0.4

	// sameStructureDistance is how far apart two same-named clickable blocks
	// may sit and still count as one thing. A door is two cells tall; a double
	// chest is two cells wide. Listing "door, door" reads as two doors.
	sameStructureDistance = 2.0

	// scanBelow/scanAbove bound the vertical scan band around the bot's feet.
	scanBelow = int32(-3)
	scanAbove = int32(4)

	// maxTerrainEntries caps the terrain histogram so the prompt stays small.
	maxTerrainEntries = 6
)

// clickableBlock is one visible interactive block, ready to be rendered.
type clickableBlock struct {
	name string
	pos  protocol.BlockPos
	dist float32
	dir  string
}

// BlocksSummary describes the blocks the bot can see: clickable ones first
// (name, distance, compass direction), then a compact histogram of the terrain
// behind them. It returns "none" for a part with nothing visible, matching the
// mobs summary format the prompt already uses.
func BlocksSummary(b *bot.Bot, maxDistance float32, limit int) string {
	origin := b.GetCoords()
	eye := origin.Add(mgl32.Vec3{0, bot.PlayerEyeHeight, 0})
	radius := int32(math.Ceil(float64(maxDistance)))

	bx := int32(math.Floor(float64(origin.X())))
	by := int32(math.Floor(float64(origin.Y())))
	bz := int32(math.Floor(float64(origin.Z())))

	var clickables []clickableBlock
	terrain := make(map[string]int)

	for dx := -radius; dx <= radius; dx++ {
		for dy := scanBelow; dy <= scanAbove; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				pos := protocol.BlockPos{bx + dx, by + dy, bz + dz}
				name, ok := b.GetBlockName(pos.X(), pos.Y(), pos.Z())
				if !ok {
					continue
				}
				clean := cleanName(name)
				if clean == "" || clean == "air" {
					continue
				}
				center := mgl32.Vec3{
					float32(pos.X()) + 0.5,
					float32(pos.Y()) + 0.5,
					float32(pos.Z()) + 0.5,
				}
				dist := center.Sub(eye).Len()
				if dist > maxDistance {
					continue
				}
				if !hasLineOfSight(b, eye, center, pos) {
					continue
				}

				if interact.IsInteractiveBlockName(name) {
					clickables = append(clickables, clickableBlock{
						name: clean,
						pos:  pos,
						dist: dist,
						dir:  compassDirection(eye, center),
					})
				} else {
					terrain[clean]++
				}
			}
		}
	}

	sort.Slice(clickables, func(i, j int) bool { return clickables[i].dist < clickables[j].dist })
	rendered := make([]clickableBlock, 0, limit)
	for _, c := range clickables {
		if alreadyListedStructure(c, rendered) {
			continue
		}
		rendered = append(rendered, c)
		if len(rendered) >= limit {
			break
		}
	}
	parts := make([]string, 0, len(rendered))
	for _, c := range rendered {
		parts = append(parts, fmt.Sprintf("%s (%.0fm %s)", c.name, c.dist, c.dir))
	}

	clickableText := "none"
	if len(parts) > 0 {
		clickableText = strings.Join(parts, ", ")
	}

	return "Clickable: " + clickableText + ". Terrain: " + terrainText(terrain)
}

// alreadyListedStructure reports whether a same-named clickable within
// sameStructureDistance was already rendered, so multi-cell structures (doors,
// double chests) appear once.
func alreadyListedStructure(c clickableBlock, rendered []clickableBlock) bool {
	for _, r := range rendered {
		if r.name != c.name {
			continue
		}
		dx := float64(r.pos.X() - c.pos.X())
		dy := float64(r.pos.Y() - c.pos.Y())
		dz := float64(r.pos.Z() - c.pos.Z())
		if math.Sqrt(dx*dx+dy*dy+dz*dz) <= sameStructureDistance {
			return true
		}
	}
	return false
}

// terrainText renders the block histogram, most common first and capped, or
// "none" when every visible cell was clickable.
func terrainText(terrain map[string]int) string {
	if len(terrain) == 0 {
		return "none"
	}
	names := make([]string, 0, len(terrain))
	for name := range terrain {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if terrain[names[i]] != terrain[names[j]] {
			return terrain[names[i]] > terrain[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > maxTerrainEntries {
		names = names[:maxTerrainEntries]
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s(%d)", name, terrain[name]))
	}
	return strings.Join(parts, " ")
}

// SeesPoint reports whether the bot can actually see a world point, using the
// same line-of-sight walk as the block scan. Exported because the AGI vision
// reflex needs the identical rule: a player behind a wall is not visible no
// matter how close they are.
func SeesPoint(b *bot.Bot, to mgl32.Vec3) bool {
	origin := b.GetCoords()
	eye := origin.Add(mgl32.Vec3{0, bot.PlayerEyeHeight, 0})
	delta := to.Sub(eye)
	length := delta.Len()
	if length < 0.001 {
		return true
	}
	steps := int(length/losStep) + 1
	for s := 1; s < steps; s++ {
		p := eye.Add(delta.Mul(float32(s) / float32(steps)))
		cell := protocol.BlockPos{
			int32(math.Floor(float64(p.X()))),
			int32(math.Floor(float64(p.Y()))),
			int32(math.Floor(float64(p.Z()))),
		}
		solid, loaded := b.WorldCache.IsBlockSolid(cell.X(), cell.Y(), cell.Z())
		if loaded && solid {
			return false
		}
	}
	return true
}

// hasLineOfSight walks the segment from the bot's eyes to a block centre and
// reports whether any loaded solid cell stands in the way. The target cell
// itself is never an occluder, and unloaded cells cannot block sight — the bot
// does not know what is in them, and claiming a wall would be inventing one.
func hasLineOfSight(b *bot.Bot, from, to mgl32.Vec3, target protocol.BlockPos) bool {
	delta := to.Sub(from)
	length := delta.Len()
	if length < 0.001 {
		return true
	}
	steps := int(length/losStep) + 1
	for s := 1; s < steps; s++ {
		p := from.Add(delta.Mul(float32(s) / float32(steps)))
		cell := protocol.BlockPos{
			int32(math.Floor(float64(p.X()))),
			int32(math.Floor(float64(p.Y()))),
			int32(math.Floor(float64(p.Z()))),
		}
		if cell == target {
			continue
		}
		solid, loaded := b.WorldCache.IsBlockSolid(cell.X(), cell.Y(), cell.Z())
		if loaded && solid {
			return false
		}
	}
	return true
}

// compassDirection names the horizontal direction from the bot to a point,
// Bedrock conventions: −Z north, +Z south, +X east, −X west.
func compassDirection(from, to mgl32.Vec3) string {
	dx := to.X() - from.X()
	dz := to.Z() - from.Z()
	if math.Abs(float64(dx)) > math.Abs(float64(dz)) {
		if dx > 0 {
			return "timur"
		}
		return "barat"
	}
	if dz > 0 {
		return "selatan"
	}
	return "utara"
}

// cleanName lowercases and strips any namespace ("minecraft:", "custom:",
// …) so the summary reads "stone_button", not "minecraft:stone_button".
func cleanName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if idx := strings.IndexByte(name, ':'); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}
