// Occlusion for block interaction.
//
// A block click is the one action in this bot that used to be decided by
// distance alone. Every other path that touches the world — mining, storage,
// perception — walks a ray first, so "the chest over there" means a chest the
// bot could actually point at. Clicking did not, and the result was a bot that
// would press a lever or open a door on the far side of a wall it was looking
// at. Servers generally validate interaction *distance* and not occlusion, so
// the click would land. A player cannot do that, and neither should this.
//
// The rule mirrors the storage layer exactly, because two different answers to
// "can I see that?" is how a bot ends up opening the chest it can see and
// ignoring the one it cannot:
//
//   - the target cell is never its own occluder
//   - a cell the world model has never heard of never blocks
//   - a loaded cell blocks unless its name is known to be see-through
//
// Unknown blocks therefore count as solid. That is the conservative direction:
// it can only make the bot decline an interaction it might have managed, never
// make it reach through a wall.

package interact

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	// losStep is how finely the ray between the eyes and a block is sampled.
	// It matches the storage layer: fine enough that a single block of wall is
	// never stepped over, coarse enough that a four-block reach is a few dozen
	// cheap map lookups.
	losStep float32 = 0.2
)

// seeThroughBlocks are the loaded blocks that do not stop a ray.
//
// This is a short list on purpose: it holds the things that actually sit in the
// air between a bot's eyes and a clickable block — torches, grass, rails, water,
// wall furniture. Everything else loaded is treated as solid, so a new wall
// material is occluding the moment it exists rather than the moment somebody
// remembers to add it here.
var seeThroughBlocks = map[string]bool{
	"air":            true,
	"cave_air":       true,
	"void_air":       true,
	"structure_void": true,

	// Light and decoration fixed to a surface.
	"torch":                         true,
	"soul_torch":                    true,
	"redstone_torch":                true,
	"lantern":                       true,
	"soul_lantern":                  true,
	"glow_lichen":                   true,
	"seagrass":                      true,
	"kelp":                          true,
	"kelp_plant":                    true,
	"vine":                          true,
	"ladder":                        true,
	"tripwire":                      true,
	"tripwire_hook":                 true,
	"stone_button":                  true,
	"wooden_button":                 true,
	"spruce_button":                 true,
	"birch_button":                  true,
	"jungle_button":                 true,
	"acacia_button":                 true,
	"dark_oak_button":               true,
	"lever":                         true,
	"stone_pressure_plate":          true,
	"light_weighted_pressure_plate": true,
	"heavy_weighted_pressure_plate": true,
	"stone_button_pressure_plate":   true,

	// Rails sit in a shallow notch in the floor.
	"rail":           true,
	"powered_rail":   true,
	"detector_rail":  true,
	"activator_rail": true,

	// Fluids are see-through. A bot standing in water still clicks things.
	"water":         true,
	"flowing_water": true,
	"lava":          true,
	"flowing_lava":  true,

	// Ground cover and crops.
	"grass":                 true,
	"tall_grass":            true,
	"fern":                  true,
	"dead_bush":             true,
	"dead_tree":             true,
	"sapling":               true,
	"beetroot":              true,
	"beetroots":             true,
	"wheat":                 true,
	"carrots":               true,
	"potatoes":              true,
	"nether_wart":           true,
	"sugar_cane":            true,
	"bamboo":                true,
	"bamboo_sapling":        true,
	"cocoa":                 true,
	"pumpkin_stem":          true,
	"melon_stem":            true,
	"attached_melon_stem":   true,
	"attached_pumpkin_stem": true,
	"lily_pad":              true,
	"carpet":                true,
	"snow_layer":            true,

	// Flowers.
	"dandelion":          true,
	"poppy":              true,
	"blue_orchid":        true,
	"allium":             true,
	"azure_bluet":        true,
	"red_tulip":          true,
	"orange_tulip":       true,
	"white_tulip":        true,
	"pink_tulip":         true,
	"oxeye_daisy":        true,
	"cornflower":         true,
	"lily_of_the_valley": true,
	"sunflower":          true,
	"lilac":              true,
	"rose_bush":          true,
	"peony":              true,
	"torchflower":        true,
	"pitcher_plant":      true,

	// Underwater and small decor.
	"sea_pickle":       true,
	"turtle_egg":       true,
	"coral":            true,
	"coral_fan":        true,
	"coral_block":      true,
	"dead_coral_block": true,
	"azalea":           true,
	"flowering_azalea": true,
	"big_dripleaf":     true,
	"small_dripleaf":   true,
	"spore_blossom":    true,
	"cave_vines":       true,
	"glow_berries":     true,
	"hanging_roots":    true,
}

// isOccluder reports whether a cell stops the ray.
//
// A cell the world model has not loaded is not an occluder. That is the same
// choice the storage layer makes, and for the same reason: a chunk the bot has
// not received data for is a gap in its knowledge, not evidence of a wall, and
// treating a knowledge gap as solid would make the bot refuse to click
// anything within a chunk radius of itself.
func isOccluder(b Bot, x, y, z int32) bool {
	name, loaded := b.GetBlockName(x, y, z)
	if !loaded {
		return false
	}
	return !seeThroughBlocks[normalise(name)]
}

// lineOfSightToBlock walks from the bot's eyes to the centre of a block.
//
// The target cell is skipped rather than tested, because every clickable block
// is by definition the thing at the end of the ray and testing it as its own
// occluder would make every target fail.
func lineOfSightToBlock(b Bot, from mgl32.Vec3, target protocol.BlockPos) bool {
	eye := from.Add(mgl32.Vec3{0, eyeHeight, 0})
	aim := blockAim(Target{Block: target})
	delta := aim.Sub(eye)
	length := delta.Len()
	if length < 0.001 {
		return true
	}
	steps := int(length/losStep) + 1
	for i := 1; i < steps; i++ {
		p := eye.Add(delta.Mul(float32(i) / float32(steps)))
		cell := protocol.BlockPos{
			int32(math.Floor(float64(p.X()))),
			int32(math.Floor(float64(p.Y()))),
			int32(math.Floor(float64(p.Z()))),
		}
		if cell == target {
			continue
		}
		if isOccluder(b, cell.X(), cell.Y(), cell.Z()) {
			return false
		}
	}
	return true
}

// onlyVisible keeps the block targets the bot could actually point at.
//
// It runs once over the scanned candidates rather than inside the per-path
// filters, so the named path and the "whatever is in front" path cannot drift
// apart. Two targeting paths that disagree about what is visible is how a bot
// ends up refusing the button it was looking at while clicking one behind it.
func onlyVisible(b Bot, from mgl32.Vec3, targets []Target) []Target {
	out := make([]Target, 0, len(targets))
	for _, t := range targets {
		if lineOfSightToBlock(b, from, t.Block) {
			out = append(out, t)
		}
	}
	return out
}
