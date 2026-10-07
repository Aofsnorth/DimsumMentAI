package movement_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/world"

	"github.com/go-gl/mathgl/mgl32"
)

func carpetRID(t *testing.T, c *world.WorldCache) uint32 {
	t.Helper()
	for rid := uint32(0); rid < 22091; rid++ {
		name, ok := c.BlockName(rid)
		if !ok || name != "minecraft:moss_carpet" {
			continue
		}
		c.SetBlockRID(0, 0, 0, rid)
		top := c.TopOffsetAt(0, 0, 0)
		c.SetBlockRID(0, 0, 0, slabAirRID)
		if top == 0.0625 {
			return rid
		}
	}
	t.Skip("embedded palette has no moss_carpet")
	return 0
}

// The host collides with a carpet's 1/16 shape, so a body whose box slides over
// a carpet at the same Y is reset every tick. It must step onto the carpet.
func TestStepUpAssistLiftsOntoCarpet(t *testing.T) {
	t.Parallel()

	b, _, full := newSlabBot(t)
	carpet := carpetRID(t, b.WorldCache)
	b.WorldCache.SetBlockRID(5, 69, 5, full)
	b.WorldCache.SetBlockRID(6, 69, 5, full)
	b.WorldCache.SetBlockRID(5, 70, 5, carpet)

	tc := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{6.1, 70.0, 5.5}, NextY: 70.0, IsGrounded: false}
	got := tc.ApplyStepUpAssistForTest(mgl32.Vec3{5.8, 70.0, 5.5})
	if got.Y() != 70.0625 {
		t.Fatalf("body walking onto a carpet was left at y=%.4f, want 70.0625", got.Y())
	}
	if !tc.IsGrounded {
		t.Error("body on the carpet must read as grounded")
	}
}

// A body that landed a block high over real air must fall, not be held there
// by the server anchor (the host echoes our own Y back).
func TestAnchorDoesNotHoldBodyFloatingOverAir(t *testing.T) {
	t.Parallel()

	b, _, full := newSlabBot(t)
	carpet := carpetRID(t, b.WorldCache)
	b.WorldCache.SetBlockRID(5, 69, 5, full)
	b.ServerGroundY = 71.0
	b.ServerGroundAt = time.Now()

	floating := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{5.5, 71.0, 5.5}, NextY: 71.0}
	floating.ApplyServerPositionAnchor()
	if floating.IsGrounded {
		t.Error("body floating over air was anchored in place")
	}

	// A carpet under the feet is not air: the anchor keeps holding there.
	b.WorldCache.SetBlockRID(5, 70, 5, carpet)
	b.ServerGroundY = 71.0
	b.ServerGroundAt = time.Now()
	onCarpet := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{5.5, 71.0, 5.5}, NextY: 71.0}
	onCarpet.ApplyServerPositionAnchor()
	if !onCarpet.IsGrounded {
		t.Error("body above a partial floor must still use the server anchor")
	}
}

// Once the body has cleared the carpet it sinks back onto the floor.
func TestGroundLandingSinksOffCarpet(t *testing.T) {
	t.Parallel()

	b, _, full := newSlabBot(t)
	carpet := carpetRID(t, b.WorldCache)
	b.WorldCache.SetBlockRID(5, 69, 5, full)
	b.WorldCache.SetBlockRID(6, 69, 5, full)
	b.WorldCache.SetBlockRID(5, 70, 5, carpet)

	on := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{5.5, 70.0625, 5.5}, NextY: 70.0625 - 0.08, VelY: -0.08}
	on.ApplyGroundLanding()
	if on.NextY != 70.0625 {
		t.Errorf("body over the carpet sank to y=%.4f, want 70.0625", on.NextY)
	}

	off := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{6.5, 70.0625, 5.5}, NextY: 70.0625 - 0.08, VelY: -0.08}
	off.ApplyGroundLanding()
	if off.NextY != 70.0 {
		t.Errorf("body clear of the carpet is at y=%.4f, want 70.0000", off.NextY)
	}
}
