package movement_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// newFollowTestBot builds a follow-mode bot tracking a named player whose
// position is fixed. Path bounds point far away so the world model does not
// pretend there is a floor under the bot.
func newFollowTestBot(botFeet, playerFeet mgl32.Vec3) *bot.Bot {
	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(pathfinder.Node{X: 500, Y: 120, Z: 500}, pathfinder.Node{X: 520, Y: 120, Z: 520})

	b := &bot.Bot{
		MovementState:    "follow",
		TargetPlayerName: "Arthenyxx",
		WorldModel:       model,
		Pos:              botFeet,
	}
	b.PlayerTracker = bot.NewPlayerTracker()
	b.PlayerEntityIDs["Arthenyxx"] = 7
	b.PlayerUsernames[7] = "Arthenyxx"
	b.PlayerPositions[7] = playerFeet
	return b
}

// TestFollowWalkLatchHysteresis guards the walk/stop boundary flap. With a
// single 2.0-block threshold on both sides, a target hovering at ~2.0 blocks
// flipped the state every tick, alternating the look target between the
// walking pose (pitch 0 + gaze scan) and the tracked pose (player eye) — which
// read as the head bouncing up and down while following. Inside the dead band
// the previous state must be kept.
func TestFollowWalkLatchHysteresis(t *testing.T) {
	t.Parallel()

	botFeet := mgl32.Vec3{0, 64, 0}
	playerFeet := mgl32.Vec3{2.0, 64, 0} // same level, 2 blocks away — dead band
	b := newFollowTestBot(botFeet, playerFeet)

	newTC := func(dist float32) *movement.TickContext {
		return &movement.TickContext{
			B:            b,
			CurrPos:      botFeet,
			MState:       "follow",
			TPlayer:      "Arthenyxx",
			DistToPlayer: dist,
		}
	}

	// Start stopped: dead band keeps the previous (stopped) state.
	b.FollowMoving = false
	tc := newTC(2.0)
	tc.UpdateShouldMoveState()
	if tc.FollowWalking {
		t.Fatal("dist=2.0 from stopped: latch engaged, want it kept stopped in the dead band")
	}
	if tc.WantsToMove() {
		t.Fatal("wantsToMove true in the dead band from stopped, want false")
	}

	// Far enough: engage walking.
	tc = newTC(2.3)
	tc.UpdateShouldMoveState()
	if !tc.FollowWalking {
		t.Fatal("dist=2.3: latch still stopped, want walking")
	}

	// Back into the dead band: must keep walking (no flap).
	tc = newTC(2.0)
	tc.UpdateShouldMoveState()
	if !tc.FollowWalking {
		t.Fatal("dist=2.0 from walking: latch dropped out, want it kept walking in the dead band")
	}

	// Only the stop threshold releases it.
	tc = newTC(1.7)
	tc.UpdateShouldMoveState()
	if tc.FollowWalking {
		t.Fatal("dist=1.7: latch still walking, want stopped")
	}

	// And the dead band keeps it stopped again.
	tc = newTC(2.0)
	tc.UpdateShouldMoveState()
	if tc.FollowWalking {
		t.Fatal("dist=2.0 from stopped: latch engaged, want kept stopped")
	}
}

// TestFollowStopTargetDoesNotFlap ties the latch to the movement gates: while
// stopped inside the dead band, CloseFollowTarget must hold and WantsToMove
// must stay false across ticks, so the look pipeline keeps one stable target.
func TestFollowStopTargetDoesNotFlap(t *testing.T) {
	t.Parallel()

	botFeet := mgl32.Vec3{0, 64, 0}
	// The player drifts ±5 cm around the 2.0-block line, as reported positions
	// do at rest.
	b := newFollowTestBot(botFeet, mgl32.Vec3{2.0, 64, 0})
	b.FollowMoving = false

	wantsMoveVals := make([]bool, 0, 40)
	for tick := 0; tick < 40; tick++ {
		dist := float32(2.0)
		if tick%2 == 0 {
			dist = 1.98
		} else {
			dist = 2.02
		}
		tc := &movement.TickContext{
			B:            b,
			CurrPos:      botFeet,
			MState:       "follow",
			TPlayer:      "Arthenyxx",
			DistToPlayer: dist,
		}
		tc.UpdateShouldMoveState()
		if tc.CloseFollowTarget() != !tc.FollowWalking {
			t.Fatalf("tick %d: CloseFollowTarget disagrees with the walk latch", tick)
		}
		wantsMoveVals = append(wantsMoveVals, tc.WantsToMove())
	}

	for i, v := range wantsMoveVals {
		if v {
			t.Fatalf("sample %d: WantsToMove flipped true while stopped in the dead band", i)
		}
	}
}

// TestServerAnchorSelfRenewsWhileStationary is the regression guard for the
// anchor expiry limit cycle. The anchor used to go stale 400 ms after the last
// server correction; a stationary bot on a floor the local world model does not
// classify as solid (snow layers, slabs) then sagged until the next correction
// snapped it back, repeating forever — a continuous body bob that read as the
// head trembling. With self-renewal, one confirmation must hold the bot flat
// for as long as it stays stationary, even with no further packets.
func TestServerAnchorSelfRenewsWhileStationary(t *testing.T) {
	t.Parallel()

	// Feet at 60.75: standing on 6 snow layers above a block — exactly the
	// kind of partial-height floor the world model reports as air.
	const anchorY = float32(60.75)
	feet := mgl32.Vec3{-43.5, anchorY, -106.5}
	b := newAnchorTestBot(feet)
	b.ServerGroundY = anchorY
	b.ServerGroundAt = time.Now()

	simY := anchorY
	for tick := uint64(0); tick < 20*30; tick++ {
		// No server corrections arrive after the first tick: the bot holds the
		// Y the server already confirmed, so the server has nothing to fix.
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
		if !tc.IsGrounded {
			t.Fatalf("tick %d: anchor let the bot go airborne (limit cycle returned)", tick)
		}
		if simY != anchorY {
			t.Fatalf("tick %d: simulated Y = %v, want held at %v", tick, simY, anchorY)
		}
	}
}
