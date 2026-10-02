package bucket_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot/bucket"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func stack(networkID int32, count uint16) protocol.ItemStack {
	return protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: networkID}, Count: count}
}

// fakeBot is a world with one pond, one cauldron, and a bucket in the hotbar.
// Its `onUse` hook is the server: it decides what the world looks like after
// the bot clicks a block, which is exactly the thing a confirmation has to
// notice.
type fakeBot struct {
	pos    mgl32.Vec3
	blocks map[[3]int32]string
	rids   map[[3]int32]uint32
	items  map[uint32]protocol.ItemStack
	names  map[int32]string
	ents   map[uint64]*entity.Info

	onUse   func(used protocol.BlockPos)
	uses    []protocol.BlockPos
	reports []event.ActionStatus
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		pos:    mgl32.Vec3{0, 64, 0},
		blocks: map[[3]int32]string{},
		rids:   map[[3]int32]uint32{},
		items:  map[uint32]protocol.ItemStack{},
		names:  map[int32]string{},
		ents:   map[uint64]*entity.Info{},
	}
}

func (f *fakeBot) GetCoords() mgl32.Vec3 { return f.pos }

func (f *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	name, ok := f.blocks[[3]int32{x, y, z}]
	return name, ok
}

func (f *fakeBot) GetBlockNetworkID(x, y, z int32) (uint32, bool) {
	rid, ok := f.rids[[3]int32{x, y, z}]
	return rid, ok
}

func (f *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack { return f.items }

func (f *fakeBot) GetItemNames() map[int32]string { return f.names }

func (f *fakeBot) GetHeldItemSlot() uint32 { return 0 }

func (f *fakeBot) GetEntities() map[uint64]*entity.Info { return f.ents }

func (f *fakeBot) NavigateToBlock(x, y, z int32, tolerance float32) bool { return true }

func (f *fakeBot) StopMovement()         {}
func (f *fakeBot) LookAt(pos mgl32.Vec3) {}
func (f *fakeBot) ResetLook()            {}

func (f *fakeBot) EquipItem(slot uint32) error { return nil }

func (f *fakeBot) ReportActionStatus(user string, status event.ActionStatus) {
	f.reports = append(f.reports, status)
}

func (f *fakeBot) GetEntityRuntimeID() uint64 { return 1 }

func (f *fakeBot) WritePacket(pk packet.Packet) error {
	tx, ok := pk.(*packet.InventoryTransaction)
	if !ok {
		return nil
	}
	data, ok := tx.TransactionData.(*protocol.UseItemTransactionData)
	if !ok {
		return nil
	}
	f.uses = append(f.uses, data.BlockPosition)
	if f.onUse != nil {
		f.onUse(data.BlockPosition)
	}
	return nil
}

func fastTimings() bucket.Timings {
	return bucket.Timings{Aim: time.Millisecond, Confirm: 60 * time.Millisecond, Poll: 5 * time.Millisecond}
}

// Filling from water: the bucket changes AND the source tile changes. Both.
func TestFillBucketConfirmsBothTheBucketAndTheBlock(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{2, 63, 0}] = "minecraft:water"
	b.rids[[3]int32{2, 63, 0}] = 11
	b.items[0] = stack(1, 1)
	b.names[1] = "minecraft:bucket"
	b.names[2] = "minecraft:water_bucket"
	b.onUse = func(used protocol.BlockPos) {
		b.items[0] = stack(2, 1)                       // the bucket is full now
		b.blocks[[3]int32{2, 63, 0}] = "minecraft:air" // and the water is gone
		b.rids[[3]int32{2, 63, 0}] = 12
	}

	m := bucket.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	kind, err := m.FillBucket(context.Background(), protocol.BlockPos{2, 63, 0})
	if err != nil {
		t.Fatalf("FillBucket: %v", err)
	}
	if kind != bucket.BucketWater {
		t.Errorf("FillBucket = %v, want water", kind)
	}
	if len(m.Reports()) == 0 || !m.Reports()[0].Success {
		t.Errorf("a confirmed fill was not reported as a success: %+v", m.Reports())
	}
}

// The click went out and nothing came back. The bucket is still a bucket.
func TestFillBucketRefusesToClaimAnUnconfirmedFill(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{2, 63, 0}] = "minecraft:water"
	b.items[0] = stack(1, 1)
	b.names[1] = "minecraft:bucket"
	b.onUse = nil // the server ignores the click

	m := bucket.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if _, err := m.FillBucket(context.Background(), protocol.BlockPos{2, 63, 0}); err == nil {
		t.Error("a fill with no bucket change and no block change was reported as success")
	}
	for _, r := range m.Reports() {
		if r.Success {
			t.Errorf("a successful bucket fill was reported for a click the server ignored: %+v", r)
		}
	}
}

// The bucket filled but the source tile did not change. Half a confirmation is
// not a confirmation.
func TestFillBucketNeedsTheBlockUpdateToo(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{2, 63, 0}] = "minecraft:water"
	b.items[0] = stack(1, 1)
	b.names[1] = "minecraft:bucket"
	b.names[2] = "minecraft:water_bucket"
	b.onUse = func(used protocol.BlockPos) {
		b.items[0] = stack(2, 1) // bucket full, water still there
	}

	m := bucket.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if _, err := m.FillBucket(context.Background(), protocol.BlockPos{2, 63, 0}); err == nil {
		t.Error("a fill with no block update was reported as success")
	}
}

// Water goes on the ground and the bucket comes back empty.
func TestEmptyBucketConfirmsTheBlockBecameWater(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{2, 63, 0}] = "minecraft:air"
	b.rids[[3]int32{2, 63, 0}] = 11
	b.items[0] = stack(2, 1)
	b.names[2] = "minecraft:water_bucket"
	b.names[1] = "minecraft:bucket"
	b.onUse = func(used protocol.BlockPos) {
		b.items[0] = stack(1, 1)
		b.blocks[[3]int32{2, 63, 0}] = "minecraft:water"
		b.rids[[3]int32{2, 63, 0}] = 12
	}

	m := bucket.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if _, err := m.EmptyBucket(context.Background(), protocol.BlockPos{2, 63, 0}); err != nil {
		t.Errorf("EmptyBucket: %v", err)
	}
}

func TestEmptyBucketRefusesToClaimAnUnconfirmedPour(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{2, 63, 0}] = "minecraft:air"
	b.items[0] = stack(2, 1)
	b.names[2] = "minecraft:water_bucket"
	b.onUse = nil

	m := bucket.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if _, err := m.EmptyBucket(context.Background(), protocol.BlockPos{2, 63, 0}); err == nil {
		t.Error("a pour that changed nothing was reported as success")
	}
}

// Pouring water into a cauldron raises its level. The level has to be readable
// for that to be confirmed, and today it is not — so the manager reports what
// it saw and says the magnitude is unknown.
func TestFillCauldronObservesTheLevel(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{3, 63, 0}] = "minecraft:cauldron"
	b.rids[[3]int32{3, 63, 0}] = 20
	b.items[0] = stack(2, 1)
	b.names[2] = "minecraft:water_bucket"
	b.names[1] = "minecraft:bucket"
	b.onUse = func(used protocol.BlockPos) {
		b.items[0] = stack(1, 1)
		b.rids[[3]int32{3, 63, 0}] = 21 // a different cauldron state
	}

	m := bucket.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	change, err := m.FillCauldron(context.Background(), protocol.BlockPos{3, 63, 0})
	if err != nil {
		t.Fatalf("FillCauldron: %v", err)
	}
	if !change.Changed {
		t.Error("the cauldron's block state changed and the fill was not confirmed")
	}
	if change.LevelKnown {
		t.Error("a level was reported with no block properties wired")
	}
}

func TestFillCauldronWithAReadableLevelKnowsTheDirection(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{3, 63, 0}] = "minecraft:cauldron"
	b.rids[[3]int32{3, 63, 0}] = 20
	b.items[0] = stack(2, 1)
	b.names[2] = "minecraft:water_bucket"
	b.names[1] = "minecraft:bucket"

	states := &scriptedStates{}
	b.onUse = func(used protocol.BlockPos) {
		b.items[0] = stack(1, 1)
		b.rids[[3]int32{3, 63, 0}] = 21
		states.filled = true
	}

	m := bucket.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())
	m.SetBlockStateSource(states)

	change, err := m.FillCauldron(context.Background(), protocol.BlockPos{3, 63, 0})
	if err != nil {
		t.Fatalf("FillCauldron: %v", err)
	}
	if !change.LevelKnown {
		t.Fatal("both sides had a fill level and the change was still reported as unknown")
	}
	if change.Rose() != 1 {
		t.Errorf("Rose() = %d, want 1", change.Rose())
	}
}

func TestFillCauldronRefusesToClaimAnUnconfirmedFill(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{3, 63, 0}] = "minecraft:cauldron"
	b.rids[[3]int32{3, 63, 0}] = 20
	b.items[0] = stack(2, 1)
	b.names[2] = "minecraft:water_bucket"
	b.onUse = nil

	m := bucket.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if _, err := m.FillCauldron(context.Background(), protocol.BlockPos{3, 63, 0}); err == nil {
		t.Error("a cauldron fill with no state change was reported as success")
	}
}

func TestDrainCauldronRefusesToClaimAnUnconfirmedDrain(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{3, 63, 0}] = "minecraft:cauldron"
	b.rids[[3]int32{3, 63, 0}] = 20
	b.items[0] = stack(1, 1)
	b.names[1] = "minecraft:bucket"
	b.onUse = nil

	m := bucket.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	if _, err := m.DrainCauldron(context.Background(), protocol.BlockPos{3, 63, 0}); err == nil {
		t.Error("a cauldron drain with no state change was reported as success")
	}
}

// A drain needs an empty bucket to scoop into, and the cauldron has to answer.
func TestDrainCauldronTakesAnEmptyBucket(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{3, 63, 0}] = "minecraft:cauldron"
	b.rids[[3]int32{3, 63, 0}] = 20
	b.items[0] = stack(1, 1)
	b.names[1] = "minecraft:bucket"
	b.names[2] = "minecraft:water_bucket"
	b.onUse = func(used protocol.BlockPos) {
		b.items[0] = stack(2, 1)
		b.rids[[3]int32{3, 63, 0}] = 21
	}

	m := bucket.NewManager(b, quietLogger())
	m.SetTimings(fastTimings())

	change, err := m.DrainCauldron(context.Background(), protocol.BlockPos{3, 63, 0})
	if err != nil {
		t.Fatalf("DrainCauldron: %v", err)
	}
	if !change.Changed {
		t.Error("the cauldron drained and the state change was not observed")
	}
	if b.items[0].Count != 1 || b.names[b.items[0].NetworkID] != "minecraft:water_bucket" {
		t.Error("the empty bucket was not replaced by a filled one")
	}
}

// Every source the acceptance criteria name, end to end through the manager.
func TestFillBucketCoversEveryNamedSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		block string
		netID int32
		want  bucket.BucketKind
	}{
		{"minecraft:water", 11, bucket.BucketWater},
		{"minecraft:lava", 12, bucket.BucketLava},
		{"minecraft:powder_snow", 13, bucket.BucketPowderSnow},
	}
	names := map[int32]string{
		1:  "minecraft:bucket",
		11: "minecraft:water_bucket",
		12: "minecraft:lava_bucket",
		13: "minecraft:powder_snow_bucket",
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.block, func(t *testing.T) {
			t.Parallel()

			b := newFakeBot()
			b.blocks[[3]int32{2, 63, 0}] = tc.block
			b.rids[[3]int32{2, 63, 0}] = 20
			b.items[0] = stack(1, 1)
			b.names = names
			b.onUse = func(used protocol.BlockPos) {
				b.items[0] = stack(tc.netID, 1)
				b.blocks[[3]int32{2, 63, 0}] = "minecraft:air"
				b.rids[[3]int32{2, 63, 0}] = 21
			}

			m := bucket.NewManager(b, quietLogger())
			m.SetTimings(fastTimings())

			got, err := m.FillBucket(context.Background(), protocol.BlockPos{2, 63, 0})
			if err != nil {
				t.Fatalf("FillBucket: %v", err)
			}
			if got != tc.want {
				t.Errorf("FillBucket = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAFindTargetFindsWaterAndACauldron(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[[3]int32{2, 63, 0}] = "minecraft:water"
	b.blocks[[3]int32{-3, 63, 0}] = "minecraft:cauldron"

	m := bucket.NewManager(b, quietLogger())

	pos, src, ok := m.FindFillTarget()
	if !ok {
		t.Fatal("no fill target found next to a water tile")
	}
	if kind, ok := bucket.BucketFillPlan(src); !ok || kind != bucket.BucketWater {
		t.Errorf("FindFillTarget returned %s, which is not water", src.Label())
	}
	_ = pos

	if _, ok := m.FindNearbyCauldron(); !ok {
		t.Error("no cauldron found next to one")
	}
}

// scriptedStates hands out a fill_level that the test's onUse hook advances.
type scriptedStates struct {
	filled bool
}

func (s *scriptedStates) GetBlockState(x, y, z int32) (string, map[string]any, bool) {
	if s.filled {
		return "minecraft:cauldron", map[string]any{
			"fill_level":      int32(2),
			"cauldron_liquid": "water",
		}, true
	}
	return "minecraft:cauldron", map[string]any{
		"fill_level":      int32(0),
		"cauldron_liquid": "",
	}, true
}
