package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"

	"github.com/go-gl/mathgl/mgl32"
)

func risingLedgeBot(t *testing.T, ledge bool) *movement.TickContext {
	t.Helper()
	b, _, full := newSlabBot(t)
	for x := int32(0); x <= 10; x++ {
		b.WorldCache.SetBlockRID(x, 69, 5, full)
	}
	if ledge {
		b.WorldCache.SetBlockRID(6, 70, 5, full)
	}
	return &movement.TickContext{B: b, CurrPos: mgl32.Vec3{5.6, 70, 5.5}, NextY: 70.42, VelY: 0.42}
}

// A jumping body 0.42 up beside a full block is still below its top: the
// horizontal step must not bury it in the block (the host resets it and the
// jump never gets going).
func TestRisingBodyDoesNotSlideIntoALedge(t *testing.T) {
	t.Parallel()

	tc := risingLedgeBot(t, true)
	got := tc.HoldRisingBodyOffLedgeForTest(mgl32.Vec3{5.8, 70.42, 5.5})
	if got.X() > 5.6+1e-4 {
		t.Fatalf("rising body slid to x=%v into the ledge at x=6", got.X())
	}
}

func TestRisingBodyMovesOnOnceItClearsTheLedge(t *testing.T) {
	t.Parallel()

	tc := risingLedgeBot(t, true)
	tc.NextY, tc.VelY = 71.02, 0.18
	got := tc.HoldRisingBodyOffLedgeForTest(mgl32.Vec3{5.8, 71.02, 5.5})
	if got.X() < 5.8-1e-4 {
		t.Fatalf("body above the ledge top was still held at x=%v", got.X())
	}
}

func TestRisingBodyOnOpenGroundIsNotHeld(t *testing.T) {
	t.Parallel()

	tc := risingLedgeBot(t, false)
	got := tc.HoldRisingBodyOffLedgeForTest(mgl32.Vec3{5.8, 70.42, 5.5})
	if got.X() < 5.8-1e-4 {
		t.Fatalf("open-ground jump was held at x=%v", got.X())
	}
}

func TestGroundedBodyIsNeverHeldByTheRisingGate(t *testing.T) {
	t.Parallel()

	tc := risingLedgeBot(t, true)
	tc.IsGrounded, tc.VelY = true, 0
	got := tc.HoldRisingBodyOffLedgeForTest(mgl32.Vec3{5.8, 70, 5.5})
	if got.X() < 5.8-1e-4 {
		t.Fatal("grounded walk was held: the slab walk-up would break")
	}
}
