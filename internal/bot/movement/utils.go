package movement

import (
	"math"
	"time"

	"bedrock-ai/internal/bot"

	"github.com/go-gl/mathgl/mgl32"
)

// organicLookDrift produces a smooth, non-repeating micro-drift for head yaw
// and pitch using a sum of incommensurate sine frequencies. This mimics the
// subtle, continuous motion of a human head that is never perfectly still —
// breathing, micro-saccades, and postural sway — without the mechanical
// pattern of fixed tick-parity jumps. The frequencies are chosen to be
// mutually irrational so the combined waveform never repeats within any
// practical session length.
func organicLookDrift(tick uint64, ampYaw, ampPitch float32) (float32, float32) {
	t := float64(tick)
	yawDrift := ampYaw * float32(
		math.Sin(t*0.0131)+
			0.35*math.Sin(t*0.0297+1.7)+
			0.15*math.Sin(t*0.0613+3.1),
	)
	pitchDrift := ampPitch * float32(
		math.Sin(t*0.0183+0.5)+
			0.30*math.Sin(t*0.0411+2.3)+
			0.12*math.Sin(t*0.0791+4.7),
	)
	return yawDrift, pitchDrift
}

// InterpolateAngle smoothly moves current angle towards target by at most maxStep, handling 360 wrap-around
func InterpolateAngle(current, target, maxStep float32) float32 {
	diff := target - current
	for diff < -180 {
		diff += 360
	}
	for diff > 180 {
		diff -= 360
	}
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	res := current + diff
	for res < 0 {
		res += 360
	}
	for res >= 360 {
		res -= 360
	}
	return res
}

// InterpolatePitch smoothly moves current pitch towards target by at most maxStep
func InterpolatePitch(current, target, maxStep float32) float32 {
	diff := target - current
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	res := current + diff
	if res > 90 {
		res = 90
	} else if res < -90 {
		res = -90
	}
	return res
}

// EaseAngle moves current yaw towards target using ease-out: the step is a
// fraction of the remaining angle (so it slows as it approaches) clamped to
// [minStep, maxStep]. Handles 360 wrap-around. Unlike InterpolateAngle (constant
// speed → robotic), this produces a human-like decelerating head turn.
// minStep keeps tiny remaining angles from stalling into a stiff crawl.
func EaseAngle(current, target, minStep, maxStep, ease float32) float32 {
	diff := target - current
	for diff < -180 {
		diff += 360
	}
	for diff > 180 {
		diff -= 360
	}

	mag := diff
	if mag < 0 {
		mag = -mag
	}
	if mag < 0.05 {
		return current
	}

	step := mag * ease
	if step > maxStep {
		step = maxStep
	}
	if step < minStep {
		step = minStep
	}
	if step > mag {
		step = mag
	}
	if diff < 0 {
		step = -step
	}

	res := current + step
	for res < 0 {
		res += 360
	}
	for res >= 360 {
		res -= 360
	}
	return res
}

// EasePitch is the pitch counterpart of EaseAngle (no wrap-around; clamped to
// [-90, 90]).
func EasePitch(current, target, minStep, maxStep, ease float32) float32 {
	diff := target - current
	mag := diff
	if mag < 0 {
		mag = -mag
	}
	if mag < 0.05 {
		return current
	}

	step := mag * ease
	if step > maxStep {
		step = maxStep
	}
	if step < minStep {
		step = minStep
	}
	if step > mag {
		step = mag
	}
	if diff < 0 {
		step = -step
	}

	res := current + step
	if res > 90 {
		res = 90
	} else if res < -90 {
		res = -90
	}
	return res
}

func StopMovement(b *bot.Bot) {
	b.Stop()
}

func LookAt(b *bot.Bot, pos mgl32.Vec3) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	dx := pos.X() - b.Pos.X()
	// Use eye-height (feet + 1.62) for pitch so the bot actually looks at
	// the target point rather than below it. Matches setLookTarget in
	// control.go which the idle look loop uses for interpolation.
	dy := pos.Y() - (b.Pos.Y() + 1.62)
	dz := pos.Z() - b.Pos.Z()

	distH := math.Sqrt(float64(dx*dx + dz*dz))
	if distH < 0.001 {
		distH = 0.001
	}

	yawRad := math.Atan2(float64(dz), float64(dx))
	yawDeg := yawRad * 180 / math.Pi
	b.Yaw = float32(yawDeg) - 90
	if b.Yaw < 0 {
		b.Yaw += 360
	}
	// Keep head yaw in sync with body yaw for an explicit look-at so the
	// next eased tick doesn't have to re-converge from a stale head angle.
	b.HeadYaw = b.Yaw

	pitchRad := -math.Atan2(float64(dy), distH)
	b.Pitch = float32(pitchRad * 180 / math.Pi)

	// Lock the idle look target so the movement loop doesn't
	// immediately override the gaze on the next tick.
	b.IdleLookTargetYaw = b.Yaw
	b.IdleLookTargetPitch = b.Pitch
	b.IdleLookTargetType = "block"
	b.IdleLookTargetPos = pos
	b.NextIdleLookChange = time.Now().Add(3 * time.Second)
}
