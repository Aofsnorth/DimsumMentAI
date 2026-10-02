package bucket

import (
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Inventory is the bot's inventory flattened to a total count per item name.
type Inventory map[string]int

// NewInventory flattens a raw slot map plus its network-ID-to-name table.
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

// Result is what one bucket operation actually achieved.
//
// The flags are kept separate rather than collapsed into a bool because they
// fail independently, and each failure means something different:
//
//   - the bucket did not change: the server rejected the use, or the bot was
//     holding the wrong item, and nothing happened at all;
//   - the block did not change: the bucket changed but the world did not, which
//     is what a client-predicted inventory looks like when the server ignored
//     the click.
//
// A struct that only had Confirmed() would lose the second case, and the second
// case is the one worth a log line.
type Result struct {
	// Kind is what the operation was about: what the bucket now holds, or
	// what it held when it was emptied.
	Kind BucketKind
	// BucketChanged is true when the held item's contents changed.
	BucketChanged bool
	// BlockChanged is true when the target cell's block changed.
	BlockChanged bool
	// BlockReadable is false when the target cell was never in the world
	// cache, so "no change" and "could not look" are told apart.
	BlockReadable bool
	// StackResponse is true when the server answered the use with a
	// successful ItemStackResponse. It is only ever set when a
	// StackResponseSource is wired; see that interface.
	StackResponse bool
	// StackResponseSeen is true when a source was wired at all, whatever it
	// said. False means the stricter check was unavailable and the inventory
	// snapshot was the only evidence used.
	StackResponseSeen bool
}

// Confirmed reports whether the operation is honestly a success: the bucket
// changed AND the world changed.
func (r Result) Confirmed() bool {
	return r.BucketChanged && r.BlockChanged
}

// BucketContentsChanged reports whether a bucket's contents moved from one
// kind to another between two readings.
//
// The "expected" pair is explicit rather than "any change at all" because an
// inventory that shifts for some unrelated reason — the hotbar being tidied, a
// stack merging — must not be read as a bucket being filled.
func BucketContentsChanged(before, after Inventory, from, to BucketKind) bool {
	if to == BucketUnknown || from == BucketUnknown {
		return false
	}
	// A gain of the target, or a loss of the source, or both: the server can
	// deliver the emptied bucket before the filled one and either order is
	// real.
	if gained(after, to) > gained(before, to) {
		return true
	}
	return lost(before, after, from) > 0
}

func gained(inv Inventory, kind BucketKind) int {
	return inv.Count(func(name string) bool { return kindMatches(name, kind) })
}

func lost(before, after Inventory, kind BucketKind) int {
	return gained(before, kind) - gained(after, kind)
}

func kindMatches(name string, kind BucketKind) bool {
	for _, want := range bucketItemNames[kind] {
		if name == want {
			return true
		}
	}
	return false
}

// --- Observation seams -------------------------------------------------

// BlockStateSource exposes a block's full state, not just its name.
//
// *bot.Bot cannot supply this: GetBlockName returns the name from the world
// cache and the world cache has no accessor for the state properties. That is
// the whole reason a cauldron's level is not readable today. The wiring is one
// method on the bot — GetBlockState(x, y, z) (string, map[string]any, bool),
// backed by chunk.RuntimeIDToState in the decoder — and until it lands, this
// package confirms cauldron changes by block state rather than by level.
type BlockStateSource interface {
	GetBlockState(x, y, z int32) (name string, props map[string]any, ok bool)
}

// StackResponse is one server answer to an item use.
type StackResponse struct {
	// OK is true for status 0, which is the only value gophertunnel's
	// processItemStackResponse treats as accepted.
	OK bool
	// At is when it arrived.
	At time.Time
}

// StackResponseSource is the observation seam for ItemStackResponse packets.
//
// The inventory snapshot is already server-driven — it is updated by
// ApplyItemStackResponse and friends — so a bucket contents change is real
// evidence on its own. This seam is the stricter check: it says the server
// answered this specific request, rather than that the inventory moved at some
// point during the wait. A nil source is not a failure; it is the absence of
// the stricter check, and the manager says so in the log.
type StackResponseSource interface {
	StackResponsesSince(since time.Time) []StackResponse
}

// accepted reports whether any of the responses was accepted.
func accepted(responses []StackResponse) bool {
	for _, r := range responses {
		if r.OK {
			return true
		}
	}
	return false
}
