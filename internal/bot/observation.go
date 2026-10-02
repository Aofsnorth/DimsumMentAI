package bot

import (
	"time"

	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Server observation: the signals a survival bot has to act on but cannot infer
// from its own intentions.
//
// Fishing, taming and breeding all ask the same kind of question — did the
// server accept this? — and the answer arrives as a packet nobody was listening
// for. The handlers written to drive the body kept their own record of what they
// had attempted and reported from that, so a cast that never got a bite and a
// feeding that was refused both read as success. The consumers in fishing/,
// husbandry/ and bucket/ were written to ask for these readings instead and were
// left unwired, because nothing on this side produced them.
//
// So this file produces them. Everything here is bounded and read-only: the
// network goroutine writes, the action goroutine reads, and both go through
// b.Mu, which is the discipline ContainerWatchState already follows.

// actorEventLogCap bounds the event ring.
//
// The ring exists to answer "did this happen since I started waiting", and the
// waiters are short — a cast, a feeding, a taming attempt. A cast that takes a
// minute at twenty ticks a second is well inside any sane cap, so a small one
// loses nothing. Growing it instead would be a leak on a connection that stays
// up for days.
const actorEventLogCap = 256

// entityMetaCap bounds the metadata cache the same way: one entry per tracked
// entity, dropped as entities leave.
const entityMetaCap = 512

// ActorEvent is one server event aimed at one entity, as this package records
// it.
//
// It is deliberately untyped. fishing and husbandry each declare their own
// ActorEvent with the same three fields, and Go cannot have two methods of one
// name on one type, so the bot stores this neutral shape and each package gets
// its own view of it through an adapter.
type ActorEvent struct {
	// RuntimeID is the entity the event was sent for.
	RuntimeID uint64
	// Type is a packet.ActorEvent* constant.
	Type uint8
	// At is when the server sent it.
	At time.Time
}

// EntityMetaState is the part of an entity's metadata the survival actions read.
type EntityMetaState struct {
	// EntityType is the tracked type string, e.g. "minecraft:wolf".
	EntityType string
	// Tamed is the tamed flag. On its own it is weak evidence: it is set on
	// mobs a player has previously owned and cleared by some servers when a
	// chunk unloads, so it is never the deciding signal.
	Tamed bool
	// Collared is the owner flag. This is the signal that a wolf is actually
	// collared rather than merely tameable.
	Collared bool
	// OwnerKnown is true when an owner key was present in the metadata.
	OwnerKnown bool
	// Baby is the baby flag.
	Baby bool
}

// RecordActorEvent appends an event to the ring, evicting the oldest when full.
func (b *Bot) RecordActorEvent(runtimeID uint64, eventType uint8, at time.Time) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.ActorEvents == nil {
		b.ActorEvents = make([]ActorEvent, 0, actorEventLogCap)
	}
	if len(b.ActorEvents) >= actorEventLogCap {
		// Compact down instead of re-slicing a window: readers are handed a
		// copy, but a shared backing array that shifts under a slow reader is
		// the kind of bug that only shows up on a busy server.
		n := copy(b.ActorEvents, b.ActorEvents[len(b.ActorEvents)-actorEventLogCap+1:])
		b.ActorEvents = b.ActorEvents[:n]
	}
	b.ActorEvents = append(b.ActorEvents, ActorEvent{RuntimeID: runtimeID, Type: eventType, At: at})
}

// ActorEventsSince returns the recorded events for one entity at or after since.
//
// The since bound is not decoration. The ring deliberately outlives a single
// attempt, so without it the tease from the last cast is still sitting in the
// buffer and the next cast reels on a bite that happened before it was cast.
func (b *Bot) ActorEventsSince(runtimeID uint64, since time.Time) []ActorEvent {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	out := make([]ActorEvent, 0, 8)
	for _, e := range b.ActorEvents {
		if e.RuntimeID == runtimeID && !e.At.Before(since) {
			out = append(out, e)
		}
	}
	return out
}

// HandleActorEvent records an ActorEvent packet.
//
// Without this the authoritative signals never arrive: the fishhook tease that
// is a real bite, the taming success that is a real tame, and the love hearts
// that are a real breeding are all ActorEvent packets, and a bot that drops them
// has to guess at all three.
func HandleActorEvent(b *Bot, pk packet.Packet) bool {
	p, ok := pk.(*packet.ActorEvent)
	if !ok {
		return false
	}
	b.RecordActorEvent(p.EntityRuntimeID, p.EventType, time.Now())
	return true
}

// RecordEntityMeta folds a metadata map into the cached reading for an entity.
func (b *Bot) RecordEntityMeta(runtimeID uint64, meta protocol.EntityMetadata) {
	if len(meta) == 0 {
		return
	}
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.EntityMetas == nil {
		b.EntityMetas = make(map[uint64]EntityMetaState, entityMetaCap)
	}
	state, ok := b.EntityMetas[runtimeID]
	if !ok {
		if len(b.EntityMetas) >= entityMetaCap {
			return
		}
		if actor, tracked := b.Actors[runtimeID]; tracked && actor != nil {
			state.EntityType = actor.Type
		}
	}
	if v, ok := meta[uint32(protocol.EntityDataFlagTamed)]; ok {
		state.Tamed = metaBool(v)
	}
	if _, ok := meta[uint32(protocol.EntityDataKeyOwner)]; ok {
		// Presence of the key is the signal, not its truthiness. A server that
		// clears ownership may write a zero rather than drop the key, and
		// reading "zero means not collared" would call every unowned wolf a
		// tame candidate — the exact inverse of the claim being made.
		state.Collared = true
		state.OwnerKnown = true
	}
	if v, ok := meta[uint32(protocol.EntityDataFlagBaby)]; ok {
		state.Baby = metaBool(v)
	}
	b.EntityMetas[runtimeID] = state
}

// EntityMeta returns the cached metadata reading for an entity.
//
// ok is false when the entity was never tracked, which is no reading at all and
// not a negative one: an untracked wolf is not an untamed wolf, and reporting it
// as untamed would let a taming action claim a tame it never confirmed.
func (b *Bot) EntityMeta(runtimeID uint64) (EntityMetaState, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	state, ok := b.EntityMetas[runtimeID]
	return state, ok
}

// ForgetEntityMeta drops a departed entity's cached metadata, so a runtime ID
// reused by a later spawn cannot inherit the previous occupant's collar.
func (b *Bot) ForgetEntityMeta(runtimeID uint64) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	delete(b.EntityMetas, runtimeID)
}

// metaBool reads a metadata flag, which Bedrock sends as a bool but which some
// hosts deliver as the equivalent number.
func metaBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int8:
		return t != 0
	case int16:
		return t != 0
	case int32:
		return t != 0
	case int64:
		return t != 0
	case uint:
		return t != 0
	case uint16:
		return t != 0
	case uint32:
		return t != 0
	case uint64:
		return t != 0
	case float32:
		return t != 0
	case float64:
		return t != 0
	}
	return false
}

// GetBlockState returns a block's full state, not just its name.
//
// The name alone cannot answer the questions the survival actions ask. Every
// Bedrock cauldron state is called "minecraft:cauldron" and differs only in
// fill_level, so a name check cannot tell an empty cauldron from a full one;
// and every stage of a crop shares one name with its siblings, so "is this wheat
// ripe" has no answer without the age. The world cache already holds the runtime
// ID and the decoder already calls chunk.RuntimeIDToState, so the reading is
// paid for — it was just being thrown away at the name.
//
// ok is false for a cell that is not loaded, which is no reading rather than an
// empty one.
func (b *Bot) GetBlockState(x, y, z int32) (string, map[string]any, bool) {
	if b.WorldCache == nil {
		return "", nil, false
	}
	rid, ok := b.WorldCache.GetBlockRID(x, y, z)
	if !ok {
		return "", nil, false
	}
	// chunk.RuntimeIDToState is a package-level var that dragonfly's world
	// package fills in at init. A binary that never links it leaves the var nil
	// and calling it takes the process down from inside a block lookup, so it is
	// asked rather than called.
	if chunk.RuntimeIDToState == nil {
		return "", nil, false
	}
	name, props, ok := chunk.RuntimeIDToState(rid)
	if !ok {
		return "", nil, false
	}
	return name, props, true
}
