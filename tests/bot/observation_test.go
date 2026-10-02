package bot_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/world"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	// dragonfly's world package fills in chunk.StateToRuntimeID and
	// chunk.RuntimeIDToState at init. Without it linked, GetBlockState
	// correctly reports "no reading" and the test below would skip — which is
	// exactly the case it exists to check is not a wrong answer.
	_ "github.com/df-mc/dragonfly/server/world"
)

// newObservationBot builds a bot with nothing but the maps the observation
// layer writes to. A real bot gets these from NewBot; here they only need to
// exist, and a nil world cache is deliberate so the block-state test can assert
// the honest no-reading case rather than a fabricated one.
func newObservationBot(t *testing.T) *bot.Bot {
	t.Helper()
	return &bot.Bot{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Actors:      make(map[uint64]*entity.Info),
		EntityMetas: nil,
	}
}

// The survival actions were built to ask the server whether it agreed, and
// nothing answered them.
//
// A fishing rod that reeled, a taming routine that reported a tame, and a crop
// harvest that decided a field was ripe all needed a signal that arrived as a
// packet. IDActorEvent had no handler, AddActor threw its metadata away, and
// the block cache kept the runtime ID but resolved it no further than a name —
// so every Bedrock cauldron looked empty and every stage of every crop looked
// identical. The managers were honest about it: with nothing wired they refused
// every time, which is correct and useless.
//
// These tests are the ones that would have caught that.

// TestAnActorEventIsRecorded is the bite, the tame and the hearts in one
// sentence: the packet arrives, it is kept.
func TestAnActorEventIsRecorded(t *testing.T) {
	t.Parallel()

	b := newObservationBot(t)
	const hook uint64 = 4242

	handled := bot.HandleActorEvent(b, &packet.ActorEvent{
		EntityRuntimeID: hook,
		EventType:       packet.ActorEventFishhookTease,
	})
	if !handled {
		t.Fatal("HandleActorEvent did not claim the packet")
	}

	since := time.Now().Add(-time.Minute)
	events := b.ActorEventsSince(hook, since)
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1: %+v", len(events), events)
	}
	if events[0].Type != packet.ActorEventFishhookTease {
		t.Errorf("recorded event type %d, want the fishhook tease %d", events[0].Type, packet.ActorEventFishhookTease)
	}
	if events[0].RuntimeID != hook {
		t.Errorf("recorded for entity %d, want the hook %d", events[0].RuntimeID, hook)
	}
}

// TestEventsAreScopedToTheirEntity. The ring is shared by every mob on the
// server, so a wolf's taming failure must not read as a fish's bite.
func TestEventsAreScopedToTheirEntity(t *testing.T) {
	t.Parallel()

	b := newObservationBot(t)
	since := time.Now().Add(-time.Minute)

	bot.HandleActorEvent(b, &packet.ActorEvent{EntityRuntimeID: 1, EventType: packet.ActorEventTamingSucceeded})
	bot.HandleActorEvent(b, &packet.ActorEvent{EntityRuntimeID: 2, EventType: packet.ActorEventFishhookTease})

	if got := b.ActorEventsSince(1, since); len(got) != 1 || got[0].Type != packet.ActorEventTamingSucceeded {
		t.Errorf("entity 1 read %+v, want only its own taming event", got)
	}
	if got := b.ActorEventsSince(2, since); len(got) != 1 || got[0].Type != packet.ActorEventFishhookTease {
		t.Errorf("entity 2 read %+v, want only its own tease", got)
	}
}

// TestTheSinceBoundIsHonoured is what stops the last cast's bite from reeling
// the next one. The ring deliberately outlives a single attempt.
func TestTheSinceBoundIsHonoured(t *testing.T) {
	t.Parallel()

	b := newObservationBot(t)
	const hook uint64 = 7

	bot.HandleActorEvent(b, &packet.ActorEvent{EntityRuntimeID: hook, EventType: packet.ActorEventFishhookTease})

	// Ask from the future: a window that has not happened yet must see nothing,
	// or every cast reels on the previous cast's bite.
	if got := b.ActorEventsSince(hook, time.Now().Add(time.Hour)); len(got) != 0 {
		t.Errorf("a future window returned %d past events: %+v", len(got), got)
	}
}

// TestTheEventRingIsBounded. A connection that stays up for days must not grow
// this without limit, and the cap has to actually evict.
func TestTheEventRingIsBounded(t *testing.T) {
	t.Parallel()

	b := newObservationBot(t)
	const hook uint64 = 1
	for i := 0; i < 2000; i++ {
		b.RecordActorEvent(hook, packet.ActorEventLoveHearts, time.Now())
	}

	events := b.ActorEventsSince(hook, time.Now().Add(-time.Hour))
	if len(events) > 512 {
		t.Errorf("the ring held %d events after 2000 writes; it is not bounded", len(events))
	}
	if len(events) == 0 {
		t.Error("the ring evicted everything instead of the oldest")
	}
}

// TestEntityMetadataRecordsTheCollar is the taming signal. The flag is in the
// AddActor metadata and was being dropped on the floor.
func TestEntityMetadataRecordsTheCollar(t *testing.T) {
	t.Parallel()

	b := newObservationBot(t)
	const wolf uint64 = 99

	meta := protocol.NewEntityMetadata()
	meta[uint32(protocol.EntityDataKeyOwner)] = int64(1)
	meta[uint32(protocol.EntityDataFlagTamed)] = true
	b.RecordEntityMeta(wolf, meta)

	state, ok := b.EntityMeta(wolf)
	if !ok {
		t.Fatal("a wolf whose metadata was recorded reads as untracked")
	}
	if !state.Collared || !state.OwnerKnown {
		t.Errorf("owner metadata did not set the collar: %+v", state)
	}
	if !state.Tamed {
		t.Errorf("tamed flag did not read: %+v", state)
	}
}

// TestAnUntrackedEntityIsNoReading is the honest default that keeps taming from
// claiming a tame. ok=false must not collapse into "untamed, so it worked".
func TestAnUntrackedEntityIsNoReading(t *testing.T) {
	t.Parallel()

	b := newObservationBot(t)
	if _, ok := b.EntityMeta(12345); ok {
		t.Error("an entity the bot never saw reported a metadata reading; that is a fabricated negative")
	}
}

// TestMetadataIsForgottenWithTheEntity. Runtime IDs are reused, so a collar left
// behind would make the next occupant of the ID inherit it.
func TestMetadataIsForgottenWithTheEntity(t *testing.T) {
	t.Parallel()

	b := newObservationBot(t)
	const wolf uint64 = 5

	meta := protocol.NewEntityMetadata()
	meta[uint32(protocol.EntityDataKeyOwner)] = int64(1)
	b.RecordEntityMeta(wolf, meta)

	if _, ok := b.EntityMeta(wolf); !ok {
		t.Fatal("the collar was never recorded")
	}
	b.ForgetEntityMeta(wolf)
	if _, ok := b.EntityMeta(wolf); ok {
		t.Error("a departed entity's collar survived; the next spawn on that runtime ID would inherit it")
	}
}

// TestBlockStateIsNotJustAName is the cauldron and the crop.
//
// Every Bedrock cauldron state is called "minecraft:cauldron" and differs only in
// fill_level, and every stage of a crop shares one name with its siblings. A
// name-only reading cannot tell an empty cauldron from a full one, nor wheat at
// stage 3 from wheat at stage 7 — which is how a harvest destroyed a freshly
// planted field.
func TestBlockStateIsNotJustAName(t *testing.T) {
	t.Parallel()

	b := newObservationBot(t)
	b.WorldCache = world.NewWorldCache(0, cube.Range{-64, 319}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Put a real block in the cache so there is a runtime ID to resolve.
	rid, ok := chunk.StateToRuntimeID("minecraft:cauldron", nil)
	if !ok {
		t.Skip("this binary does not link the block palette, so no state can be resolved")
	}
	b.WorldCache.SetBlockRID(0, 64, 0, rid)

	name, props, ok := b.GetBlockState(0, 64, 0)
	if !ok {
		t.Fatal("a cell holding a resolved block reported no state; the accessor has to be able to answer at all")
	}
	if name != "minecraft:cauldron" {
		t.Errorf("resolved name = %q, want minecraft:cauldron", name)
	}
	// The properties are the part the name cannot carry. A cell whose state
	// resolves to a name and no properties at all is the name-only reading this
	// accessor exists to replace, so it is worth failing on.
	if props == nil {
		t.Error("GetBlockState returned a name and no properties; that is the name-only reading it was written to replace")
	}
}

// TestBlockStateOnAnEmptyBotIsNoReading. No world cache means no reading, and
// no reading must not look like an empty cauldron.
func TestBlockStateOnAnEmptyBotIsNoReading(t *testing.T) {
	t.Parallel()

	b := newObservationBot(t)
	if _, _, ok := b.GetBlockState(0, 0, 0); ok {
		t.Error("a bot with no world cache reported a block state")
	}
}
