// Package perception summarises what the bot can actually see at the block
// level. It exists because the LLM's grounded context listed mobs and players
// but no blocks at all: asked "is there a button near you?" the model could
// only guess, and answered "no" while standing in front of one.
//
// Two rules govern everything here, and both are about honesty rather than
// capability:
//
//   - LINE OF SIGHT. A ray is walked from the bot's eyes to each block, and
//     anything behind a loaded solid cell is dropped. That is the "not xray"
//     rule: a button on the far side of a wall is never reported.
//   - FIELD OF VIEW. A block outside the vision cone is not reported either.
//     Line of sight alone still describes a sphere, which means the bot "saw"
//     the tree behind its head — a claim no player could make. See
//     field_of_view.go.
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

// blockScan is the one pass over what the bot can see, shared by every consumer
// so the "not xray" rule has exactly one implementation. The prose summary and
// the plain name list were separate scans once, and they drifted: the summary
// renders prose and the brain wants names, so a name list is derived here rather
// than re-parsed out of a sentence.
type blockScan struct {
	clickables []clickableBlock
	terrain    map[string]int
	// order preserves first-sight order, which is the order a human would list
	// things in and a far more readable prompt than alphabetical noise.
	terrainOrder []string
}

// scanBlocks walks the volume around the bot and returns what is genuinely
// visible: inside the vision cone, within range, and with a clear line of
// sight. Nothing here trusts the chunk cache to mean the bot can see a cell.
func scanBlocks(b *bot.Bot, maxDistance float32) blockScan {
	origin := b.GetCoords()
	eye := origin.Add(mgl32.Vec3{0, bot.PlayerEyeHeight, 0})
	radius := int32(math.Ceil(float64(maxDistance)))

	bx := int32(math.Floor(float64(origin.X())))
	by := int32(math.Floor(float64(origin.Y())))
	bz := int32(math.Floor(float64(origin.Z())))

	scan := blockScan{terrain: make(map[string]int)}
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
				// Vision cone, before the LOS walk. Doing it in this order means
				// the expensive ray is only ever cast at blocks the bot could
				// plausibly be looking at, and — more importantly — the summary
				// can no longer report a tree behind the player as "nearby".
				if !InFieldOfView(b, center) {
					continue
				}
				if !hasLineOfSight(b, eye, center, pos) {
					continue
				}

				if interact.IsInteractiveBlockName(name) {
					scan.clickables = append(scan.clickables, clickableBlock{
						name: clean,
						pos:  pos,
						dist: dist,
						dir:  compassDirection(eye, center),
					})
					continue
				}
				if _, seen := scan.terrain[clean]; !seen {
					scan.terrainOrder = append(scan.terrainOrder, clean)
				}
				scan.terrain[clean]++
			}
		}
	}
	return scan
}

// BlocksSummary describes the blocks the bot can see: clickable ones first
// (name, distance, compass direction), then a compact histogram of the terrain
// behind them. It returns "none" for a part with nothing visible, matching the
// mobs summary format the prompt already uses.
//
// This is the PROSE form, for a prompt a human or a language model reads. Code
// that needs the block names — anything that counts them, matches on them, or
// feeds them to a decision — must use VisibleBlockNames instead. Parsing names
// back out of this sentence is how the brain ended up convinced it was standing
// on a one-block world: the text has prose around a space-separated list, and a
// comma split returns the whole sentence as one block name.
func BlocksSummary(b *bot.Bot, maxDistance float32, limit int) string {
	return renderSummary(scanBlocks(b, maxDistance), limit)
}

// renderSummary is the prose half of a scan, shared by BlocksSummary and
// VisibleBlocks.
func renderSummary(scan blockScan, limit int) string {
	rendered := renderClickables(scan.clickables, limit)

	parts := make([]string, 0, len(rendered))
	for _, c := range rendered {
		parts = append(parts, fmt.Sprintf("%s (%.0fm %s)", c.name, c.dist, c.dir))
	}

	clickableText := "none"
	if len(parts) > 0 {
		clickableText = strings.Join(parts, ", ")
	}

	return "Clickable: " + clickableText + ". Terrain: " + terrainText(scan.terrain)
}

// VisibleBlockNames returns the distinct names of every block the bot can
// genuinely see, most-seen first, comma-joined.
//
// This is the form every decision in the brain reads. The same visibility rules
// as BlocksSummary apply — vision cone, range, and a walked line of sight — so
// nothing behind a wall is ever named here. What differs is the shape: names,
// not a sentence. DetectOneBlock counts them, the curriculum gates on them, and
// the vocabulary records them, and all three need to be able to tell one block
// from another.
//
// "none" when nothing is visible, which is both the honest answer in an empty
// plain and the signature of a single-block world.
func VisibleBlockNames(b *bot.Bot, maxDistance float32, limit int) string {
	scan := scanBlocks(b, maxDistance)
	return visibleBlockNames(scan, limit)
}

// VisibleBlocks returns both renderings of a single scan: the comma-separated
// names the brain's rules read, and the prose the models read.
//
// One scan, two shapes. Calling the two entry points separately would walk the
// volume twice per tick for no gain, and — worse — would let the name list and
// the prompt describe different worlds whenever the world changed in between.
func VisibleBlocks(b *bot.Bot, maxDistance float32, limit int) (names, text string) {
	scan := scanBlocks(b, maxDistance)
	return visibleBlockNames(scan, limit), renderSummary(scan, limit)
}

// visibleBlockNames is the pure half, so the name list can be tested against a
// hand-built scan without a live world behind it.
func visibleBlockNames(scan blockScan, limit int) string {
	names := make([]string, 0, len(scan.clickables)+len(scan.terrainOrder))

	// Clickables are listed before terrain because they are the actionable ones:
	// a chest or a crafting table in view is the reason to stop, and burying it
	// under a histogram of stone would hide it.
	seen := make(map[string]bool, len(scan.clickables)+len(scan.terrainOrder))
	rendered := renderClickables(scan.clickables, limit)
	for _, c := range rendered {
		if seen[c.name] {
			continue
		}
		seen[c.name] = true
		names = append(names, c.name)
	}

	terrain := rank(scan.terrain, scan.terrainOrder)
	if limit > 0 && len(names)+len(terrain) > limit {
		terrain = trim(terrain, limit-len(names))
	}
	for _, name := range terrain {
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}

	if len(names) == 0 {
		return "none"
	}
	// Plain comma, no space. This string is never shown to a person — the
	// readable rendering is — and it is split on commas by the brain's rules. A
	// space after the separator makes the contract depend on the reader
	// trimming, which is exactly the kind of quiet coupling that lets a term
	// like " oak_log" slip through as a different name.
	return strings.Join(names, ",")
}

// renderClickables sorts the clickables nearest-first and drops repeats of the
// same structure within sameStructureDistance.
func renderClickables(clickables []clickableBlock, limit int) []clickableBlock {
	sort.Slice(clickables, func(i, j int) bool { return clickables[i].dist < clickables[j].dist })
	rendered := make([]clickableBlock, 0, limit)
	for _, c := range clickables {
		if alreadyListedStructure(c, rendered) {
			continue
		}
		rendered = append(rendered, c)
		if limit > 0 && len(rendered) >= limit {
			break
		}
	}
	return rendered
}

// trim shortens a list to at most n entries, and to nothing when n is negative —
// which is what a limit smaller than what is already listed means.
func trim(list []string, n int) []string {
	if n <= 0 {
		return nil
	}
	if n >= len(list) {
		return list
	}
	return list[:n]
}

// rank orders names by how often they were seen, falling back to first-sight
// order so the result is stable and a term is never silently reordered between
// two ticks — a list that reshuffles every tick is unreadable in a prompt.
func rank(counts map[string]int, order []string) []string {
	out := make([]string, len(order))
	copy(out, order)
	sort.SliceStable(out, func(i, j int) bool {
		return counts[out[i]] > counts[out[j]]
	})
	return out
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
//
// The vision cone is applied here too, so "visible" means the same thing to the
// AGI as it does to the block summary. Checking only the ray would let the
// brain decide someone is present directly behind it.
func SeesPoint(b *bot.Bot, to mgl32.Vec3) bool {
	if !InFieldOfView(b, to) {
		return false
	}
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
		if !loaded || solid {
			return false
		}
	}
	return true
}

// SeesBlock applies the same view cone and occlusion rules as the block summary.
// Chunk receipt alone is not evidence that a semantic target was observed.
func SeesBlock(b *bot.Bot, target protocol.BlockPos) bool {
	if _, loaded := b.GetBlockName(target.X(), target.Y(), target.Z()); !loaded {
		return false
	}
	center := mgl32.Vec3{float32(target.X()) + 0.5, float32(target.Y()) + 0.5, float32(target.Z()) + 0.5}
	if !InFieldOfView(b, center) {
		return false
	}
	eye := b.GetCoords().Add(mgl32.Vec3{0, bot.PlayerEyeHeight, 0})
	return hasLineOfSight(b, eye, center, target)
}

// hasLineOfSight excludes the target itself as an occluder. Unknown terrain
// is not evidence of a clear ray: fail closed without claiming a wall exists.
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
		if !loaded || solid {
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
