package fishing

import (
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Inventory is the bot's inventory flattened to a total count per item name.
//
// Slots are the wrong unit here: a fish can land in any free slot, a stack can
// merge into an existing one, and a rod can be swapped between slots by the
// equip code. Comparing totals is the only comparison that survives all three.
type Inventory map[string]int

// catchableItems is the vanilla fishing loot table, lower-cased and without the
// namespace. Junk is included on purpose: a rotten_flesh is a real catch, and a
// table of only fish would make the bot discard the haul it just proved it got.
//
// The rod is not in this list and must never be added. "fishing_rod" contains
// none of these tokens, which is the whole reason the match is a token list
// rather than a substring of "hook" — "tripwire_hook" is on the loot table and
// "fishing_rod" is the thing in the bot's hand.
var catchableItems = []string{
	// Fish.
	"cod", "salmon", "pufferfish", "tropical_fish", "raw_fish", "cooked_fish",
	"cooked_cod", "cooked_salmon",
	// Junk.
	"bowl", "leather", "leather_horse_armor", "rotten_flesh", "string",
	"bone", "ink_sac", "nautilus_shell", "tripwire_hook", "bamboo",
	"clay_ball", "clay", "seagrass", "water_bottle", "torchflower_seeds",
	"sponge", "scute", "prismarine_shard", "potion", "wooden_hoe", "name_tag",
	"lily_pad", "kelp", "mud",
}

// NewInventory flattens a raw slot map plus its network-ID-to-name table.
//
// Slots whose name is unknown are skipped rather than counted under a blank
// key: an unnamed stack cannot be recognised as loot, and lumping every
// unrecognised stack together would make a whole-inventory rewrite look like a
// catch.
func NewInventory(slots map[uint32]protocol.ItemStack, names map[int32]string) Inventory {
	inv := Inventory{}
	for _, s := range slots {
		if s.Count == 0 {
			continue
		}
		name := names[s.NetworkID]
		if name == "" {
			continue
		}
		inv[normalise(name)] += int(s.Count)
	}
	return inv
}

// CountOf returns the total number of one item.
func (inv Inventory) CountOf(name string) int { return inv[normalise(name)] }

// Count totals every item whose name satisfies the predicate.
func (inv Inventory) Count(predicate func(name string) bool) int {
	total := 0
	for name, n := range inv {
		if predicate(name) {
			total += n
		}
	}
	return total
}

// IsCatchable reports whether an item name is on the fishing loot table.
func IsCatchable(name string) bool {
	n := normalise(name)
	if n == "" {
		return false
	}
	for _, want := range catchableItems {
		if n == want {
			return true
		}
	}
	return false
}

// CaughtDelta returns how many items of loot the inventory gained.
//
// This is the catch confirmation. It is a count of real objects that appeared
// in a server-authoritative inventory, which is the only thing in this package
// that is not a guess. The old implementation incremented a local integer at
// the moment it decided to stop waiting; this counts what arrived.
func CaughtDelta(before, after Inventory) int {
	gained := 0
	for name, n := range after {
		if !IsCatchable(name) {
			continue
		}
		if d := n - before[name]; d > 0 {
			gained += d
		}
	}
	return gained
}

// normalise lower-cases a name and strips the namespace so "minecraft:Cod" and
// "cod" are the same item.
func normalise(name string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "minecraft:")))
}
