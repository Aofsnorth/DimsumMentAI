package movement_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/network/player"

	"github.com/go-gl/mathgl/mgl32"
)

// reconcilePinFixture lays model ground: solid at y=120 (top 121.0), air above
// — a decode gap — and a fresh server pin at 123.06 over that column.
func reconcilePinFixture(t *testing.T, bodyY float32) *bot.Bot {
	t.Helper()
	b, _, full := newSlabBot(t)
	b.WorldCache.SetBlockRID(5, 120, 5, full)
	b.Mu.Lock()
	b.ServerGroundY = 123.06
	b.ServerGroundAt = time.Now()
	b.Pos = mgl32.Vec3{5.5, bodyY, 5.5}
	b.Mu.Unlock()
	return b
}

// The Y-fight regression: the server pins the feet at ~123.06 on a block the
// model never decoded (model ground at 121.0), the body oscillates between the
// two resting levels, and the old body-lift gate [0.5,1.6] sat exactly between
// them — every reconcile missed deterministically. The gate now measures gross
// displacement only, so all three body levels reconcile.
func TestReconcileServerPinnedSupportFiresAtEveryBodyLevel(t *testing.T) {
	t.Parallel()

	for _, bodyY := range []float32{121.0, 122.34, 123.06} {
		b := reconcilePinFixture(t, bodyY)

		if !player.ReconcileServerPinnedSupportNow(b) {
			t.Fatalf("body at %.2f: reconcile refused a decoded-air pin cell", bodyY)
		}
		if !b.WorldModel.IsSolid(5, 122, 5) {
			t.Fatalf("body at %.2f: pinned cell 122 was not marked solid", bodyY)
		}
	}
}

// The guards that keep a transient echo from conjuring ground.
func TestReconcileServerPinnedSupportRefusals(t *testing.T) {
	t.Parallel()

	// A stale pin says nothing about the column being fought right now.
	b := reconcilePinFixture(t, 121.0)
	b.Mu.Lock()
	b.ServerGroundAt = time.Now().Add(-3 * time.Second)
	b.Mu.Unlock()
	if player.ReconcileServerPinnedSupportNow(b) {
		t.Fatal("a stale pin reconciled a cell")
	}

	// A pin five blocks up is a real drop / a far teleport, not a decode gap.
	b = reconcilePinFixture(t, 121.0)
	b.Mu.Lock()
	b.ServerGroundY = 128.06
	b.Mu.Unlock()
	if player.ReconcileServerPinnedSupportNow(b) {
		t.Fatal("a pin five blocks above the body reconciled a cell")
	}

	// Mid-cell feet are a partial block top: the support hold's territory.
	b = reconcilePinFixture(t, 121.0)
	b.Mu.Lock()
	b.ServerGroundY = 123.5
	b.Mu.Unlock()
	if player.ReconcileServerPinnedSupportNow(b) {
		t.Fatal("a mid-cell pin reconciled a full block")
	}

	// The 2s cooldown: a second reconcile right after the first must wait.
	b = reconcilePinFixture(t, 121.0)
	if !player.ReconcileServerPinnedSupportNow(b) {
		t.Fatal("first reconcile refused")
	}
	if player.ReconcileServerPinnedSupportNow(b) {
		t.Fatal("the 2s cooldown did not gate an immediate second reconcile")
	}
}
