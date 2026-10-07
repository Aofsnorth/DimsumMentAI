package movement_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/network/player"

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
