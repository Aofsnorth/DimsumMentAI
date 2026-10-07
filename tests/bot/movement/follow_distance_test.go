package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"

	"github.com/go-gl/mathgl/mgl32"
)

// A following bot that never refreshed DistToPlayer read 0 ("already next to
// the player"), so the walk latch never engaged and it stood still while A*
// kept planning routes. The distance has to come from the tracked player.
func TestFollowDistanceIsRefreshedAndEngagesWalking(t *testing.T) {
	t.Parallel()

	botFeet := mgl32.Vec3{0, 64, 0}
	b := newFollowTestBot(botFeet, mgl32.Vec3{6, 64, 0})

	tc := &movement.TickContext{B: b, CurrPos: botFeet, MState: "follow", TPlayer: "Arthenyxx"}
	tc.UpdateDistanceToPlayer()
	if tc.DistToPlayer < 5.9 || tc.DistToPlayer > 6.1 {
		t.Fatalf("DistToPlayer = %v, want ~6", tc.DistToPlayer)
	}

	tc.UpdateShouldMoveState()
	if !tc.FollowWalking || !tc.WantsToMove() {
		t.Fatal("player 6 blocks away: follow latch must be walking")
	}
}
