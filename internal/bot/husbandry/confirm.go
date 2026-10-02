package husbandry

import (
	"strings"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// --- Entity metadata ---------------------------------------------------

// EntityMeta is the slice of an animal's entity data a taming check needs.
//
// entity.Info — the struct every subsystem reads out of the bot — has no
// metadata field at all, which is why this is a separate type and a separate
// seam. Bedrock carries the collar and owner on the entity's data flags
// (SetActorData / AddActor metadata), and nothing in this repository stores it.
type EntityMeta struct {
	// EntityType is the mob's type string, e.g. "minecraft:wolf". A blank
	// type is never treated as a match: an unidentified entity is an
	// unidentified entity, not a tameable one.
	EntityType string
	// Tamed is the mob's tamed flag. On its own it is weak evidence — it is
	// set on mobs a player has previously owned, and cleared on some servers
	// when a chunk unloads — so it is never the deciding signal.
	Tamed bool
	// Collared is the wolf's collar. This is the acceptance criterion for
	// "the wolf is actually collared".
	Collared bool
	// OwnerKnown means an owning player UUID is set. It is the equivalent
	// signal for the mobs that do not wear a collar: cats, horses, llamas.
	OwnerKnown bool
	// Baby is the mob's baby flag, and doubles as the breeding signal when
	// the same animal turns into a calf.
	Baby bool
}

// tameableMobs are the animals a player can make their own. A mob outside this
// list is never reported as tamed no matter what its flags say, because a cow
// with a stray flag is not a pet.
var tameableMobs = []string{
	"wolf", "cat", "ocelot", "horse", "donkey", "mule", "llama",
	"parrot", "fox", "camel", "strider", "panda",
}

// IsTameable reports whether an entity type is a mob a player can tame.
//
// Strider and panda are in the list because Bedrock lets a player tame them
// with the right item, and the flag that results is the same one a collar sets.
func IsTameable(name string) bool {
	n := normalise(name)
	if n == "" {
		return false
	}
	for _, want := range tameableMobs {
		if n == want {
			return true
		}
	}
	return false
}

// TameConfirmed reports whether an animal became the player's between two
// readings of its metadata.
//
// It is a transition, not a state: an animal that was already collared before
// the attempt is not evidence that this attempt worked. Both readings must name
// a genuinely tameable mob, because "the flags changed on something I cannot
// identify" is not a tame.
//
// The old routine had no such function. It counted attempts.
func TameConfirmed(before, after EntityMeta) bool {
	if !IsTameable(before.EntityType) && !IsTameable(after.EntityType) {
		return false
	}
	wasOwned := before.Collared || before.OwnerKnown
	nowOwned := after.Collared || after.OwnerKnown
	return !wasOwned && nowOwned
}

// --- Breeding ----------------------------------------------------------

// BreedSignal is everything that was observed after feeding two animals.
type BreedSignal struct {
	// HeartsObserved is the love-mode particle burst, either seen as a level
	// event or as the server's love-hearts actor event.
	HeartsObserved bool
	// BabyAppeared is a calf/piglet/chick of the pair's type showing up
	// afterwards. It is the strongest signal and the one that arrives last.
	BabyAppeared bool
	// BothFoodsConsumed means the food stack went down by the two items the
	// two feeds should have eaten. It is real evidence — the inventory is
	// server-authoritative — but it is not conclusive on its own, which is
	// why it is reported separately from the other two.
	BothFoodsConsumed bool
}

// BreedingConfirmed reports whether breeding can be honestly reported.
//
// Any one of the three signals is enough. All three are real observations; none
// of them is the click that was sent, which is all the old code had.
func BreedingConfirmed(s BreedSignal) bool {
	return s.HeartsObserved || s.BabyAppeared || s.BothFoodsConsumed
}

// --- Inventory ---------------------------------------------------------

// Inventory is the bot's inventory flattened to a total count per item name.
//
// Totals rather than slots: a milk bucket can land in any free slot, and a
// stack can merge into an existing one, so a slot-by-slot diff reports nothing
// when the thing actually happened.
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

// Gained returns how many items matching predicate appeared between two
// readings.
func (inv Inventory) Gained(before Inventory, predicate func(name string) bool) int {
	gained := 0
	for name, n := range inv {
		if !predicate(name) {
			continue
		}
		if d := n - before[name]; d > 0 {
			gained += d
		}
	}
	return gained
}

// MilkConfirmed reports whether a milk bucket appeared since the last reading.
//
// The bucket changing is the whole of the interaction: there is no particle,
// no sound the bot parses, and no other item involved. Reading the inventory
// is therefore not a fallback here — it is the primary signal, and it is
// server-authoritative.
func MilkConfirmed(before, after Inventory) bool {
	return after.Gained(before, isMilkBucket) > 0
}

func isMilkBucket(name string) bool { return normalise(name) == "milk_bucket" }

// ShearConfirmed reports whether wool appeared, and how much.
//
// Wool is the only product of shearing, and a sheep that was not shorn has none
// in the inventory to confuse the count with. A sheared sheep is worth nothing
// to report if no wool turned up.
func ShearConfirmed(before, after Inventory) (int, bool) {
	gained := after.Gained(before, isWool)
	return gained, gained > 0
}

func isWool(name string) bool { return strings.Contains(normalise(name), "wool") }

// --- Observation seams -------------------------------------------------

// The actor-event types this package reads. They are the server's own verdict
// on an animal, delivered as packet.ActorEvent.
const (
	// ActorEventTamingSucceeded is the server confirming the animal is tamed.
	ActorEventTamingSucceeded = uint8(packet.ActorEventTamingSucceeded)
	// ActorEventTamingFailed is the server rejecting a tame attempt. It is an
	// observation, and an observation of failure — which is why an attempt
	// that produced it must not be reported as a tame.
	ActorEventTamingFailed = uint8(packet.ActorEventTamingFailed)
	// ActorEventLoveHearts is the breeding heart burst.
	ActorEventLoveHearts = uint8(packet.ActorEventLoveHearts)
	// ActorEventInLoveHearts is the hearts shown on a single animal that has
	// just been fed.
	ActorEventInLoveHearts = uint8(packet.ActorEventInLoveHearts)
	// ActorEventEatGrass is grazing, and is never a breeding or taming
	// signal. It is named here so a matcher cannot pick it up by accident.
	ActorEventEatGrass = uint8(packet.ActorEventEatGrass)
)

// ActorEvent is one observed actor event, reduced from packet.ActorEvent so
// the decision functions stay free of protocol types.
type ActorEvent struct {
	// RuntimeID is the entity the event was sent for.
	RuntimeID uint64
	// Type is one of the ActorEvent* constants above.
	Type uint8
	// At is when the server sent it.
	At time.Time
}

// ActorEventSource is the observation seam for taming and love-heart events.
//
// A runtimeID of 0 means "every entity", because a caller watching two animals
// of the same kind has two IDs and one question.
type ActorEventSource interface {
	ActorEventsSince(runtimeID uint64, since time.Time) []ActorEvent
}

// EntityMetaSource is the observation seam for the collar, owner, and baby
// flags.
//
// Implementations return ok=false when the entity is gone or was never
// tracked, which is not a negative reading — it is no reading at all, and the
// callers treat it as such.
type EntityMetaSource interface {
	EntityMeta(runtimeID uint64) (EntityMeta, bool)
}

// HasEvent reports whether an event of the given type was sent at or after
// since. Without the `since` bound, a rejection from a previous attempt is
// still in the buffer and reads as this attempt's result.
func HasEvent(events []ActorEvent, typ uint8, since time.Time) bool {
	for _, e := range events {
		if e.Type == typ && !e.At.Before(since) {
			return true
		}
	}
	return false
}

// TameObserved reports what the server said about a tame attempt: whether it
// accepted, whether it rejected, or whether it said nothing at all.
//
// The three-way answer matters. Collapsing "said nothing" into "said yes" is
// the bug this package was written to remove, so this function cannot express it.
func TameObserved(events []ActorEvent) (succeeded, failed bool) {
	for _, e := range events {
		switch e.Type {
		case ActorEventTamingSucceeded:
			succeeded = true
		case ActorEventTamingFailed:
			failed = true
		}
	}
	return succeeded, failed
}

// HeartsObserved reports whether a love-mode particle burst was sent.
func HeartsObserved(events []ActorEvent) bool {
	for _, e := range events {
		if e.Type == ActorEventLoveHearts || e.Type == ActorEventInLoveHearts {
			return true
		}
	}
	return false
}

// normalise lower-cases a name and strips the namespace.
func normalise(name string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "minecraft:")))
}
