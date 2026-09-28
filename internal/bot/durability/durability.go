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

// maxUses is how many swings a tool of each kind is assumed to survive.
//
// These are the vanilla durability values for the middle tiers. Being wrong by a
// factor of two is not important: the tracker is a reason to swap early, not a
// prediction of the exact break. The thresholds below it are deliberately
// conservative, because swapping a tool that still had ten swings left costs one
// equip and breaking one that had two costs the whole vein.
var maxUses = map[string]int{
	"sword":   250,
	"axe":     250,
	"pickaxe": 132,
	"shovel":  132,
	"hoe":     132,
	"shield":  336,
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
	limit, known := maxUses[toolFamily(itemName)]
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
	limit, known := maxUses[toolFamily(itemName)]
	if !known {
		return -1
	}
	left := limit - d.Used(slot)
	if left < 0 {
		return 0
	}
	return left
}

// toolFamily maps an item name to its durability class.
//
// The match is on the family suffix so every tier of a tool shares one budget:
// an iron pickaxe and a wooden one do not break at the same moment, and the
// bot's decision should not pretend they do.
func toolFamily(itemName string) string {
	name := strings.ToLower(strings.TrimSpace(itemName))
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	for family := range maxUses {
		if name == family || strings.HasSuffix(name, "_"+family) {
			return family
		}
	}
	return ""
}
