package husbandry_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/husbandry"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeBot is a pasture: a few animals, an inventory, and a record of what the
// manager reported.
type fakeBot struct {
	pos    mgl32.Vec3
	items  map[uint32]protocol.ItemStack
	names  map[int32]string
	ents   map[uint64]*entity.Info
	events []event.ActionStatus

	// onInteract runs when the manager clicks an animal, which is where a
	// test decides whether the server actually gave anything back.
	onInteract func(call int) map[uint32]protocol.ItemStack
	interacts  int
	reports    []event.ActionStatus
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		pos:   mgl32.Vec3{0, 64, 0},
		items: map[uint32]protocol.ItemStack{},
		names: map[int32]string{},
		ents:  map[uint64]*entity.Info{},
	}
}

func (f *fakeBot) withAnimal(id uint64, kind string) *fakeBot {
	f.ents[id] = &entity.Info{ID: id, Type: kind, Position: mgl32.Vec3{1, 64, 1}, Health: 20}
	return f
}

func (f *fakeBot) GetCoords() mgl32.Vec3 { return f.pos }

func (f *fakeBot) WritePacket(pk packet.Packet) error {
	tx, ok := pk.(*packet.InventoryTransaction)
	if !ok {
		return nil
	}
	if _, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData); !ok {
		return nil
	}
	f.interacts++
	if f.onInteract != nil {
		if next := f.onInteract(f.interacts); next != nil {
			f.items = next
		}
	}
	return nil
}

func (f *fakeBot) GetEntities() map[uint64]*entity.Info {
	out := make(map[uint64]*entity.Info, len(f.ents))
	for id, e := range f.ents {
		out[id] = e
	}
	return out
}

func (f *fakeBot) NavigateTo(pos mgl32.Vec3) {}
func (f *fakeBot) StopMovement()             {}
func (f *fakeBot) LookAt(pos mgl32.Vec3)     {}
func (f *fakeBot) SendChat(msg string)       {}
func (f *fakeBot) GetHeldItemSlot() uint32   { return 0 }

func (f *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack { return f.items }

func (f *fakeBot) GetItemNames() map[int32]string { return f.names }

func (f *fakeBot) EquipItem(slot uint32) error { return nil }

func (f *fakeBot) ReportActionStatus(user string, status event.ActionStatus) {
	f.reports = append(f.reports, status)
	f.events = append(f.events, status)
}

func (f *fakeBot) GetEntityRuntimeID() uint64 { return 1 }

func (f *fakeBot) FormatItemName(name string) string { return name }

// fastTimings shrinks every settle delay so a test does not have to sit
// through five taming attempts worth of real-world waiting.
func fastTimings() husbandry.Timings {
	return husbandry.Timings{Approach: time.Millisecond, Aim: time.Millisecond, Action: 2 * time.Millisecond}
}

// A wolf, a bag of bones, five attempts spent, and nothing whatever happened to
// the animal. The old routine returned true here, every time.
func TestTameWolfRefusesToClaimSuccessItCannotSee(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withAnimal(1, "minecraft:wolf")
	b.items[0] = stack(1, 5)
	b.names[1] = "minecraft:bone"

	m := husbandry.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if m.TameWolf(context.Background()) {
		t.Error("taming reported success with no collared wolf and no server event")
	}
	for _, r := range b.reports {
		if r.Success {
			t.Errorf("a successful tame was reported without an observation: %+v", r)
		}
	}
}

// The wolf's collared flag flips: that is the confirmation.
func TestTameWolfConfirmsWhenTheWolfIsCollared(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withAnimal(1, "minecraft:wolf")
	b.items[0] = stack(1, 5)
	b.names[1] = "minecraft:bone"

	m := husbandry.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())
	m.SetEntityMetaSource(&collaringMeta{after: 2})

	if !m.TameWolf(context.Background()) {
		t.Error("a wolf that was collared during the attempt was not reported as tamed")
	}
}

// The server explicitly rejects the attempt. That is an observation, and it is
// an observation of failure.
func TestTameWolfDoesNotReportSuccessOnARejectedAttempt(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withAnimal(1, "minecraft:wolf")
	b.items[0] = stack(1, 5)
	b.names[1] = "minecraft:bone"

	m := husbandry.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())
	m.SetActorEventSource(&alwaysEvent{typ: husbandry.ActorEventTamingFailed})

	if m.TameWolf(context.Background()) {
		t.Error("a rejected tame attempt was reported as a tame")
	}
}

// Two cows, two feeds, and no hearts, no baby, and no food consumed.
func TestBreedingRefusesToClaimSuccessItCannotSee(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withAnimal(1, "minecraft:cow").withAnimal(2, "minecraft:cow")
	b.items[0] = stack(1, 4)
	b.names[1] = "minecraft:wheat"

	m := husbandry.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if m.BreedAnimals(context.Background(), "cow") {
		t.Error("breeding reported success with nothing observed")
	}
	for _, r := range b.reports {
		if r.Success {
			t.Errorf("a successful breed was reported without an observation: %+v", r)
		}
	}
}

// The same two cows, but the server took the wheat: both feeds landed.
func TestBreedingIsConfirmedWhenTheFoodIsConsumed(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withAnimal(1, "minecraft:cow").withAnimal(2, "minecraft:cow")
	b.items[0] = stack(1, 4)
	b.names[1] = "minecraft:wheat"
	b.onInteract = func(call int) map[uint32]protocol.ItemStack {
		if call < 2 {
			return nil
		}
		return map[uint32]protocol.ItemStack{0: stack(1, 2)} // two wheat eaten
	}

	m := husbandry.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if !m.BreedAnimals(context.Background(), "cow") {
		t.Error("two feeds were consumed and breeding was not confirmed")
	}
}

// The cow is milked into a bucket, and the bucket is still a bucket afterwards.
func TestMilkCowRefusesToClaimSuccessItCannotSee(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withAnimal(1, "minecraft:cow")
	b.items[0] = stack(1, 1)
	b.names[1] = "minecraft:bucket"

	m := husbandry.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if m.MilkCow(context.Background()) {
		t.Error("milking reported success with no milk bucket in the inventory")
	}
}

func TestMilkCowConfirmsTheMilkBucket(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withAnimal(1, "minecraft:cow")
	b.items[0] = stack(1, 1)
	b.names[1] = "minecraft:bucket"
	b.onInteract = func(call int) map[uint32]protocol.ItemStack {
		return map[uint32]protocol.ItemStack{3: stack(2, 1)}
	}
	b.names[2] = "minecraft:milk_bucket"

	m := husbandry.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if !m.MilkCow(context.Background()) {
		t.Error("a milk bucket appeared and milking was not confirmed")
	}
}

func TestShearSheepRefusesToClaimSuccessItCannotSee(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withAnimal(1, "minecraft:sheep")
	b.items[0] = stack(1, 1)
	b.names[1] = "minecraft:shears"

	m := husbandry.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if m.ShearSheep(context.Background()) {
		t.Error("shearing reported success with no wool in the inventory")
	}
}

func TestShearSheepConfirmsTheWool(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withAnimal(1, "minecraft:sheep")
	b.items[0] = stack(1, 1)
	b.names[1] = "minecraft:shears"
	b.onInteract = func(call int) map[uint32]protocol.ItemStack {
		return map[uint32]protocol.ItemStack{0: stack(1, 1), 4: stack(2, 1)}
	}
	b.names[2] = "minecraft:white_wool"

	m := husbandry.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if !m.ShearSheep(context.Background()) {
		t.Error("a ball of wool appeared and shearing was not confirmed")
	}
}

// collaringMeta reports a wild wolf until the first attempt, then a collared
// one — the shape of a successful tame. after=1 would be a wolf that was
// already collared before the bot did anything, which is not a transition.
type collaringMeta struct {
	after int
	calls int
}

func (c *collaringMeta) EntityMeta(runtimeID uint64) (husbandry.EntityMeta, bool) {
	c.calls++
	if c.calls < c.after {
		return husbandry.EntityMeta{EntityType: "minecraft:wolf"}, true
	}
	return husbandry.EntityMeta{EntityType: "minecraft:wolf", Collared: true, Tamed: true}, true
}

// alwaysEvent replays one actor event for every query.
type alwaysEvent struct {
	typ uint8
}

func (a *alwaysEvent) ActorEventsSince(runtimeID uint64, since time.Time) []husbandry.ActorEvent {
	return []husbandry.ActorEvent{{RuntimeID: runtimeID, Type: a.typ, At: time.Now()}}
}
