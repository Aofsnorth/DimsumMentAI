package combat

import (
	"context"
	"testing"
	"time"

	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// rangedFakeBot is the combat fake plus an actual inventory, so the shot
// path has a weapon and ammunition to work with.
type rangedFakeBot struct {
	*combatFakeBot
	slots map[uint32]protocol.ItemStack
	names map[int32]string
}

func (f *rangedFakeBot) GetInventorySlots() map[uint32]protocol.ItemStack { return f.slots }
func (f *rangedFakeBot) GetItemNames() map[int32]string                   { return f.names }

func newRangedFakeBot(origin mgl32.Vec3) *rangedFakeBot {
	return &rangedFakeBot{
		combatFakeBot: newCombatFakeBot(origin, stripLoaded(-40, 40), noSolid),
		slots: map[uint32]protocol.ItemStack{
			0: {ItemType: protocol.ItemType{NetworkID: 1}, Count: 1},
			1: {ItemType: protocol.ItemType{NetworkID: 2}, Count: 16},
		},
		names: map[int32]string{1: "minecraft:bow", 2: "minecraft:arrow"},
	}
}

func addSkeleton(f *combatFakeBot, id uint64, x float32) {
	f.actors[id] = &entity.Info{ID: id, Type: "minecraft:skeleton", Name: "minecraft:skeleton", Position: mgl32.Vec3{x, 64, 0}, Health: 20}
}

func TestBowAimPoint_LiftsTheAimByDistance(t *testing.T) {
	t.Parallel()
	bot := mgl32.Vec3{0, 64, 0}
	target := mgl32.Vec3{20, 64, 0}

	near := bowAimPoint(bot, mgl32.Vec3{2, 64, 0})
	far := bowAimPoint(bot, target)

	// The aim point is the body's centre, lifted.
	if near.Y() <= 65.2 {
		t.Fatalf("near aim height = %v, want at least the 65.2 body centre", near.Y())
	}
	// Gravity pulls the arrow down over the whole flight, so the correction
	// must grow with distance. At 20 blocks a full-draw arrow falls over a
	// block, which is why a shot aimed dead level would land short.
	if far.Y() <= near.Y() {
		t.Fatalf("far aim height = %v, want above the near aim height %v", far.Y(), near.Y())
	}
	if drop := far.Y() - 65.2; drop < 0.5 || drop > 2.0 {
		t.Fatalf("drop at 20 blocks = %v, want a plausible 0.5-2.0 block correction", drop)
	}
}

func TestPlanShot_Decisions(t *testing.T) {
	t.Parallel()
	now := time.Now()

	cases := []struct {
		name      string
		kind      WeaponKind
		hasArrows bool
		state     shot
		want      ShotAction
	}{
		{"melee weapon never shoots", WeaponSword, true, shot{}, ShotNone},
		{"bow without arrows is a stick", WeaponBow, false, shot{}, ShotNone},
		{"fresh bow with arrows draws", WeaponBow, true, shot{}, ShotDraw},
		{"drawing bow holds until full draw", WeaponBow, true, shot{drawStart: now.Add(-500 * time.Millisecond)}, ShotNone},
		{"full draw releases", WeaponBow, true, shot{drawStart: now.Add(-1200 * time.Millisecond)}, ShotFire},
		{"cooldown after release", WeaponBow, true, shot{lastShot: now.Add(-500 * time.Millisecond)}, ShotNone},
		{"draw again once the interval passes", WeaponBow, true, shot{lastShot: now.Add(-2 * time.Second)}, ShotDraw},
		{"unloaded crossbow needs its load time", WeaponCrossbow, false, shot{drawStart: now.Add(-500 * time.Millisecond)}, ShotNone},
		{"unloaded crossbow fires after loading", WeaponCrossbow, false, shot{drawStart: now.Add(-1300 * time.Millisecond)}, ShotFire},
		{"loaded crossbow fires at once", WeaponCrossbow, false, shot{drawStart: now.Add(-1 * time.Millisecond), loaded: true}, ShotFire},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := planShot(tc.kind, tc.hasArrows, now, tc.state); got != tc.want {
				t.Fatalf("planShot() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTick_BowAtRange_DrawsThenReleases(t *testing.T) {
	t.Parallel()
	f := newRangedFakeBot(mgl32.Vec3{0, 64, 0})
	addSkeleton(f.combatFakeBot, 7, 15) // beyond the 8-block ranged threshold
	cm := NewCombatManager(f, f.logger)
	cm.EngageTarget(7)

	cm.Tick(context.Background())

	f.mu.Lock()
	if len(f.packets) != 1 {
		f.mu.Unlock()
		t.Fatalf("packets after first tick = %d, want the draw transaction", len(f.packets))
	}
	draw, ok := f.packets[0].(*packet.InventoryTransaction)
	if !ok {
		f.mu.Unlock()
		t.Fatalf("first packet = %T, want *packet.InventoryTransaction", f.packets[0])
	}
	use, ok := draw.TransactionData.(*protocol.UseItemTransactionData)
	if !ok {
		f.mu.Unlock()
		t.Fatalf("draw data = %T, want UseItemTransactionData", draw.TransactionData)
	}
	if use.ActionType != protocol.UseItemActionClickAir {
		f.mu.Unlock()
		t.Fatalf("draw action = %d, want UseItemActionClickAir", use.ActionType)
	}
	f.mu.Unlock()

	// Simulate the full draw having elapsed, then release.
	cm.mu.Lock()
	cm.shot.drawStart = time.Now().Add(-2 * time.Second)
	cm.mu.Unlock()

	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.packets) != 2 {
		t.Fatalf("packets after release tick = %d, want draw + release", len(f.packets))
	}
	release, ok := f.packets[1].(*packet.InventoryTransaction)
	if !ok {
		t.Fatalf("second packet = %T, want *packet.InventoryTransaction", f.packets[1])
	}
	rel, ok := release.TransactionData.(*protocol.ReleaseItemTransactionData)
	if !ok {
		t.Fatalf("release data = %T, want ReleaseItemTransactionData", release.TransactionData)
	}
	if rel.ActionType != protocol.ReleaseItemActionRelease {
		t.Fatalf("release action = %d, want ReleaseItemActionRelease", rel.ActionType)
	}
	if rel.HotBarSlot != 0 {
		t.Fatalf("release slot = %d, want the bow slot 0", rel.HotBarSlot)
	}
}

func TestTick_BowWithoutArrows_NeverDraws(t *testing.T) {
	t.Parallel()
	f := newRangedFakeBot(mgl32.Vec3{0, 64, 0})
	delete(f.slots, 1) // no arrows left
	addSkeleton(f.combatFakeBot, 7, 15)
	cm := NewCombatManager(f, f.logger)
	cm.EngageTarget(7)

	cm.Tick(context.Background())
	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, pk := range f.packets {
		tx, ok := pk.(*packet.InventoryTransaction)
		if !ok {
			continue
		}
		if _, isUse := tx.TransactionData.(*protocol.UseItemTransactionData); isUse {
			t.Fatalf("bow drawn without arrows: %T", tx.TransactionData)
		}
	}
}
