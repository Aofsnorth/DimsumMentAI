// Ground safety: the blocks and the emptiness that a path must not cross.
//
// This is a vocabulary, not a behaviour. Everything here answers one question —
// "is standing here a way to die?" — and the answer is folded into IsHazard, so
// every neighbour rule the walker already has (walk, drop, parkour, step-jump,
// diagonal, scaffold, ladder) inherits the answer without any of them knowing
// the list exists.
//
// That is the point. A safety rule that each movement mode has to remember to
// consult is a safety rule that the next movement mode will forget, and the
// failure is always the same: a bot that walks confidently into lava because the
// fall-planning branch was written before lava was on the list.

package pathfinder

import (
	"strings"
)

// worldFloorY is the bottom of a Bedrock world. Below it there is no block to
// land on and no way back, so a cell down there is a death rather than a detour.
const worldFloorY int32 = -64

// worldCeilingY is the build limit, and the point above which the bot's own
// world model stops having data.
const worldCeilingY int32 = 320

// lethalBlocks are the loaded blocks that hurt on contact or on approach.
//
// The list is deliberately about consequences rather than tidiness. Lava and
// fire were already here; the rest are the ones a survival-focused bot walks
// into in ordinary play — a campfire it built yesterday, a berry bush it cannot
// see past, magma it should have recognised as a floor and not a path.
//
// A block that is merely inconvenient (leaves, tall grass, sugar cane) is not on
// this list. Refusing to walk through a field would be a bot that cannot cross
// a plain, which is worse than the problem being solved.
var lethalBlocks = map[string]bool{
	// Fire and lava.
	"lava":          true,
	"flowing_lava":  true,
	"fire":          true,
	"magma_block":   true,
	"nether_portal": true, // the block itself does not hurt; being caught in the
	// fireball that a ghast throws into it certainly does, and a
	// path that treats the portal mouth as safe scenery is a path
	// that walks the bot into the shot.

	// Contact damage.
	"cactus":           true,
	"sweet_berry_bush": true,
	"wither_rose":      true,
	"campfire":         true,
	"soul_campfire":    true,
	"powder_snow":      true,
	"cobweb":           true,
	"end_portal":       true,
}

// hazardFamilyNames is the lookup form of lethalBlocks, resolved once at first
// use rather than per call: IsHazard runs millions of times over a long gather
// and a map lookup keyed by an untrimmed name is exactly the wrong place to pay
// for string surgery.
var normalisedLethal = func() map[string]bool {
	out := make(map[string]bool, len(lethalBlocks))
	for name, lethal := range lethalBlocks {
		if lethal {
			out[name] = true
		}
	}
	return out
}()

// normaliseBlockName strips the namespace and lowercases, so a lookup written
// in vanilla terms still matches a server that namespaces its blocks.
func normaliseBlockName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// IsVoid reports whether a cell is below the world floor.
//
// This is separate from "not solid" on purpose. The bot's world model answers
// false for a cell it has never heard of and false for a cell that is genuinely
// empty air over the void, and those two answers mean opposite things to a
// bot deciding whether to step forwards.
func IsVoid(y int32) bool {
	return y < worldFloorY || y > worldCeilingY
}

// IsLethal reports whether a cell is occupied by something that hurts.
//
// An unloaded cell is not lethal. The bot cannot see a block it has no data
// for, and refusing to walk anywhere near a chunk it has not decoded would
// leave it rooted in place — a bot that never moves is not a safe bot.
func (w *LocalWorldModel) IsLethal(x, y, z int32) bool {
	if IsVoid(y) {
		return true
	}
	if w.chunkQuerier == nil {
		return false
	}
	rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
	if !loaded {
		return false
	}
	name, ok := blockNameFor(w.chunkQuerier, rid)
	if !ok {
		return false
	}
	return normalisedLethal[normaliseBlockName(name)]
}

// waterNames are the blocks that count as breathable-for-now liquid.
var waterNames = map[string]bool{
	"water":         true,
	"flowing_water": true,
}

// IsWater reports whether a cell is filled with water.
func (w *LocalWorldModel) IsWater(x, y, z int32) bool {
	if w.chunkQuerier == nil {
		return false
	}
	rid, loaded := w.chunkQuerier.GetBlockRID(x, y, z)
	if !loaded {
		return false
	}
	name, ok := blockNameFor(w.chunkQuerier, rid)
	if !ok {
		return false
	}
	return waterNames[normaliseBlockName(name)]
}

// HeadCell returns the world cell a bot's head occupies when its feet are at
// (x, feetY, z).
//
// Bedrock's eye height is 1.62, so the head sits in the cell one block above the
// feet; the fractional remainder is what makes a player look right and is
// irrelevant to whether they can breathe. Isolating the arithmetic here is what
// lets the breath reflex be tested without a body, a connection or a world.
func HeadCell(x, feetY, z int32) (headX, headY, headZ int32) {
	return x, feetY + headHeightBlocks, z
}

// headHeightBlocks is how many blocks above the feet the head sits, in whole
// blocks.
const headHeightBlocks int32 = 1
