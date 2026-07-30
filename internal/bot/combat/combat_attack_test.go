package combat

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type fakeBlockReader struct {
	loaded func(x, y, z int32) bool
}

func (r fakeBlockReader) GetBlockName(x, y, z int32) (string, bool) {
	if !r.loaded(x, y, z) {
		return "", false
	}
	return "minecraft:air", true
}

type fakeSolidity struct {
	solid func(x, y, z int32) bool
}

func (s fakeSolidity) IsSolid(x, y, z int32) bool      { return s.solid(x, y, z) }
func (s fakeSolidity) SetSolid(x, y, z int32, v bool)  {}
func (s fakeSolidity) IsHazard(x, y, z int32) bool     { return false }
func (s fakeSolidity) SetHazard(x, y, z int32, v bool) {}

type combatFakeBot struct {
	mu      sync.Mutex
	coords  mgl32.Vec3
	actors  map[uint64]*entity.Info
	players map[string]struct {
		id  uint64
		pos mgl32.Vec3
	}
	logger   *slog.Logger
	solidity entity.WorldModel
	blocks   fakeBlockReader
	packets  []packet.Packet
}

func newCombatFakeBot(origin mgl32.Vec3, loaded, solid func(x, y, z int32) bool) *combatFakeBot {
	return &combatFakeBot{
		coords: origin,
		actors: map[uint64]*entity.Info{},
		players: map[string]struct {
			id  uint64
			pos mgl32.Vec3
		}{},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		solidity: fakeSolidity{solid: solid},
		blocks:   fakeBlockReader{loaded: loaded},
	}
}

func (f *combatFakeBot) GetCoords() mgl32.Vec3 { f.mu.Lock(); defer f.mu.Unlock(); return f.coords }
func (f *combatFakeBot) GetEntities() map[uint64]*entity.Info {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[uint64]*entity.Info, len(f.actors))
	for id, info := range f.actors {
		copied := *info
		out[id] = &copied
	}
	return out
}
func (f *combatFakeBot) WritePacket(pk packet.Packet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.packets = append(f.packets, pk)
	return nil
}
func (f *combatFakeBot) NavigateTo(pos mgl32.Vec3)                                 {}
func (f *combatFakeBot) StopMovement()                                             {}
func (f *combatFakeBot) LookAt(pos mgl32.Vec3)                                     {}
func (f *combatFakeBot) InjectAIEvent(msg string)                                  {}
func (f *combatFakeBot) GetHeldItemSlot() uint32                                   { return 0 }
func (f *combatFakeBot) GetInventorySlots() map[uint32]protocol.ItemStack          { return nil }
func (f *combatFakeBot) GetItemNames() map[int32]string                            { return nil }
func (f *combatFakeBot) EquipItem(slot uint32) error                               { return nil }
func (f *combatFakeBot) SendChat(msg string)                                       {}
func (f *combatFakeBot) ReportActionStatus(user string, status event.ActionStatus) {}
func (f *combatFakeBot) GetEntityRuntimeID() uint64                                { return 999 }
func (f *combatFakeBot) GetLocalWorldModel() entity.WorldModel                     { return f.solidity }
func (f *combatFakeBot) GetBlockName(x, y, z int32) (string, bool) {
	return f.blocks.GetBlockName(x, y, z)
}
func (f *combatFakeBot) FindPlayer(username string) (uint64, mgl32.Vec3, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, p := range f.players {
		if equalFoldASCII(name, username) {
			return p.id, p.pos, true
		}
	}
	return 0, mgl32.Vec3{}, false
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func stripLoaded(minX, maxX int32) func(x, y, z int32) bool {
	return func(x, y, z int32) bool { return x >= minX && x <= maxX && y >= 64 && y <= 66 && z == 0 }
}

func noSolid(x, y, z int32) bool { return false }

func addCow(f *combatFakeBot, id uint64, x float32) {
	f.actors[id] = &entity.Info{ID: id, Type: "minecraft:cow", Name: "minecraft:cow", Position: mgl32.Vec3{x, 64, 0}, Health: 10}
}

func TestTick_EnemyInLOS_AttacksWithSwingSourceAttack(t *testing.T) {
	t.Parallel()
	f := newCombatFakeBot(mgl32.Vec3{0, 64, 0}, stripLoaded(-2, 8), noSolid)
	addCow(f, 5, 2)
	cm := NewCombatManager(f, f.logger)
	cm.EngageTarget(5)

	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.packets) < 2 {
		t.Fatalf("packets written = %d, want animate + transaction", len(f.packets))
	}
	animate, ok := f.packets[0].(*packet.Animate)
	if !ok {
		t.Fatalf("first packet = %T, want *packet.Animate", f.packets[0])
	}
	if animate.SwingSource != packet.AnimateSwingSourceAttack {
		t.Fatalf("Animate.SwingSource = %d, want AnimateSwingSourceAttack", animate.SwingSource)
	}
	if _, ok := f.packets[1].(*packet.InventoryTransaction); !ok {
		t.Fatalf("second packet = %T, want *packet.InventoryTransaction", f.packets[1])
	}
}

func TestTick_EnemyBehindWall_DoesNotAttack(t *testing.T) {
	t.Parallel()
	f := newCombatFakeBot(mgl32.Vec3{0, 64, 0}, stripLoaded(-2, 8), func(x, y, z int32) bool {
		return x == 1 && y == 65 // wall cell intersected by the eye-level ray
	})
	addCow(f, 5, 2)
	cm := NewCombatManager(f, f.logger)
	cm.EngageTarget(5)

	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.packets) != 0 {
		t.Fatalf("packets written through wall = %d, want 0", len(f.packets))
	}
}

func TestTick_EnemyThroughUnloadedCell_DoesNotAttack(t *testing.T) {
	t.Parallel()
	f := newCombatFakeBot(mgl32.Vec3{0, 64, 0}, func(x, y, z int32) bool {
		return stripLoaded(-2, 8)(x, y, z) && x != 1
	}, noSolid)
	addCow(f, 5, 2)
	cm := NewCombatManager(f, f.logger)
	cm.EngageTarget(5)

	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.packets) != 0 {
		t.Fatalf("packets written through unloaded cell = %d, want 0", len(f.packets))
	}
}

func TestEngagePlayer_TracksAndAttacksByName(t *testing.T) {
	t.Parallel()
	f := newCombatFakeBot(mgl32.Vec3{0, 64, 0}, stripLoaded(-2, 8), noSolid)
	f.players["Steve"] = struct {
		id  uint64
		pos mgl32.Vec3
	}{id: 42, pos: mgl32.Vec3{2, 64, 0}}
	cm := NewCombatManager(f, f.logger)

	if !cm.EngagePlayer("steve") {
		t.Fatal("EngagePlayer(steve) = false, want tracked player")
	}
	if got := fmt.Sprint(cm.pvpTarget); got != "steve" {
		t.Fatalf("pvpTarget = %q, want steve", got)
	}

	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.packets) < 2 {
		t.Fatalf("PVP packets = %d, want animate + transaction", len(f.packets))
	}
	tx := f.packets[1].(*packet.InventoryTransaction)
	data, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData)
	if !ok {
		t.Fatalf("transaction data = %T, want UseItemOnEntityTransactionData", tx.TransactionData)
	}
	if data.TargetEntityRuntimeID != 42 {
		t.Fatalf("attack target = %d, want player runtime ID 42", data.TargetEntityRuntimeID)
	}
}

func TestEngagePlayer_UnknownPlayerRejected(t *testing.T) {
	t.Parallel()
	f := newCombatFakeBot(mgl32.Vec3{0, 64, 0}, stripLoaded(-2, 8), noSolid)
	cm := NewCombatManager(f, f.logger)
	if cm.EngagePlayer("ghost") {
		t.Fatal("EngagePlayer(ghost) = true, want false for untracked player")
	}
	if cm.InCombat() {
		t.Fatal("InCombat() = true after failed player engagement")
	}
}
