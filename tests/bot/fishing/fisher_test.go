package fishing_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/fishing"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeBot is a scripted world: a water tile the bot walks to, a rod in the hotbar,
// and a bobber whose position the test walks forward one step per observation.
type fakeBot struct {
	pos    mgl32.Vec3
	blocks map[[3]int32]string
	items  map[uint32]protocol.ItemStack
	names  map[int32]string
	ents   map[uint64]*entity.Info

	// bobberPositions is walked one entry per GetEntities call. Once it runs
	// out the last position is held, which is how "the bobber never moved"
	// is scripted.
	bobberPositions []mgl32.Vec3
	bobberType      string
	bobberID        uint64
	polls           int

	// onRodUse runs every time the bot uses the rod, which is where a test
	// decides whether a fish actually shows up in the inventory.
	onRodUse func(use int) map[uint32]protocol.ItemStack

	reports []event.ActionStatus
	uses    int
	equips  []uint32
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		pos:        mgl32.Vec3{0, 62, 0},
		blocks:     map[[3]int32]string{},
		items:      map[uint32]protocol.ItemStack{},
		names:      map[int32]string{1: "minecraft:fishing_rod"},
		ents:       map[uint64]*entity.Info{},
		bobberType: "minecraft:fishing_hook",
		bobberID:   7,
	}
}

func (f *fakeBot) withWater() *fakeBot {
	f.blocks[[3]int32{4, 61, 4}] = "minecraft:water"
	return f
}

func (f *fakeBot) withRod() *fakeBot {
	f.items[0] = stack(1, 1)
	return f
}

func (f *fakeBot) GetCoords() mgl32.Vec3 { return f.pos }

func (f *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	name, ok := f.blocks[[3]int32{x, y, z}]
	return name, ok
}

func (f *fakeBot) NavigateTo(pos mgl32.Vec3) {}

func (f *fakeBot) NavigateToBlock(x, y, z int32, tolerance float32) bool { return true }

func (f *fakeBot) StopMovement()         {}
func (f *fakeBot) LookAt(pos mgl32.Vec3) {}
func (f *fakeBot) SendChat(msg string)   {}

func (f *fakeBot) GetHeldItemSlot() uint32 { return 0 }

func (f *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack { return f.items }

func (f *fakeBot) GetItemNames() map[int32]string { return f.names }

func (f *fakeBot) EquipItem(slot uint32) error {
	f.equips = append(f.equips, slot)
	return nil
}

func (f *fakeBot) ReportActionStatus(user string, status event.ActionStatus) {
	f.reports = append(f.reports, status)
}

func (f *fakeBot) GetEntityRuntimeID() uint64            { return 1 }
func (f *fakeBot) GetLocalWorldModel() entity.WorldModel { return nil }

// WritePacket counts rod uses. The second use of a cast line is the reel, so
// the fake uses the count both to script the world and to report what the bot
// actually did.
func (f *fakeBot) WritePacket(pk packet.Packet) error {
	tx, ok := pk.(*packet.InventoryTransaction)
	if !ok {
		return nil
	}
	if _, ok := tx.TransactionData.(*protocol.UseItemTransactionData); !ok {
		return nil
	}
	f.uses++
	if f.onRodUse != nil {
		if next := f.onRodUse(f.uses); next != nil {
			f.items = next
		}
	}
	return nil
}

// GetEntities advances the bobber script by one step per observation, which is
// the only thing the fisherman polls while waiting for a bite.
func (f *fakeBot) GetEntities() map[uint64]*entity.Info {
	out := make(map[uint64]*entity.Info, len(f.ents))
	for id, e := range f.ents {
		out[id] = e
	}
	if f.bobberType == "" {
		return out
	}
	pos := mgl32.Vec3{}
	if len(f.bobberPositions) > 0 {
		idx := f.polls
		if idx >= len(f.bobberPositions) {
			idx = len(f.bobberPositions) - 1
		}
		pos = f.bobberPositions[idx]
	}
	f.polls++
	out[f.bobberID] = &entity.Info{ID: f.bobberID, Type: f.bobberType, Position: pos, Health: 1}
	return out
}

func fastTimings() fishing.Timings {
	return fishing.Timings{
		PostCast:  time.Millisecond,
		BiteWatch: 200 * time.Millisecond,
		CatchWait: 200 * time.Millisecond,
		Poll:      5 * time.Millisecond,
		Between:   time.Millisecond,
	}
}

// A bite the bot can see: the bobber drops and closes. A fish appears when the
// line is reeled. Both must be true before a catch is counted.
func TestGoFishCountsOnlyBitesItReeled(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withWater().withRod()
	// Cast from the bank; the bobber lands five blocks out, then bites.
	b.bobberPositions = []mgl32.Vec3{
		{5, 61.5, 0},   // floating
		{5, 61.5, 0},   // still floating
		{4.5, 61.2, 0}, // bit
	}
	b.onRodUse = func(use int) map[uint32]protocol.ItemStack {
		if use != 2 {
			return nil // the cast; nothing caught yet
		}
		return map[uint32]protocol.ItemStack{0: stack(1, 1), 2: stack(3, 1)}
	}
	b.names[3] = "minecraft:cod"

	f := fishing.NewFisher(b, quietLogger())
	f.SetTimings(fastTimings())

	if got := f.GoFish(context.Background(), 1); got != 1 {
		t.Errorf("GoFish = %d, want 1: the bobber bit and a cod landed", got)
	}
	if b.items[2].Count != 1 {
		t.Error("the catch was counted but no fish is actually in the inventory")
	}
}

// No bite, no catch. The old implementation reeled on a timer and reported a
// fish; here the bot must come back empty even though a fish is sitting in the
// inventory the whole time, because nothing was reeled.
func TestGoFishReturnsNothingWithoutABite(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withWater().withRod()
	b.bobberPositions = []mgl32.Vec3{{5, 61.5, 0}} // never moves
	b.onRodUse = func(use int) map[uint32]protocol.ItemStack {
		// A cod is dropped in regardless of what the bot does.
		return map[uint32]protocol.ItemStack{0: stack(1, 1), 2: stack(3, 1)}
	}
	b.names[3] = "minecraft:cod"

	f := fishing.NewFisher(b, quietLogger())
	f.SetTimings(fastTimings())

	if got := f.GoFish(context.Background(), 3); got != 0 {
		t.Errorf("GoFish = %d, want 0: the bobber never bit, so nothing was reeled", got)
	}
	for _, r := range b.reports {
		if r.Success && r.Count > 0 {
			t.Errorf("reported a successful catch with no bite: %+v", r)
		}
	}
}

// A bite that produces nothing must not be counted. The bobber drops, the bot
// reels on the bite, and then no fish ever appears.
func TestABiteWithNoCatchIsNotCounted(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withWater().withRod()
	b.bobberPositions = []mgl32.Vec3{{5, 61.5, 0}, {4.5, 61.2, 0}}
	b.onRodUse = nil // the server never gives anything back

	f := fishing.NewFisher(b, quietLogger())
	f.SetTimings(fastTimings())

	if got := f.GoFish(context.Background(), 1); got != 0 {
		t.Errorf("GoFish = %d, want 0: a bite with no fish in the inventory is not a catch", got)
	}
	if len(b.reports) == 0 {
		t.Error("an unconfirmed fishing trip reported nothing at all")
	}
}

// With no water there is nothing to cast at, and the bot must say so rather than
// cast into the void.
func TestGoFishWithoutWaterReportsFailure(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withRod()
	f := fishing.NewFisher(b, quietLogger())
	f.SetTimings(fastTimings())

	if got := f.GoFish(context.Background(), 1); got != 0 {
		t.Errorf("GoFish = %d with no water nearby, want 0", got)
	}
	if len(b.reports) == 0 || b.reports[0].Success {
		t.Errorf("expected a failed status, got %+v", b.reports)
	}
}

// The bobber may be entirely unobservable: no actor is ever tracked for it. In
// that case the motion path has nothing to compare and the bite must come from
// the actor-event seam instead.
func TestBiteCanComeFromTheTeaseEventAlone(t *testing.T) {
	t.Parallel()

	b := newFakeBot().withWater().withRod()
	b.bobberType = "" // never observed as an entity
	b.onRodUse = func(use int) map[uint32]protocol.ItemStack {
		if use != 2 {
			return nil
		}
		return map[uint32]protocol.ItemStack{0: stack(1, 1), 2: stack(3, 1)}
	}
	b.names[3] = "minecraft:cod"

	events := &scriptedEvents{after: map[int]bool{1: true}}
	f := fishing.NewFisher(b, quietLogger())
	f.SetTimings(fastTimings())
	f.SetActorEventSource(events)

	if got := f.GoFish(context.Background(), 1); got != 1 {
		t.Errorf("GoFish = %d, want 1: the tease event is a bite even with no tracked bobber", got)
	}
}

// scriptedEvents hands out a tease event on the first poll after the cast. It
// answers for any runtime ID, which is what the fisherman asks for when it has
// never seen the bobber as a tracked actor.
type scriptedEvents struct {
	after map[int]bool
	polls int
}

func (s *scriptedEvents) ActorEventsSince(runtimeID uint64, since time.Time) []fishing.ActorEvent {
	s.polls++
	if !s.after[s.polls] {
		return nil
	}
	return []fishing.ActorEvent{{RuntimeID: runtimeID, Type: fishing.ActorEventFishhookTease, At: time.Now()}}
}
