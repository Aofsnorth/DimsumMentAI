package movement_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/network/player"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// The host keeps lifting a body that sinks into a stair step the local world
// model sees as a full cube. Trusting that lift keeps the body standing instead
// of sink / snap-back forever at the same spot.
func TestServerPushUpHoldsBodyOnUnmodelledSupport(t *testing.T) {
	t.Parallel()

	b, _, _ := newSlabBot(t)
	feet := mgl32.Vec3{23.85, 70.6, -0.39}

	b.Mu.Lock()
	player.NoteServerSupport(b, 70.44, 70.6)
	b.Mu.Unlock()

	tc := movement.TickContextForTest(b, feet)
	tc.RunPhysicsAndCollisionsForTest()
	if !tc.IsGrounded || tc.NextYForTest() != 70.6 {
		t.Fatalf("body not held on server support: grounded=%v nextY=%.3f", tc.IsGrounded, tc.NextYForTest())
	}
}

// A carpet is passable in the model, so the floor reads as the block under it
// and the resting catch pulled the body down to that top, where the host
// refused it and reset the bot upward again, forever. A reset above the floor
// must hold the body at the host's height while its box is still over the block.
func TestServerResetAboveFloorHoldsBodyOnCarpet(t *testing.T) {
	t.Parallel()

	b, _, full := newSlabBot(t)
	b.WorldCache.SetBlockRID(7, 96, 3, full)
	b.Pos = mgl32.Vec3{7.5, 97.0, 3.5}
	b.MovementState = "follow"

	b.Mu.Lock()
	player.ApplySelfMovePlayer(b, 2, mgl32.Vec3{7.5, 97.12, 3.5})
	b.Mu.Unlock()

	tc := movement.TickContextForTest(b, mgl32.Vec3{7.5, 97.12, 3.5})
	tc.RunPhysicsAndCollisionsForTest()
	if !tc.IsGrounded || tc.NextYForTest() != 97.12 {
		t.Fatalf("body pulled off the host's height: grounded=%v nextY=%.3f", tc.IsGrounded, tc.NextYForTest())
	}

	// Box cleared the block: the claim is not renewed and no longer holds the body up.
	b.Mu.Lock()
	b.ServerSupportAt = time.Now().Add(-time.Second)
	b.ServerGroundAt = time.Time{} // the idle-bot anchor is a separate mechanism
	b.Mu.Unlock()
	far := movement.TickContextForTest(b, mgl32.Vec3{9.5, 97.12, 3.5})
	far.RunPhysicsAndCollisionsForTest()
	if far.NextYForTest() >= 97.12 {
		t.Fatalf("body hovers over air with an expired claim: nextY=%.3f", far.NextYForTest())
	}
}

// The host keeps standing the body a full block above what the model decoded —
// the live tree-on-a-slope run: server feet ~101.06, model air from 100 up.
// Three agreeing corrections must reconcile the missing support into the world
// model, so grounding and pathfinding agree with the server's world instead of
// fighting it (the fight is what stalled the step-up and burned twenty scaffold
// placements on a cell the server already considered occupied).
func TestPersistentServerPinReconcilesMissingSupport(t *testing.T) {
	t.Parallel()

	b, _, _ := newSlabBot(t)
	b.MovementState = "idle"

	serverFeet := float32(101.06)
	for i := 0; i < 3; i++ {
		b.Mu.Lock()
		b.Pos = mgl32.Vec3{7.5, 100.0, 3.5} // local physics sank the body again
		player.ApplySelfMovePlayer(b, 2, mgl32.Vec3{7.5, serverFeet, 3.5})
		b.Mu.Unlock()
	}

	if !b.WorldModel.IsSolid(7, 100, 3) {
		t.Fatal("three agreeing server pins did not reconcile the missing support block into the model")
	}
}

// A correction that stands the feet mid-cell is a partial block top (slab,
// decor) — the support hold's territory, never a full missing cube.
func TestMidCellServerPinDoesNotReconcileABlock(t *testing.T) {
	t.Parallel()

	b, _, _ := newSlabBot(t)
	b.MovementState = "idle"

	for i := 0; i < 5; i++ {
		b.Mu.Lock()
		b.Pos = mgl32.Vec3{7.5, 100.0, 3.5}
		player.ApplySelfMovePlayer(b, 2, mgl32.Vec3{7.5, 101.56, 3.5})
		b.Mu.Unlock()
	}

	if b.WorldModel.IsSolid(7, 100, 3) || b.WorldModel.IsSolid(7, 101, 3) {
		t.Fatal("a mid-cell pin reconciled a full block that is not there")
	}
}

// Corrections landing while the body is in flight are stale arc floors, not
// ground truth — they must never reconcile anything.
func TestInFlightCorrectionsDoNotReconcile(t *testing.T) {
	t.Parallel()

	b, _, _ := newSlabBot(t)
	b.MovementState = "walk_to"

	for i := 0; i < 5; i++ {
		b.Mu.Lock()
		b.Pos = mgl32.Vec3{7.5, 100.0, 3.5}
		b.VelY = 0.3 // rising
		player.ApplySelfMovePlayer(b, 2, mgl32.Vec3{7.5, 101.06, 3.5})
		b.Mu.Unlock()
	}

	if b.WorldModel.IsSolid(7, 100, 3) {
		t.Fatal("an in-flight correction stream reconciled a missing support block")
	}
}

// The idle-falling carve-out: an idle body falling through model-air while the
// server pins it a block+ higher IS the Y-fight's signature. Those echoes must
// count (the live run's echoes landed mid-fall and were all skipped), and
// three of them — resting, falling, resting — must reconcile the pin.
func TestIdleFallingEchoesReconcileThePin(t *testing.T) {
	t.Parallel()

	b, _, full := newSlabBot(t)
	b.WorldCache.SetBlockRID(5, 120, 5, full)
	b.MovementState = "idle"

	// One rising echo first: a tower jump's stale pre-arc echo must not count.
	b.Mu.Lock()
	b.Pos = mgl32.Vec3{5.5, 121.0, 5.5}
	b.VelY = 0.4
	player.ApplySelfMovePlayer(b, 2, mgl32.Vec3{5.5, 123.06, 5.5})
	b.Mu.Unlock()

	// The fight's echo cadence: falling, resting, falling — all idle.
	epochs := []struct {
		bodyY float32
		velY  float32
	}{
		{122.34, -0.4},
		{121.0, 0},
		{122.34, -0.4},
	}
	for _, e := range epochs {
		b.Mu.Lock()
		b.Pos = mgl32.Vec3{5.5, e.bodyY, 5.5}
		b.VelY = e.velY
		player.ApplySelfMovePlayer(b, 2, mgl32.Vec3{5.5, 123.06, 5.5})
		b.Mu.Unlock()
	}

	if !b.WorldModel.IsSolid(5, 122, 5) {
		t.Fatal("idle-falling echo stream did not reconcile the missing support block")
	}
}

// A reset that actually moves the body during a walk invalidates the route:
// it is anchored to the pre-reset position, and resuming it walks the body
// straight back to where the server just refused it — the dug-shaft chase that
// fought the server for seventeen seconds. A sub-half-block nudge must keep
// the route.
func TestServerResetDuringWalkDropsTheStaleRoute(t *testing.T) {
	t.Parallel()

	b, _, _ := newSlabBot(t)
	b.MovementState = "walk_to"
	b.CurrentPath = []pathfinder.Node{{X: -94, Y: 117, Z: -2}, {X: -93, Y: 117, Z: -2}}
	b.PathIndex = 1

	b.Mu.Lock()
	player.ApplySelfMovePlayer(b, 2, mgl32.Vec3{7.5, 100.0, 5.5}) // ~2 blocks away
	b.Mu.Unlock()

	if b.CurrentPath != nil || b.PathIndex != 0 {
		t.Fatal("a meaningful reset during a walk kept the stale route")
	}
	if b.ServerResets != 1 {
		t.Fatalf("ServerResets = %d, want 1", b.ServerResets)
	}

	// A tiny nudge is not a refusal: the route stays.
	b.CurrentPath = []pathfinder.Node{{X: 1}}
	b.PathIndex = 1
	b.Mu.Lock()
	b.Pos = mgl32.Vec3{7.5, 100.0, 3.5} // back on the pre-reset footing
	player.ApplySelfMovePlayer(b, 2, mgl32.Vec3{7.5, 100.02, 3.52})
	b.Mu.Unlock()
	if b.CurrentPath == nil {
		t.Fatal("a sub-half-block reset dropped a route that was still valid")
	}
	if b.ServerResets != 2 {
		t.Fatalf("ServerResets = %d, want 2 after the second ack", b.ServerResets)
	}
}

// Without a recent push-up, or once it is stale, gravity applies as usual.
func TestServerSupportExpiresAndNeedsAnUpwardPush(t *testing.T) {
	t.Parallel()

	b, _, _ := newSlabBot(t)
	feet := mgl32.Vec3{23.85, 70.6, -0.39}

	b.Mu.Lock()
	player.NoteServerSupport(b, 70.8, 70.6) // pushed DOWN: not support
	b.Mu.Unlock()
	tc := movement.TickContextForTest(b, feet)
	tc.RunPhysicsAndCollisionsForTest()
	if tc.IsGrounded {
		t.Fatal("a downward correction must not count as support")
	}

	b.Mu.Lock()
	b.ServerSupportY = 70.6
	b.ServerSupportAt = time.Now().Add(-time.Second)
	b.Mu.Unlock()
	tc = movement.TickContextForTest(b, feet)
	tc.RunPhysicsAndCollisionsForTest()
	if tc.IsGrounded {
		t.Fatal("a stale push-up must not count as support")
	}

	b.Mu.Lock()
	player.NoteServerSupport(b, 60, 70.6) // far too big: a teleport
	stale := b.ServerSupportAt
	b.Mu.Unlock()
	if !stale.Before(time.Now().Add(-500 * time.Millisecond)) {
		t.Fatal("a large lift is a teleport and must not be recorded as support")
	}
}
