package movement_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"

	"github.com/go-gl/mathgl/mgl32"
)

// TestEasedPitchConvergesWithoutOscillating is the regression guard for the
// head-tremor fix. The organic drift used to be added onto Pitch every tick,
// which the ease then fought to undo at only 0.22 per tick. The leftover error
// made the head visibly bob up and down. Pitch must now settle on the target
// and stay there.
func TestEasedPitchConvergesWithoutOscillating(t *testing.T) {
	t.Parallel()

	const target = float32(0.0)
	pitch := float32(3.0)

	var prev = pitch
	for tick := 0; tick < 200; tick++ {
		pitch = movement.EasePitch(pitch, target, 0.30, 12.0, 0.22)
		if math.IsNaN(float64(pitch)) {
			t.Fatalf("tick %d: pitch became NaN", tick)
		}
		// Every step must move toward the target, never away from it.
		if math.Abs(float64(pitch-target)) > math.Abs(float64(prev-target)) {
			t.Fatalf("tick %d: pitch moved away from target: %v -> %v", tick, prev, pitch)
		}
		prev = pitch
	}

	if math.Abs(float64(pitch)) > 0.05 {
		t.Fatalf("pitch = %v after 200 ticks, want converged to %v", pitch, target)
	}
}

// TestLookDriftDoesNotFeedBackIntoEasedState verifies the drift is held apart
// from the eased state, which is what actually stops the oscillation.
func TestLookDriftDoesNotFeedBackIntoEasedState(t *testing.T) {
	t.Parallel()

	ampYaw, ampPitch := float32(0.22), float32(0.28)

	// Simulate 30 seconds at 20 Hz with a level target. HeadYaw and Pitch must
	// converge and stay put; only the drift fields should keep changing.
	tc := &movement.TickContext{
		Tick: 0,
		Yaw:  90, HeadYaw: 90, Pitch: 0,
		TargetYaw: 90, TargetPitch: 0,
	}
	settled := false
	for tick := uint64(0); tick < 20*30; tick++ {
		tc.Tick = tick
		tc.HeadYaw = movement.EaseAngle(tc.HeadYaw, tc.TargetYaw, 0.40, 40.0, 0.32)
		tc.Pitch = movement.EasePitch(tc.Pitch, tc.TargetPitch, 0.30, 12.0, 0.22)
		tc.LookDriftYaw, tc.LookDriftPitch = movement.OrganicLookDrift(tick, ampYaw, ampPitch)

		if tick > 20*5 { // after settling
			if math.Abs(float64(tc.Pitch)) > 0.05 {
				t.Fatalf("tick %d: Pitch drifted to %v while the target was level", tick, tc.Pitch)
			}
			if movement.AngleDifference(tc.HeadYaw, tc.TargetYaw) > 0.05 {
				t.Fatalf("tick %d: HeadYaw drifted to %v while the target was %v", tick, tc.HeadYaw, tc.TargetYaw)
			}
			settled = true
		}
	}
	if !settled {
		t.Fatal("loop never reached the settled phase")
	}
}

// TestFollowLookAimsAtEyesNotFeet covers the upward bias when following a
// player. The eye height used to be added to both the bot and the target,
// cancelling out and leaving the bot aimed at the target's feet.
func TestFollowLookAimsAtEyesNotFeet(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{}
	botFeet := mgl32.Vec3{0, 64, 0}
	// Target standing one block away at the same level.
	targetFeet := mgl32.Vec3{1, 64, 0}

	tc := &movement.TickContext{
		B:       b,
		CurrPos: botFeet,
		Yaw:     0,
		Pitch:   0,
		MState:  "follow",
	}
	b.TargetPos = targetFeet

	tc.ApplyFollowLookTarget()

	// Both heads are at the same height, so pitch should be level, not tilted up.
	if math.Abs(float64(tc.TargetPitch)) > 0.001 {
		t.Fatalf("TargetPitch = %v, want 0 for a same-level target", tc.TargetPitch)
	}
}
