package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

func ledgeDropTick(t *testing.T, z float32) *movement.TickContext {
	t.Helper()
	b, _, full := newSlabBot(t)
	// Ledge the bot stands on, drop beyond it toward -Z.
	b.WorldCache.SetBlockRID(6, 99, 4, full)
	b.WorldCache.SetBlockRID(6, 97, 3, full)
	b.CurrentPath = []pathfinder.Node{{X: 6, Y: 98, Z: 3, LinkType: pathfinder.LinkFall}}
	b.PathIndex = 0
	b.Pos = mgl32.Vec3{6.5, 100.0, z}

	tc := &movement.TickContext{
		B:       b,
		HasPath: true,
		CurrPos: b.Pos,
		NextY:   100.0,
		Dx:      0,
		Dz:      3.5 - z,
		Dist:    4.5 - z - 1,
		MState:  "walk_to",
	}
	tc.RunPhysicsAndCollisionsForTest()
	return tc
}

// The host keeps a body up while its 0.6-wide box overlaps the ledge, so a bot
// whose centre has crossed the edge but whose box has not cleared it must not
// sink: the lowered position is refused and the bot is snapped back every tick.
func TestBodyStaysUpWhileBoxOverlapsLedge(t *testing.T) {
	t.Parallel()

	tc := ledgeDropTick(t, 3.86) // box spans z 3.56..4.16, still over the ledge cell
	if tc.NextY < 100.0 {
		t.Fatalf("body sank to y=%.3f while its box still overlaps the ledge", tc.NextY)
	}
	if !tc.IsGrounded {
		t.Fatal("body resting on a ledge with its box overlapping must stay grounded")
	}
}

func TestBodyFallsOnceBoxClearsLedge(t *testing.T) {
	t.Parallel()

	tc := ledgeDropTick(t, 3.6) // box spans z 3.3..3.9, fully past the ledge cell
	if tc.NextY >= 100.0 {
		t.Fatalf("body did not start falling after its box cleared the ledge (y=%.3f)", tc.NextY)
	}
}
