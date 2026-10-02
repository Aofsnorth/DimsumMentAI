package fishing

import (
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// The actor-event types this package cares about. They are the server telling
// the client something happened to an entity, and the fishing one is the bite.
//
// packet.IDActorEvent is currently unhandled in the network layer, so today
// nothing ever fills an ActorEvent slice. That is why ActorEventSource is an
// explicit seam rather than a method on Bot: see the note on the interface.
const (
	// ActorEventFishhookTease is sent for the hook when a fish is interested.
	// It is the authoritative bite, and it is what a real client waits for.
	ActorEventFishhookTease = uint8(packet.ActorEventFishhookTease)
	// ActorEventFishhookBubble is the periodic bubble trail. It is deliberately
	// NOT a bite: bubbles appear on any bobber that has been in the water.
	ActorEventFishhookBubble = uint8(packet.ActorEventFishhookBubble)
	// ActorEventTamingSucceeded is the server confirming an animal is tamed.
	// The husbandry package has its own copy of this constant; they are
	// separate packages with separate observation seams on purpose.
	ActorEventTamingSucceeded = uint8(packet.ActorEventTamingSucceeded)
	// ActorEventTamingFailed is the server rejecting a tame attempt.
	ActorEventTamingFailed = uint8(packet.ActorEventTamingFailed)
	// ActorEventLoveHearts is the breeding heart burst.
	ActorEventLoveHearts = uint8(packet.ActorEventLoveHearts)
)

// ActorEvent is one observed actor event, reduced from packet.ActorEvent so the
// decision functions above stay free of protocol types and testable with no
// connection.
type ActorEvent struct {
	// RuntimeID is the entity the event was sent for. For a bite that is the
	// fishing hook.
	RuntimeID uint64
	// Type is one of the ActorEvent* constants above.
	Type uint8
	// At is when the server sent it.
	At time.Time
}

// ActorEventSource is the observation seam for bite events.
//
// It is an interface rather than a method on Bot because *bot.Bot cannot supply
// it: packet.IDActorEvent has no handler in internal/bot/network/player, so the
// events are never stored anywhere. The fix is one handler plus one accessor on
// the bot; until that lands, this returns nothing and the fisherman falls back
// to watching the bobber's motion.
//
// Implementations must treat a runtimeID of 0 as "every entity", because a
// caller that has never seen the hook as a tracked actor has no ID to ask about.
type ActorEventSource interface {
	ActorEventsSince(runtimeID uint64, since time.Time) []ActorEvent
}

// HasEvent reports whether an event of the given type was sent at or after
// since.
//
// The `since` argument is not decoration. Without it the last cast's tease is
// still sitting in the buffer and the next cast reels on it, throwing the line
// back with nothing on it.
func HasEvent(events []ActorEvent, typ uint8, since time.Time) bool {
	for _, e := range events {
		if e.Type == typ && !e.At.Before(since) {
			return true
		}
	}
	return false
}

// BiteObserved reports whether a fishhook tease was sent since the given moment.
//
// This is the primary bite signal. The motion predicate in bite.go is the
// fallback for servers or proxies that do not forward the event, and the
// fisherman accepts either — never neither.
func BiteObserved(events []ActorEvent, since time.Time) bool {
	return HasEvent(events, ActorEventFishhookTease, since)
}
