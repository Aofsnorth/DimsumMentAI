package movement_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/bot/movement"
)

func TestWalkingHeadTargetStaysBounded(t *testing.T) {
	const targetYaw float32 = 359
	const targetPitch float32 = 0

	var left, right, down bool
	var lastYawOffset, lastPitchOffset float32
	var smooth bool
	for tick := uint64(0); tick < 2000; tick++ {
		headYaw, pitch := movement.WalkingHeadTarget(tick, targetYaw, targetPitch, true)
		yawOffset := movement.AngleDifference(headYaw, targetYaw)
		pitchOffset := pitch - targetPitch
		if float32(math.Abs(float64(yawOffset))) > movement.WalkingGazeMaxYawOffset {
			t.Fatalf("tick %d yaw offset = %.3f, max %.3f", tick, yawOffset, movement.WalkingGazeMaxYawOffset)
		}
		if float32(math.Abs(float64(pitchOffset))) > movement.WalkingGazeMaxPitchOffset {
			t.Fatalf("tick %d pitch offset = %.3f, max %.3f", tick, pitchOffset, movement.WalkingGazeMaxPitchOffset)
		}
		if yawOffset < -movement.WalkingGazeYawAmplitude/2 {
			left = true
		}
		if yawOffset > movement.WalkingGazeYawAmplitude/2 {
			right = true
		}
		if pitchOffset > movement.WalkingGazeDownwardBias {
			down = true
		}
		if tick > 0 && math.Abs(float64(yawOffset-lastYawOffset)) < 2 && math.Abs(float64(pitchOffset-lastPitchOffset)) < 2 {
			smooth = true
		}
		lastYawOffset = yawOffset
		lastPitchOffset = pitchOffset
	}
	if !left || !right || !down {
		t.Fatalf("walking gaze phases missing: left=%t right=%t down=%t", left, right, down)
	}
	if !smooth {
		t.Fatal("walking gaze phase moved abruptly")
	}
}

func TestWalkingHeadTargetDisabledPreservesForcedTarget(t *testing.T) {
	const targetYaw float32 = 123
	const targetPitch float32 = -28

	headYaw, pitch := movement.WalkingHeadTarget(731, targetYaw, targetPitch, false)
	if headYaw != targetYaw || pitch != targetPitch {
		t.Fatalf("disabled scan changed target to yaw %.3f pitch %.3f", headYaw, pitch)
	}
}

func TestWalkingBodyYawUsesMovementTarget(t *testing.T) {
	tc := &movement.TickContext{
		Tick:              731,
		Yaw:               80,
		HeadYaw:           120,
		Pitch:             0,
		TargetYaw:         90,
		TargetPitch:       0,
		HasHorizontalMove: true,
	}

	bodySpeed := movement.SmoothSpeedMultiplier(tc.Tick, 0.22, 4.3)
	wantBodyYaw := movement.EaseAngle(tc.Yaw, tc.TargetYaw, 0.50*bodySpeed, 16.0*bodySpeed, 0.30*bodySpeed)

	tc.ApplyEasedLook(true, 40, 28)

	if tc.Yaw != wantBodyYaw {
		t.Fatalf("body yaw = %.3f, want movement-target yaw %.3f", tc.Yaw, wantBodyYaw)
	}
	if math.Abs(float64(movement.AngleDifference(tc.HeadYaw, tc.Yaw))) < 0.1 {
		t.Fatalf("body yaw %.3f followed scanned head yaw %.3f", tc.Yaw, tc.HeadYaw)
	}
	if offset := float32(math.Abs(float64(movement.AngleDifference(tc.HeadYaw, tc.TargetYaw)))); offset > movement.WalkingGazeMaxYawOffset {
		t.Fatalf("head yaw offset = %.3f, max %.3f", offset, movement.WalkingGazeMaxYawOffset)
	}
}
