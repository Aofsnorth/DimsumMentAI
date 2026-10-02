package movement_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// newLookTestBot builds a stationary bot for look-pipeline tests. Path bounds
// point at a distant node so the world model's unloaded-chunk fallback does not
// pretend there is a floor under the bot.
func newLookTestBot(feet mgl32.Vec3) *bot.Bot {
	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(pathfinder.Node{X: 500, Y: 120, Z: 500}, pathfinder.Node{X: 520, Y: 120, Z: 520})
	return &bot.Bot{
		MovementState: "idle",
		WorldModel:    model,
		Pos:           feet,
	}
}

// TestStationaryLookSmoothingHoldsLevelCloseBy guards the tracking tremor fix.
// A player standing next to the bot reports a position that alternates between
// two quantized values. When the raw pitch from that position sat near the old
// hard deadzone threshold (a few centimetres of real height difference), the
// target snapped between 0 and the raw angle every tick and the head visibly
// bobbed. The smoothed pipeline must keep the frame-to-frame pitch change far
// below anything the eye can read as motion.
func TestStationaryLookSmoothingHoldsLevelCloseBy(t *testing.T) {
	t.Parallel()

	botFeet := mgl32.Vec3{0, 64, 0}
	eye := botFeet.Y() + 1.62

	// The player hovers ~7 cm above the bot's eye line: raw pitch ~2.7 deg at
	// 1.5 blocks, right where the old deadzone used to chatter. The reported
	// position alternates ±4 cm every packet.
	const dist = float32(1.5)
	const baseDy = float32(0.07)

	tc := &movement.TickContext{
		B:                 newLookTestBot(botFeet),
		CurrPos:           botFeet,
		Yaw:               90,
		Pitch:             0,
		SmoothedLookYaw:   90,
		SmoothedLookPitch: 0,
	}

	var maxStep, minPitch, maxPitch float64
	prevPitch := tc.Pitch
	const settleTicks = uint64(100)
	for tick := uint64(0); tick < 20*20; tick++ {
		jitter := float32(0.04)
		if tick%2 == 0 {
			jitter = -0.04
		}
		target := mgl32.Vec3{dist, eye + baseDy + jitter, 0}
		tc.TargetYaw, tc.TargetPitch = movement.NaturalLookAngles(botFeet, target, 90)
		tc.SmoothStationaryLookTarget()
		tc.Pitch = movement.EasePitch(tc.Pitch, tc.TargetPitch, 0.30, 12.0, 0.22)

		if tick < settleTicks {
			prevPitch = tc.Pitch
			continue
		}
		// After settling, measure the residual wobble. The old hard deadzone
		// made this input snap the target between 0 and ~4 deg every tick; the
		// smoothed pipeline must leave a flat line.
		step := math.Abs(float64(tc.Pitch - prevPitch))
		if step > maxStep {
			maxStep = step
		}
		if float64(tc.Pitch) < minPitch || tick == settleTicks {
			minPitch = float64(tc.Pitch)
		}
		if float64(tc.Pitch) > maxPitch || tick == settleTicks {
			maxPitch = float64(tc.Pitch)
		}
		prevPitch = tc.Pitch
	}

	// The old pipeline produced ~4 deg peak-to-peak chatter on this input.
	// Anything under half a degree is a flat line to the eye.
	if peak := maxPitch - minPitch; peak > 0.5 {
		t.Fatalf("settled pitch peak-to-peak = %.3f deg, want <= 0.5 (old chatter was ~4 deg)", peak)
	}
	if maxStep > 0.2 {
		t.Fatalf("max settled pitch step = %.4f deg/tick, want <= 0.2", maxStep)
	}
	// The head must settle at the honest mean angle (the player really is
	// ~7 cm above the bot's eye line, and Bedrock pitch is negative looking
	// up), not wander and not clamp to level.
	const honestMean = -2.67
	if math.Abs(float64(tc.Pitch)-honestMean) > 0.6 {
		t.Fatalf("settled pitch = %.3f, want near the honest mean %.2f", tc.Pitch, honestMean)
	}
}

// TestStationaryLookConvergesFully guards the sticky-bias fix. The old
// dampenLookJitter froze the target to the current angle whenever they were
// within 1.4 deg, so a head that started 2.5 deg off never returned to level —
// it stayed tilted, which read as aiming above the player's head. The smoothed
// pipeline must converge to the true target.
func TestStationaryLookConvergesFully(t *testing.T) {
	t.Parallel()

	botFeet := mgl32.Vec3{0, 64, 0}
	eye := botFeet.Y() + 1.62
	// A same-level target: the honest raw pitch is exactly 0.
	target := mgl32.Vec3{1.5, eye, 0}

	tc := &movement.TickContext{
		B:                 newLookTestBot(botFeet),
		CurrPos:           botFeet,
		Yaw:               90,
		Pitch:             -2.5, // head left tilted up from a previous glance
		SmoothedLookYaw:   90,
		SmoothedLookPitch: -2.5,
	}

	for tick := uint64(0); tick < 20*5; tick++ {
		tc.TargetYaw, tc.TargetPitch = movement.NaturalLookAngles(botFeet, target, 90)
		tc.SmoothStationaryLookTarget()
		tc.Pitch = movement.EasePitch(tc.Pitch, tc.TargetPitch, 0.30, 12.0, 0.22)
	}

	if math.Abs(float64(tc.Pitch)) > 0.3 {
		t.Fatalf("pitch = %.3f after 5 s, want converged to level (old freeze left ~1.4 deg residue)", tc.Pitch)
	}
}

// TestStationaryLookTracksLargeVerticalOffset makes sure the smoothing did not
// flatten genuinely useful look angles: a target well above or below must still
// be looked at, just without chatter.
func TestStationaryLookTracksLargeVerticalOffset(t *testing.T) {
	t.Parallel()

	botFeet := mgl32.Vec3{0, 64, 0}

	tests := []struct {
		name       string
		targetFeet float32
		wantPitch  float32 // sign: negative looks up, positive looks down
	}{
		{"far above", 68, -1},
		{"far below", 60, +1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target := mgl32.Vec3{6, tt.targetFeet + 1.62, 0}
			tc := &movement.TickContext{
				B:                 newLookTestBot(botFeet),
				CurrPos:           botFeet,
				Yaw:               90,
				Pitch:             0,
				SmoothedLookYaw:   90,
				SmoothedLookPitch: 0,
			}

			for tick := uint64(0); tick < 20*5; tick++ {
				tc.TargetYaw, tc.TargetPitch = movement.NaturalLookAngles(botFeet, target, 90)
				tc.SmoothStationaryLookTarget()
				tc.Pitch = movement.EasePitch(tc.Pitch, tc.TargetPitch, 0.30, 12.0, 0.22)
			}

			if math.Abs(float64(tc.Pitch)) < 3.0 {
				t.Fatalf("target %+v: pitch = %+.2f, want a clearly non-zero angle", target, tc.Pitch)
			}
			// Bedrock pitch is positive looking down, so a target above the bot
			// must produce a negative pitch and vice versa.
			if tt.wantPitch < 0 && tc.Pitch >= 0 {
				t.Fatalf("target above the bot: pitch = %+.2f, want negative (looking up)", tc.Pitch)
			}
			if tt.wantPitch > 0 && tc.Pitch <= 0 {
				t.Fatalf("target below the bot: pitch = %+.2f, want positive (looking down)", tc.Pitch)
			}
		})
	}
}

// TestNaturalLookAnglesReportsRawGeometry pins the new contract for
// NaturalLookAngles: it computes honest raw angles with only sanity clamps. The
// noise rejection moved to SmoothStationaryLookTarget, which tests above cover.
func TestNaturalLookAnglesReportsRawGeometry(t *testing.T) {
	t.Parallel()

	botFeet := mgl32.Vec3{0, 64, 0}
	eye := botFeet.Y() + 1.62

	for _, dy := range []float32{-0.05, -0.02, 0, 0.02, 0.05} {
		for _, dist := range []float32{1.0, 1.5, 2.0, 2.5} {
			target := mgl32.Vec3{dist, eye + dy, 0}
			_, pitch := movement.NaturalLookAngles(botFeet, target, 90)
			want := float32(-math.Atan2(float64(dy), float64(dist)) * 180 / math.Pi)
			if math.Abs(float64(pitch-want)) > 0.01 {
				t.Fatalf("dist=%.1f dy=%+.2f: pitch = %+.2f, want raw %+.2f",
					dist, dy, pitch, want)
			}
		}
	}
}

// TestSoftenLevelBandIsContinuous pins the property that replaced the hard
// deadzone: the soft band is continuous at its edge and ~0 at level, so an
// input hovering at the edge cannot make the output snap.
func TestSoftenLevelBandIsContinuous(t *testing.T) {
	t.Parallel()

	if got := movement.SoftenLevelBand(0); got != 0 {
		t.Fatalf("SoftenLevelBand(0) = %v, want 0", got)
	}
	if got := movement.SoftenLevelBand(movement.LookLevelBand); got != movement.LookLevelBand {
		t.Fatalf("SoftenLevelBand(band edge %v) = %v, want passthrough", movement.LookLevelBand, got)
	}
	if got := movement.SoftenLevelBand(-movement.LookLevelBand); got != -movement.LookLevelBand {
		t.Fatalf("SoftenLevelBand(-band edge %v) = %v, want passthrough", -movement.LookLevelBand, got)
	}
	if got := movement.SoftenLevelBand(2 * movement.LookLevelBand); got != 2*movement.LookLevelBand {
		t.Fatalf("SoftenLevelBand(above band) = %v, want passthrough", got)
	}
	// Just inside the band must be close to just outside it — no snap.
	inside := movement.SoftenLevelBand(movement.LookLevelBand - 0.01)
	if math.Abs(float64(inside-(movement.LookLevelBand-0.01))) > 0.02 {
		t.Fatalf("SoftenLevelBand near edge = %v, discontinuous", inside)
	}
	// Mid-band must be strongly attenuated but sign-correct.
	if got := movement.SoftenLevelBand(0.75); got <= 0 || got > 0.4 {
		t.Fatalf("SoftenLevelBand(0.75) = %v, want small positive", got)
	}
	if got := movement.SoftenLevelBand(-0.75); got >= 0 || got < -0.4 {
		t.Fatalf("SoftenLevelBand(-0.75) = %v, want small negative", got)
	}
}

// TestPinnedStaticLookBypassesSmoothing guards action code (drop aiming) that
// pins exact angles and waits for the head to reach them: the EMA must not add
// its half-second of lag on top.
func TestPinnedStaticLookBypassesSmoothing(t *testing.T) {
	t.Parallel()

	botFeet := mgl32.Vec3{0, 64, 0}
	b := newLookTestBot(botFeet)
	b.IdleLookTargetType = "static"

	tc := &movement.TickContext{
		B:                 b,
		CurrPos:           botFeet,
		TargetYaw:         200,
		TargetPitch:       -28,
		SmoothedLookYaw:   90,
		SmoothedLookPitch: 0,
	}

	tc.SmoothStationaryLookTarget()

	if tc.TargetYaw != 200 || tc.TargetPitch != -28 {
		t.Fatalf("pinned look was smoothed: yaw=%.2f pitch=%.2f, want passthrough 200/-28", tc.TargetYaw, tc.TargetPitch)
	}
	if tc.SmoothedLookYaw != 200 || tc.SmoothedLookPitch != -28 {
		t.Fatalf("smoothed state not synced to pin: yaw=%.2f pitch=%.2f", tc.SmoothedLookYaw, tc.SmoothedLookPitch)
	}
}

// TestOrganicLookDriftStaysBelowVisibleTremorThreshold guards the frequency and
// amplitude of the idle drift. Terms above ~1 Hz read as a vibration rather than
// a slow drift, and per-tick amplitude past roughly a third of a degree is
// visible as rocking.
func TestOrganicLookDriftStaysBelowVisibleTremorThreshold(t *testing.T) {
	t.Parallel()

	const (
		idleYawAmp   = 0.22
		idlePitchAmp = 0.28
	)

	var (
		maxYawStep   float64
		maxPitchStep float64
	)
	prevYaw, prevPitch := movement.OrganicLookDrift(0, idleYawAmp, idlePitchAmp)

	for tick := uint64(1); tick <= 20*60; tick++ {
		yaw, pitch := movement.OrganicLookDrift(tick, idleYawAmp, idlePitchAmp)
		if d := math.Abs(float64(yaw) - float64(prevYaw)); d > maxYawStep {
			maxYawStep = d
		}
		if d := math.Abs(float64(pitch) - float64(prevPitch)); d > maxPitchStep {
			maxPitchStep = d
		}
		prevYaw, prevPitch = yaw, pitch
	}

	// At 20 Hz, anything above ~0.05 deg/tick reads as a buzz.
	if maxYawStep > 0.05 {
		t.Fatalf("max yaw step = %.4f deg/tick, want <= 0.05", maxYawStep)
	}
	if maxPitchStep > 0.05 {
		t.Fatalf("max pitch step = %.4f deg/tick, want <= 0.05", maxPitchStep)
	}
}
