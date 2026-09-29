package chest

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// fakeBot is a chest.Bot that knows about block names and nothing else. The
// tests here are about which block the bot decides is a chest, so the fake only
// has to answer that question.
type fakeBot struct {
	pos    mgl32.Vec3
	blocks map[protocol.BlockPos]string
	world  *fakeWorld
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		// Standing on the ground at y=64, which is where every fixture places
		// its container.
		pos:    mgl32.Vec3{0.5, 64, 0.5},
		blocks: make(map[protocol.BlockPos]string),
		world:  &fakeWorld{},
	}
}

func (f *fakeBot) GetCoords() mgl32.Vec3 { return f.pos }

func (f *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	name, ok := f.blocks[protocol.BlockPos{x, y, z}]
	return name, ok
}

func (f *fakeBot) GetLocalWorldModel() entity.WorldModel { return f.world }

func (f *fakeBot) GetEntities() map[uint64]*entity.Info   { return nil }
func (f *fakeBot) WritePacket(pk packet.Packet) error    { return nil }
func (f *fakeBot) NavigateTo(pos mgl32.Vec3)             {}
func (f *fakeBot) NavigateToBlock(x, y, z int32, tolerance float32) bool {
	return true
}
func (f *fakeBot) StopMovement()                   {}
func (f *fakeBot) LookAt(pos mgl32.Vec3)           {}
func (f *fakeBot) InjectAIEvent(msg string)        {}
func (f *fakeBot) GetHeldItemSlot() uint32         { return 0 }
func (f *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack {
	return nil
}
func (f *fakeBot) GetItemNames() map[int32]string                   { return nil }
func (f *fakeBot) EquipItem(slot uint32) error                     { return nil }
func (f *fakeBot) UnequipItem() error                              { return nil }
func (f *fakeBot) SendChat(msg string)                             {}
func (f *fakeBot) GetEntityRuntimeID() uint64                      { return 1 }
func (f *fakeBot) DropItem(name string, count int) error           { return nil }
func (f *fakeBot) FindPlayer(username string) (uint64, mgl32.Vec3, bool) {
	return 0, mgl32.Vec3{}, false
}
func (f *fakeBot) SetLookAngles(yaw, pitch float32)   {}
func (f *fakeBot) WaitForYawSync(targetYaw float32, timeout time.Duration) bool {
	return true
}
func (f *fakeBot) AimAtPlayerForDrop(target string, pitch float32) (float32, bool) {
	return 0, false
}
func (f *fakeBot) OverrideLookPitch(pitch float32) {}
func (f *fakeBot) ResetLook()                      {}

// fakeWorld reports a single solid block, which is enough to prove the finder
// no longer treats "solid" as "chest".
type fakeWorld struct {
	solid map[protocol.BlockPos]bool
}

func (w *fakeWorld) IsSolid(x, y, z int32) bool { return w.solid[protocol.BlockPos{x, y, z}] }
func (w *fakeWorld) SetSolid(x, y, z int32, solid bool) {
	if w.solid == nil {
		w.solid = make(map[protocol.BlockPos]bool)
	}
	w.solid[protocol.BlockPos{x, y, z}] = solid
}
func (w *fakeWorld) IsHazard(x, y, z int32) bool { return false }
func (w *fakeWorld) SetHazard(x, y, z int32, hazard bool) {}

func newTestContainer(b *fakeBot) *Container {
	return NewContainer(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestFindNearbyChestFindsAChestByName is the regression for the old finder,
// which returned the first solid block it touched — a wall, a floor, the ground
// under the bot's feet — and then tried to open it.
func TestFindNearbyChestFindsAChestByName(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	// Solid terrain everywhere, none of it a chest.
	b.world.SetSolid(0, 63, 0, true)
	b.world.SetSolid(0, 64, 0, true)
	b.world.SetSolid(0, 64, 1, true)
	// A chest, three blocks away.
	b.blocks[protocol.BlockPos{3, 64, 0}] = "minecraft:chest"

	ic := newTestContainer(b)

	got := ic.findNearbyChest()
	want := protocol.BlockPos{3, 64, 0}
	if got != want {
		t.Errorf("findNearbyChest = %v, want the chest at %v", got, want)
	}
}

func TestFindNearbyChestPrefersTheNearestChest(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[protocol.BlockPos{1, 64, 0}] = "minecraft:chest"
	b.blocks[protocol.BlockPos{5, 64, 0}] = "minecraft:chest"

	ic := newTestContainer(b)

	if got, want := ic.findNearbyChest(), (protocol.BlockPos{1, 64, 0}); got != want {
		t.Errorf("findNearbyChest = %v, want the nearer chest at %v", got, want)
	}
}

func TestFindNearbyChestAcceptsOtherContainers(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[protocol.BlockPos{2, 64, 0}] = "minecraft:barrel"

	ic := newTestContainer(b)

	if got, want := ic.findNearbyChest(), (protocol.BlockPos{2, 64, 0}); got != want {
		t.Errorf("findNearbyChest = %v, want the barrel at %v", got, want)
	}
}

// TestFindNearbyChestReturnsNothingWhenThereIsNoChest is the case the old
// finder could not produce at all: a world full of stone and no chest must not
// report a chest.
func TestFindNearbyChestReturnsNothingWhenThereIsNoChest(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	for x := -2; x <= 2; x++ {
		for y := 62; y <= 66; y++ {
			for z := -2; z <= 2; z++ {
				b.world.SetSolid(int32(x), int32(y), int32(z), true)
			}
		}
	}

	ic := newTestContainer(b)

	if got := ic.findNearbyChest(); got != (protocol.BlockPos{}) {
		t.Errorf("findNearbyChest = %v, want the zero position when only stone is around", got)
	}
}
