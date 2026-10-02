// Features reports the things in view that make specific activities possible.
//
// It exists because the activity curriculum cannot offer "fish" without knowing
// there is water, or "harvest" without knowing there are ripe crops. Offering
// them anyway produces a bot that repeatedly picks a fishing rod and casts it
// at a tree, which is worse than never offering fishing at all — the failure is
// visible, and the bot learns nothing from it.
//
// The scan reuses the same vision cone and line-of-sight walk as the block
// summary, so "in view" means exactly the same thing everywhere. A crop the bot
// cannot see does not count as a crop it can harvest.
package perception

import (
	"math"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Features is what the world currently offers the bot.
type Features struct {
	// Water is true when standing or flowing water is in view, which is the
	// precondition for fishing.
	Water bool
	// RipeCrops counts fully grown crops in view, the precondition for a
	// harvest that actually yields something.
	//
	// It counts ripe crops rather than any crop on purpose: a bot that harvests
	// seedlings has destroyed the farm and gained nothing, which is a far worse
	// outcome than not farming at all.
	RipeCrops int
	// Logs counts tree trunks in view, the precondition for chopping.
	Logs int
	// Animals counts passive, farmable animals in view, the precondition for
	// herding, taming, or feeding.
	Animals int
}

// WaterBlocks are the block states that mean "there is water here".
//
// The list is by name because the world model exposes names, not state IDs, and
// Bedrock has accumulated a long tail of water-like blocks over the years. A
// substring match on "water" covers the modded and coloured variants without
// pretending to know a list that could never be complete.
var WaterBlocks = []string{"water", "flowing_water", "bubble_column"}

// ripeCropBlocks are the fully grown forms. Seedlings and crops at growth stage
// zero are deliberately absent.
var ripeCropBlocks = []string{
	"wheat", "carrots", "potatoes", "beetroot", "nether_wart",
	"cocoa", "melon", "pumpkin", "sugar_cane", "bamboo",
	"cocoa_pods",
}

// LogBlocks are tree trunks.
var LogBlocks = []string{
	"oak_log", "birch_log", "spruce_log", "jungle_log", "acacia_log",
	"dark_oak_log", "mangrove_log", "cherry_log", "crimson_stem", "warped_stem",
}

// FarmableAnimals are the passive mobs a player keeps. Hostile mobs are
// excluded on purpose: "tend the animals" offered next to a creeper is a task
// the bot should not be choosing.
var FarmableAnimals = []string{
	"cow", "pig", "sheep", "chicken", "rabbit", "horse", "donkey", "mule",
	"llama", "goat", "bee", "turtle", "cat", "wolf", "axolotl", "frog",
}

// VisibleFeatures scans what is in view and reports what the bot could act on.
func VisibleFeatures(b *bot.Bot, maxDistance float32) Features {
	var f Features

	origin := b.GetCoords()
	eye := origin.Add(mgl32.Vec3{0, bot.PlayerEyeHeight, 0})
	radius := int32(maxDistance)
	if radius < 1 {
		radius = 1
	}
	bx := int32(math.Floor(float64(origin.X())))
	by := int32(math.Floor(float64(origin.Y())))
	bz := int32(math.Floor(float64(origin.Z())))

	for dx := -radius; dx <= radius; dx++ {
		for dy := scanBelow; dy <= scanAbove; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				pos := protocol.BlockPos{bx + dx, by + dy, bz + dz}
				name, ok := b.GetBlockName(pos.X(), pos.Y(), pos.Z())
				if !ok {
					continue
				}
				clean := CleanName(name)
				if clean == "" || clean == "air" {
					continue
				}
				center := mgl32.Vec3{
					float32(pos.X()) + 0.5,
					float32(pos.Y()) + 0.5,
					float32(pos.Z()) + 0.5,
				}
				if center.Sub(eye).Len() > maxDistance {
					continue
				}
				if !InFieldOfView(b, center) || !HasLineOfSight(b, eye, center, pos) {
					continue
				}

				switch {
				case MatchesAny(clean, WaterBlocks):
					f.Water = true
				case IsRipeCrop(clean):
					f.RipeCrops++
				case MatchesAny(clean, LogBlocks):
					f.Logs++
				}
			}
		}
	}

	f.Animals = countFarmableAnimals(b, origin, maxDistance)
	return f
}

// countFarmableAnimals counts passive animals in view, using the same
// line-of-sight rule the block scan uses.
func countFarmableAnimals(b *bot.Bot, origin mgl32.Vec3, maxDistance float32) int {
	actors := b.GetEntities()
	eye := origin.Add(mgl32.Vec3{0, bot.PlayerEyeHeight, 0})

	count := 0
	for _, info := range actors {
		if info == nil {
			continue
		}
		name := entity.NormalizeName(info.Type)
		if !MatchesAny(name, FarmableAnimals) {
			continue
		}
		if info.Position.Sub(eye).Len() > maxDistance {
			continue
		}
		if !InFieldOfView(b, info.Position) {
			continue
		}
		count++
	}
	return count
}

// IsRipeCrop distinguishes a harvestable crop from a seedling.
func IsRipeCrop(clean string) bool {
	if !MatchesAny(clean, ripeCropBlocks) {
		return false
	}
	// Growth-stage and age suffixes are what mark an immature plant. A crop
	// block with no such suffix is assumed ripe, because a bot that refuses to
	// harvest an unrecognised ripe crop merely does nothing, while a bot that
	// harvests seedlings destroys the field.
	for _, marker := range []string{"seedling", "sprout", "stage0", "stage1", "age0", "age1", "young"} {
		if strings.Contains(clean, marker) {
			return false
		}
	}
	return true
}

// MatchesAny reports whether a cleaned block name contains any of the given
// tokens.
//
// Substring rather than equality, because Bedrock has far more water and crop
// block variants than any fixed list could hold, and a modded server will name
// them in ways no enumeration anticipated. Over-matching is bounded by the
// tokens being specific enough that a false positive is an odd block name
// rather than a wrong action.
func MatchesAny(clean string, tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(clean, token) {
			return true
		}
	}
	return false
}
