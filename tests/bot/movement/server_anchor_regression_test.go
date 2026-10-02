package movement_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// newAnchorTestBot builds a stationary bot whose local world model has no floor
// beneath it. That is the state a freshly joined LAN world is in, and it is what
// used to make the bot fall every tick.
//
// Path bounds are set to a node far away on purpose. With bounds active the
// world model's unloaded-chunk fallback only treats the start node's floor cell
// as solid, which is exactly the condition that leaves the real bot airborne
// while the server keeps correcting it. Without bounds the model falls back to
// a sea-level assumption (y <= 62 is solid) and the bug cannot be reproduced.
func newAnchorTestBot(feet mgl32.Vec3) *bot.Bot {
	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(pathfinder.Node{X: 500, Y: 120, Z: 500}, pathfinder.Node{X: 520, Y: 120, Z: 520})
	return &bot.Bot{
		MovementState: "idle",
		WorldModel:    model,
		Pos:           feet,
	}
}

// TestServerAnchorStopsIdleFreeFall is the regression guard for the vertical
// fall/snap cycle. With no decoded floor and no movement, the local world model
// reports "not grounded", so gravity accumulated every tick and the bot drifted
// downwards. The server then snapped it back on the next position correction,
// producing a repeating fall/snap cycle that moved the body over a block.
//
// The anchor must hold the bot at the Y the server last confirmed.
func TestServerAnchorStopsIdleFreeFall(t *testing.T) {
	t.Parallel()

	const anchorY = float32(60.0)
	feet := mgl32.Vec3{-43.5, anchorY, -106.5}
	b := newAnchorTestBot(feet)
	b.ServerGroundY = anchorY
	b.ServerGroundAt = time.Now()

	tc := &movement.TickContext{
		B:       b,
		CurrPos: feet,
		MState:  "idle",
		FeetY:   60,
	}

	tc.UpdateGroundedState()
	if tc.IsGrounded {
		t.Fatal("empty world model reported grounded; test no longer covers the bug")
	}

	tc.ApplyServerPositionAnchor()

	if !tc.IsGrounded {
		t.Fatal("IsGrounded = false, want the server anchor to report grounded")
	}
	if tc.VelY != 0 {
		t.Fatalf("VelY = %v, want 0 while anchored", tc.VelY)
	}
	if tc.NextY != anchorY {
		t.Fatalf("NextY = %v, want the server-confirmed Y %v", tc.NextY, anchorY)
	}
}

// TestServerAnchorHoldsAcrossManyTicks simulates the real failure shape: an idle
// bot whose body Y would otherwise sag over time, with the server refreshing its
// confirmed position periodically. The simulated Y must stay flat.
func TestServerAnchorHoldsAcrossManyTicks(t *testing.T) {
	t.Parallel()

	const anchorY = float32(60.0)
	feet := mgl32.Vec3{-43.5, anchorY, -106.5}
	b := newAnchorTestBot(feet)

	simY := anchorY
	for tick := uint64(0); tick < 20*30; tick++ {
		// The host refreshes its correction every 6 ticks in practice.
		if tick%6 == 0 {
			b.Mu.Lock()
			b.ServerGroundY = anchorY
			b.ServerGroundAt = time.Now()
			b.Pos = mgl32.Vec3{feet.X(), simY, feet.Z()}
			b.Mu.Unlock()
		}

		tc := &movement.TickContext{
			B:       b,
			CurrPos: mgl32.Vec3{feet.X(), simY, feet.Z()},
			MState:  "idle",
			Tick:    tick,
			FeetY:   int32(simY),
			PrevPos: mgl32.Vec3{feet.X(), simY, feet.Z()},
		}

		tc.UpdateGroundedState()
		tc.ApplyServerPositionAnchor()
		tc.ApplyVerticalVelocity()
		tc.ApplyGroundLanding()

		simY = tc.NextY
		if diff := simY - anchorY; diff > 0.01 || diff < -0.01 {
			t.Fatalf("tick %d: simulated Y = %v, drifted from anchor %v", tick, simY, anchorY)
		}
	}
}

// TestServerAnchorIgnoresRealDisplacement ensures the anchor never hides a real
// teleport: a large gap between the simulated Y and the server Y must be left
// for the normal movement code to resolve.
func TestServerAnchorIgnoresRealDisplacement(t *testing.T) {
	t.Parallel()

	const anchorY = float32(60.0)
	feet := mgl32.Vec3{-43.5, anchorY, -106.5}
	b := newAnchorTestBot(feet)
	b.ServerGroundY = anchorY
	b.ServerGroundAt = time.Now()

	// Simulate a teleport: the bot is far below the last confirmed Y.
	teleported := mgl32.Vec3{feet.X(), anchorY - 4, feet.Z()}
	tc := &movement.TickContext{
		B:       b,
		CurrPos: teleported,
		MState:  "idle",
		FeetY:   56,
	}

	tc.UpdateGroundedState()
	tc.ApplyServerPositionAnchor()

	if tc.IsGrounded {
		t.Fatal("anchor pinned a bot that had really been displaced 4 blocks")
	}
}

// TestServerAnchorStaleNotUsed ensures an old confirmation cannot freeze a bot
// that is genuinely in the air after a jump.
func TestServerAnchorStaleNotUsed(t *testing.T) {
	t.Parallel()

	const anchorY = float32(60.0)
	feet := mgl32.Vec3{-43.5, anchorY, -106.5}
	b := newAnchorTestBot(feet)
	b.ServerGroundY = anchorY
	b.ServerGroundAt = time.Now().Add(-5 * time.Second)

	tc := &movement.TickContext{
		B:       b,
		CurrPos: feet,
		MState:  "idle",
		FeetY:   60,
	}

	tc.UpdateGroundedState()
	tc.ApplyServerPositionAnchor()

	if tc.IsGrounded {
		t.Fatal("a stale server anchor grounded the bot; it must expire")
	}
}

// TestServerAnchorSkipsActiveMovement guards the other side of the contract: a
// bot that is walking, jumping or climbing owns its own vertical model and must
// never be pinned to the anchor.
func TestServerAnchorSkipsActiveMovement(t *testing.T) {
	t.Parallel()

	const anchorY = float32(60.0)
	feet := mgl32.Vec3{-43.5, anchorY, -106.5}

	tests := []struct {
		name  string
		apply func(tc *movement.TickContext)
	}{
		{"walking", func(tc *movement.TickContext) { tc.HasHorizontalMove = true }},
		{"on a path", func(tc *movement.TickContext) { tc.HasPath = true }},
		{"jumping", func(tc *movement.TickContext) { tc.ShouldJump = true }},
		{"parkour jump", func(tc *movement.TickContext) { tc.IsParkourJump = true }},
		{"on a ladder", func(tc *movement.TickContext) { tc.IsOnLadder = true }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := newAnchorTestBot(feet)
			b.ServerGroundY = anchorY
			b.ServerGroundAt = time.Now()

			tc := &movement.TickContext{
				B:       b,
				CurrPos: feet,
				MState:  "idle",
				FeetY:   60,
			}
			tt.apply(tc)

			tc.UpdateGroundedState()
			tc.ApplyServerPositionAnchor()

			if tc.IsGrounded {
				t.Fatalf("%s: anchor pinned a bot that must keep its own vertical model", tt.name)
			}
		})
	}
}

// TestFollowLookPitchStableWhileWatchingNearbyPlayer ties the two reported
// symptoms to the single root cause. The bot used to drift up and down while
// idle, and because the follow-look pitch is derived from the bot's own Y, a
// body that sags and gets snapped back produced a visible up/down head tremor
// and an upward aim bias when looking at a player standing right next to it.
//
// With the body held at the server-confirmed Y, the follow-look pitch for a
// same-level neighbour must stay flat at zero.
func TestFollowLookPitchStableWhileWatchingNearbyPlayer(t *testing.T) {
	t.Parallel()

	const anchorY = float32(60.0)
	botFeet := mgl32.Vec3{-43.5, anchorY, -106.5}
	// A neighbour standing one block away on the same floor.
	neighbourFeet := mgl32.Vec3{-42.5, anchorY, -106.5}

	b := newAnchorTestBot(botFeet)
	simY := anchorY

	for tick := uint64(0); tick < 20*20; tick++ {
		if tick%6 == 0 {
			b.Mu.Lock()
			b.ServerGroundY = anchorY
			b.ServerGroundAt = time.Now()
			b.TargetPos = neighbourFeet
			b.Mu.Unlock()
		}

		tc := &movement.TickContext{
			B:       b,
			CurrPos: mgl32.Vec3{botFeet.X(), simY, botFeet.Z()},
			MState:  "follow",
			TPlayer: "Arthenyxx",
			TPos:    neighbourFeet,
			FeetY:   int32(simY),
			PrevPos: mgl32.Vec3{botFeet.X(), simY, botFeet.Z()},
		}

		tc.UpdateGroundedState()
		tc.ApplyServerPositionAnchor()
		tc.ApplyVerticalVelocity()
		tc.ApplyGroundLanding()
		simY = tc.NextY

		tc.ApplyFollowLookTarget()

		if tc.TargetPitch != 0 {
			t.Fatalf("tick %d: follow TargetPitch = %v, want 0 for a same-level neighbour", tick, tc.TargetPitch)
		}
	}
}
