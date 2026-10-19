// Knowing when a tool is about to break.
//
// This is counted locally rather than read from the server. That is a real
// limitation and it is worth being plain about it: Bedrock carries remaining
// durability in item metadata, and this client has never been shown receiving
// it, so there is no number to read. Counting swings gets the same answer for
// the only thing that actually consumes durability in this bot — swinging — and
// it gets it early enough to act on.
//
// The failure it prevents is specific and ugly: the bot mines its way through a
// stone vein with a wooden pickaxe, the pickaxe breaks mid-swing, and every
// subsequent block comes out as nothing. The bot then reports a gather that
// "succeeded" while its inventory never filled.

package durability

import (
	"strings"
	"sync"
)

// maxUses is how many swings a tool with no material prefix is assumed to
// survive. Tiered tools are resolved per material below instead, because a
// single budget for every tier is the bug this table used to have: 132 uses
// (the iron value) means the 80% threshold fired at 105, long after a wooden
// pickaxe (59) or a golden one (32) had already broken.
var maxUses = map[string]int{
	"shield": 336,
}

// materialUses is the vanilla durability of a tool by material. A tool's
// durability does not depend on whether it is a pickaxe or a sword, only on
// what it is made of, which is why one lookup serves every family.
var materialUses = map[string]int{
	"wooden":    59,
	"stone":     131,
	"iron":      250,
	"golden":    32,
	"diamond":   1561,
	"netherite": 2031,
}

// replaceFraction is how much of a tool's life must be gone before the bot stops
// using it. Eighty percent is early enough to always beat the break, and late
// enough that the bot is not constantly swapping tools that are perfectly fine.
const replaceFraction = 0.8

// Tracker counts tool uses per inventory slot.
//
// It is keyed by slot rather than by item name on purpose: two wooden pickaxes
// in different slots have separate lives, and merging them would let a freshly
// replaced pickaxe inherit the old one's near-death count.
type Tracker struct {
	mu   sync.Mutex
	uses map[uint32]int
}

// NewTracker returns a tracker with no history.
func NewTracker() *Tracker {
	return &Tracker{uses: make(map[uint32]int)}
}

// Record notes one use of whatever is in a slot.
func (d *Tracker) Record(slot uint32) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.uses == nil {
		d.uses = make(map[uint32]int)
	}
	d.uses[slot]++
}

// Forget drops a slot's history, called when a different item is put into it.
func (d *Tracker) Forget(slot uint32) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.uses, slot)
}

// Used reports how many uses a slot has recorded.
func (d *Tracker) Used(slot uint32) int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.uses[slot]
}

// ShouldReplace reports whether a tool in a slot is close enough to breaking to
// be worth swapping.
//
// An item with no known durability — a block, food, anything not in the table —
// is never replaced. Swapping a block of cobblestone because it has been held a
// thousand times is a bot that never finishes anything.
func (d *Tracker) ShouldReplace(slot uint32, itemName string) bool {
	limit, known := toolUses(itemName)
	if !known {
		return false
	}
	return float64(d.Used(slot)) >= float64(limit)*replaceFraction
}

// Remaining reports how many more uses a slot is assumed to have, or -1 when
// that is not knowable. It is clamped at zero: a tool that has already gone past
// its limit is reported as spent, not as having a negative life left, because
// "negative uses remaining" is a number that only confuses whoever reads it.
func (d *Tracker) Remaining(slot uint32, itemName string) int {
	limit, known := toolUses(itemName)
	if !known {
		return -1
	}
	left := limit - d.Used(slot)
	if left < 0 {
		return 0
	}
	return left
}

// toolUses resolves how many uses the named item survives, or false when the
// item has no known durability.
func toolUses(itemName string) (int, bool) {
	family := toolFamily(itemName)
	if family == "" {
		// Not a tool this tracker knows about.
		return 0, false
	}
	name := normaliseItemName(itemName)
	if i := strings.IndexByte(name, '_'); i > 0 {
		if uses, ok := materialUses[name[:i]]; ok {
			return uses, true
		}
	}
	if uses, ok := maxUses[family]; ok {
		return uses, true
	}
	return 0, false
}

func normaliseItemName(itemName string) string {
	name := strings.ToLower(strings.TrimSpace(itemName))
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSpace(name)
}

// toolFamily maps an item name to its durability class.
//
// The match is on the family suffix so every tier of a tool is recognised as
// the same kind of tool; the budget itself comes from the material prefix in
// toolUses.
func toolFamily(itemName string) string {
	name := normaliseItemName(itemName)
	for family := range maxUses {
		if name == family || strings.HasSuffix(name, "_"+family) {
			return family
		}
	}
	for _, family := range []string{"sword", "axe", "pickaxe", "shovel", "hoe"} {
		if name == family || strings.HasSuffix(name, "_"+family) {
			return family
		}
	}
	return ""
}
