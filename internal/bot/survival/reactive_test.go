package survival

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// fakeBot is a minimal survival.Bot. It records nothing about the actions,
// because these tests exercise the decisions: the actions walk and block for
// seconds, and a test that ran them would take a minute per case while
// asserting on side effects rather than on the judgement.
type fakeBot struct {
	busy      bool
	blocks    map[protocol.BlockPos]string
	inventory map[uint32]protocol.ItemStack
	names     map[int32]string
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		blocks:    make(map[protocol.BlockPos]string),
		inventory: make(map[uint32]protocol.ItemStack),
		names:     make(map[int32]string),
	}
}

func (f *fakeBot) GetCoords() mgl32.Vec3                             { return mgl32.Vec3{} }
func (f *fakeBot) WritePacket(pk packet.Packet) error                { return nil }
func (f *fakeBot) GetEntities() map[uint64]*entity.Info              { return nil }
func (f *fakeBot) NavigateTo(pos mgl32.Vec3)                         {}
func (f *fakeBot) NavigateToBlock(x, y, z int32, tol float32) bool   { return true }
func (f *fakeBot) StopMovement()                                     {}
func (f *fakeBot) LookAt(pos mgl32.Vec3)                             {}
func (f *fakeBot) InjectAIEvent(msg string)                          {}
func (f *fakeBot) GetHeldItemSlot() uint32                           { return 0 }
func (f *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack  { return f.inventory }
func (f *fakeBot) GetItemNames() map[int32]string                    { return f.names }
func (f *fakeBot) EquipItem(slot uint32) error                       { return nil }
func (f *fakeBot) UnequipItem() error                                { return nil }
func (f *fakeBot) SendChat(msg string)                               {}
func (f *fakeBot) ReportActionStatus(u string, s event.ActionStatus) {}
func (f *fakeBot) GetEntityRuntimeID() uint64                        { return 1 }
func (f *fakeBot) GetLocalWorldModel() entity.WorldModel             { return nil }
func (f *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	n, ok := f.blocks[protocol.BlockPos{x, y, z}]
	return n, ok
}
func (f *fakeBot) IsBusy() bool { return f.busy }

func newTestManager(b *fakeBot) *Manager {
	return NewManager(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// withBed puts a bed on the floor beside the bot, which is what
// FindNearbyBed looks for.
func withBed(b *fakeBot) {
	origin := b.GetCoords()
	b.blocks[protocol.BlockPos{
		int32(origin.X()) + 1,
		int32(origin.Y()),
		int32(origin.Z()),
	}] = "minecraft:red_bed"
}

// TestNightDoesNothingInDaylight is the obvious guard, written down because it
// is the one a reader is most likely to doubt.
func TestNightDoesNothingInDaylight(t *testing.T) {
	t.Parallel()

	m := newTestManager(newFakeBot())
	m.SetWorldTime(2000) // midday

	if got := m.PlanNight(); got != NightDoNothing {
		t.Errorf("PlanNight in broad daylight = %v, want NightDoNothing", got)
	}
}

// TestWaitsUntilProperNightfall stops the bot running to bed the instant the sky
// turns. The isNight flag starts at dusk; a player does not.
func TestWaitsUntilProperNightfall(t *testing.T) {
	t.Parallel()

	m := newTestManager(newFakeBot())
	m.SetWorldTime(13000) // isNight is true, earlier than nightStartTicks

	if got := m.PlanNight(); got != NightDoNothing {
		t.Errorf("PlanNight at dusk = %v, want NightDoNothing until night properly falls", got)
	}
}

// TestSleepsWhenThereIsABed is the behaviour the config advertised and nothing
// ever performed.
func TestSleepsWhenThereIsABed(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	withBed(b)
	m := newTestManager(b)
	m.SetWorldTime(15000) // proper night

	if got := m.PlanNight(); got != NightSleep {
		t.Errorf("PlanNight with a bed available = %v, want NightSleep", got)
	}
}

// TestSheltersWhenThereIsNoBed is the other branch of the same decision.
func TestSheltersWhenThereIsNoBed(t *testing.T) {
	t.Parallel()

	m := newTestManager(newFakeBot())
	m.SetWorldTime(15000)

	if got := m.PlanNight(); got != NightShelter {
		t.Errorf("PlanNight at night with no bed = %v, want NightShelter", got)
	}
}

// TestDoesNotWallItselfInWhenItOwnsABed encodes the judgement that stops the bot
// building a shelter around a bed it already has.
func TestDoesNotWallItselfInWhenItOwnsABed(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	withBed(b)
	m := newTestManager(b)
	m.SetWorldTime(15000)

	got := m.PlanNight()
	if got == NightShelter {
		t.Error("chose to shelter despite owning a bed")
	}
}

// TestNightDefersWhenBusy keeps the reactive loop from interrupting the brain's
// own plan. A bot that abandons a task to go to bed half-way through looks like
// it cannot hold a thought.
func TestNightDefersWhenBusy(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.busy = true
	withBed(b)
	m := newTestManager(b)
	m.SetWorldTime(15000)

	if got := m.PlanNight(); got != NightDoNothing {
		t.Errorf("PlanNight while busy = %v, want NightDoNothing", got)
	}
}

// TestNightIsCooldowned stops the loop hammering an interaction that can fail
// for reasons the bot cannot control. Without a cooldown it retries every 500ms
// forever, which on some servers looks like a bot attacking a bed.
func TestNightIsCooldowned(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	withBed(b)
	m := newTestManager(b)
	m.SetWorldTime(15000)

	first := m.PlanNight()
	second := m.PlanNight()
	if first != NightSleep {
		t.Fatalf("first plan = %v, want NightSleep", first)
	}
	if second != NightDoNothing {
		t.Errorf("second plan = %v, want NightDoNothing inside the cooldown", second)
	}
}

// TestDisablingAutoSleepFallsBackToShelter makes sure the two flags are
// independent, since a server owner may want one without the other.
func TestDisablingAutoSleepFallsBackToShelter(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	withBed(b)
	m := newTestManager(b)
	m.SetWorldTime(15000)
	m.AutoSleepEnabled = false

	// With a bed but no auto-sleep, it should not sleep — and should not build a
	// shelter over its own bed either.
	if got := m.PlanNight(); got != NightDoNothing {
		t.Errorf("PlanNight with auto-sleep off and a bed present = %v, want NightDoNothing", got)
	}
}

// TestDeathRecoveryIgnoresBusy is the central rule: no version of "busy"
// outranks your own corpse, and a bot that walks away from what it dropped has
// thrown away everything it was carrying.
func TestDeathRecoveryIgnoresBusy(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.busy = true
	m := newTestManager(b)
	m.MarkDeath(mgl32.Vec3{1, 2, 3})
	m.mu.Lock()
	m.deathTime = time.Now().Add(-time.Minute) // backdate past the respawn delay
	m.mu.Unlock()

	if !m.ShouldRecoverDeath() {
		t.Error("death recovery refused while the bot was busy; it must interrupt")
	}
}

// TestDeathRecoveryWaitsForRespawn stops the bot acting on a death it has not
// finished respawning from.
func TestDeathRecoveryWaitsForRespawn(t *testing.T) {
	t.Parallel()

	m := newTestManager(newFakeBot())
	m.MarkDeath(mgl32.Vec3{1, 2, 3}) // deathTime = now, inside the delay

	if m.ShouldRecoverDeath() {
		t.Error("death recovery fired before the respawn delay elapsed")
	}
}

// TestNoDeathNoRecovery is the null case.
func TestNoDeathNoRecovery(t *testing.T) {
	t.Parallel()

	m := newTestManager(newFakeBot())
	if m.ShouldRecoverDeath() {
		t.Error("death recovery fired with no death recorded")
	}
}

// TestTorchNeedsTorchInInventory keeps the bot from announcing it is placing a
// torch it does not have. The cooldown alone would still let it log a lie every
// 45 seconds.
func TestTorchNeedsTorchInInventory(t *testing.T) {
	t.Parallel()

	m := newTestManager(newFakeBot())
	m.SetWorldTime(15000) // night

	if m.ShouldPlaceTorch() {
		t.Error("wanted to place a torch while carrying none")
	}
}

// TestTorchGoesOutAtNightWhenCarried is the behaviour itself.
func TestTorchGoesOutAtNightWhenCarried(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.inventory[0] = protocol.ItemStack{Count: 10}
	b.names[0] = "torch"
	m := newTestManager(b)
	m.SetWorldTime(15000)

	if !m.ShouldPlaceTorch() {
		t.Error("wanted to place a torch at night while carrying one, but declined")
	}
}

// TestNoTorchInDaylight is the obvious guard.
func TestNoTorchInDaylight(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.inventory[0] = protocol.ItemStack{Count: 10}
	b.names[0] = "torch"
	m := newTestManager(b)
	m.SetWorldTime(2000)

	if m.ShouldPlaceTorch() {
		t.Error("wanted to place a torch in broad daylight")
	}
}

// TestTorchIsCooldowned stops the bot from scattering torches, which is the
// failure mode the long cooldown exists to prevent.
func TestTorchIsCooldowned(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.inventory[0] = protocol.ItemStack{Count: 10}
	b.names[0] = "torch"
	m := newTestManager(b)
	m.SetWorldTime(15000)

	if !m.ShouldPlaceTorch() {
		t.Fatal("first torch was refused")
	}
	if m.ShouldPlaceTorch() {
		t.Error("a second torch was placed inside the cooldown window")
	}
}

// TestTorchDefersWhenBusy keeps self-care from interrupting work.
func TestTorchDefersWhenBusy(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.busy = true
	b.inventory[0] = protocol.ItemStack{Count: 10}
	b.names[0] = "torch"
	m := newTestManager(b)
	m.SetWorldTime(15000)

	if m.ShouldPlaceTorch() {
		t.Error("placed a torch while busy")
	}
}
