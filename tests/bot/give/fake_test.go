package give_test

import (
	"errors"
	"io"
	"log/slog"
	"math"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// --- fake bot ---

// errBoom is a transport failure: the drop never left the bot. Reporting
// success from it is the bug this package exists to prevent.
type errBoom struct{}

func (errBoom) Error() string { return "connection closed" }

// fakeBot is a server the give flow can be pointed at, with the two behaviours
// that decide whether a give is confirmed or merely attempted:
//
//   - confirmDrop: the host spawns a dropped-item entity near the player, the
//     way a real one answers a DropStackRequest. This is the only signal that
//     distinguishes "the item is in the world" from "we sent a packet".
//   - confirmHandoff: the host removes the item again without it ever being
//     seen, which is what a fast pickup looks like.
//
// With both false the host accepts the request and says nothing, and a result
// claiming the player received the item is lying.
type fakeBot struct {
	mu sync.Mutex

	pos      mgl32.Vec3
	player   string
	playerAt mgl32.Vec3
	inv      map[uint32]protocol.ItemStack
	names    map[int32]string
	actors   map[uint64]*entity.Info
	solids   map[[3]int32]bool

	confirmDrop    bool
	confirmHandoff bool

	dropErr   error
	navTarget [3]int32
	navCalls  int
	lookAt    []mgl32.Vec3
	dropped   []string
	nextActor uint64
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		pos:       mgl32.Vec3{0, 64, 0},
		player:    "Steve",
		playerAt:  mgl32.Vec3{3, 64, 0},
		inv:       map[uint32]protocol.ItemStack{},
		names:     map[int32]string{7: "minecraft:diamond"},
		actors:    map[uint64]*entity.Info{},
		solids:    map[[3]int32]bool{},
		dropErr:   nil,
		nextActor: 100,
	}
}

func (b *fakeBot) GetCoords() mgl32.Vec3 { return b.pos }

func (b *fakeBot) GetHeldItemSlot() uint32 { return 0 }

func (b *fakeBot) GetEntities() map[uint64]*entity.Info {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[uint64]*entity.Info, len(b.actors))
	for id, info := range b.actors {
		cp := *info
		out[id] = &cp
	}
	return out
}

func (b *fakeBot) GetLocalWorldModel() entity.WorldModel { return b }

func (b *fakeBot) IsSolid(x, y, z int32) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.solids[[3]int32{x, y, z}]
}

func (b *fakeBot) SetSolid(x, y, z int32, solid bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.solids[[3]int32{x, y, z}] = solid
}

func (b *fakeBot) IsHazard(x, y, z int32) bool { return false }

func (b *fakeBot) SetHazard(x, y, z int32, hazard bool) {}

func (b *fakeBot) NavigateToBlock(x, y, z int32, tol float32) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.navCalls++
	b.navTarget = [3]int32{x, y, z}
	// A real navigation moves the body. Model that, or every assertion about
	// ending up within reach would pass for the wrong reason.
	b.pos = mgl32.Vec3{
		float32(x) + 0.5,
		float32(y),
		float32(z) + 0.5,
	}
	return true
}

func (b *fakeBot) StopMovement() {}

func (b *fakeBot) LookAt(p mgl32.Vec3) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lookAt = append(b.lookAt, p)
}

func (b *fakeBot) ResetLook() {}

func (b *fakeBot) SetLookAngles(yaw, pitch float32) {}

func (b *fakeBot) WaitForYawSync(yaw float32, timeout time.Duration) bool { return true }

func (b *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[uint32]protocol.ItemStack, len(b.inv))
	for slot, stack := range b.inv {
		out[slot] = stack
	}
	return out
}

func (b *fakeBot) GetItemNames() map[int32]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[int32]string, len(b.names))
	for id, name := range b.names {
		out[id] = name
	}
	return out
}

func (b *fakeBot) FindPlayer(username string) (uint64, mgl32.Vec3, bool) {
	if username == "" {
		return 0, mgl32.Vec3{}, false
	}
	return 2, b.playerAt, true
}

func (b *fakeBot) DropItem(name string, count int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dropErr != nil {
		return b.dropErr
	}
	b.dropped = append(b.dropped, name)
	// A real host answers the drop by spawning an item entity. Model exactly
	// that, and nothing else: if the fake invents a confirmation the
	// production code never receives, the test proves nothing.
	if b.confirmDrop {
		b.nextActor++
		b.actors[b.nextActor] = &entity.Info{
			ID:       b.nextActor,
			Type:     "minecraft:item",
			Name:     "minecraft:diamond",
			Position: b.playerAt,
			Health:   1,
		}
	}
	return nil
}

func (b *fakeBot) dropCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.dropped)
}

func (b *fakeBot) navCallCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.navCalls
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stockDiamond puts a diamond in the fake's inventory.
//
// The count is uint16 because that is what protocol.ItemStack.Count is; an
// int16 here is a compile error rather than a conversion, and silently
// converting would let a negative count into an unsigned field.
func (b *fakeBot) stockDiamond(count uint16) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inv[4] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 7}, Count: count}
}

var _ = errors.New
var _ = math.Abs
