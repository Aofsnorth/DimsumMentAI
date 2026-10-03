package movement

import "math"

// Stride phase-locks the head bob to travel rather than to wall-clock time.
//
// Why this is not a sine on the tick counter
// -----------------------------------------
// A bob driven by tick * period is a sine wave that keeps oscillating whether or
// not the bot is moving. Stop moving and it keeps bobbing, which reads as a body
// vibrating in place; start moving and it starts at whatever phase the counter
// happened to be at, so the first stride after a stop is a jump to mid-bob.
//
// A real head bobs because feet are landing. One bob cycle is one stride pair,
// and a stride pair covers a fixed distance. Deriving the phase from distance
// actually travelled therefore gives all of the right behaviour for free:
//
//   - stopping stops the bob, because distance stops;
//   - the bob accelerates and decelerates with speed, because a sprint takes
//     longer strides and so completes fewer cycles per second;
//   - the phase is continuous through a stop-and-go, so a restart resumes mid
//     stride rather than snapping to zero.
//
// The frequencies that fall out of this are the ones human walking produces:
// at the walk stride length a step is about 1.1 m, and at 4.3 m/s that is
// roughly 3.9 Hz -- the upper end of the measured human range, as expected for a
// game body whose stride is slightly longer than a real one.

// Bob amplitudes, in degrees. Human walking head pitch nod measures about
// 1.5-3.5 degrees peak-to-peak, with the lateral yaw component far smaller; a
// sprint lifts the pitch amplitude rather than the yaw, because the bigger
// stride displaces the head vertically more than sideways.
const (
	walkBobPitch   float32 = 1.8
	sprintBobPitch float32 = 2.4
	walkBobYaw     float32 = 0.5
	sprintBobYaw   float32 = 0.7

	// walkStrideLength is how far one foot covers per step while walking,
	// sprintStrideLength the same while sprinting. The ratio is what sets the
	// bob frequency ratio, and it is why the bob quickens under a sprint
	// without any frequency term being written anywhere.
	walkStrideLength   float32 = 1.10
	sprintStrideLength float32 = 1.45

	// airBobDamping is how much of the bob survives in mid-air. A real head
	// stops nodding once the body has left the ground, so the term collapses to
	// a trace rather than switching off -- a hard cut would be its own tell.
	airBobDamping float32 = 0.22
)

// StrideBob returns the pitch and yaw offset a walking head carries at the given
// distance travelled.
//
// It is a pure function of distance so that it has no state of its own to get
// out of step with the body, which is what lets the phase survive a stop and a
// restart without any bookkeeping beyond the distance itself.
//
// inAir damps rather than cancels the bob; see airBobDamping.
func StrideBob(distance, speed float32, inAir bool) (pitch, yaw float32) {
	if distance <= 0 || speed <= 0 {
		return 0, 0
	}

	stride := walkStrideLength
	if speed > walkSpeedThreshold {
		stride = sprintStrideLength
	}

	phase := float64(distance/stride) * 2 * math.Pi

	// The lateral component leads the vertical one by a quarter cycle: the head
	// is at its highest as it passes over the support foot and at its furthest
	// side as it crosses, so the two are a quarter turn apart rather than in
	// phase. Getting this wrong makes the bob look like a single axis shaking.
	pitch = bobPitchAmplitude(speed) * float32(math.Sin(phase))
	yaw = bobYawAmplitude(speed) * float32(math.Sin(phase+math.Pi/2))

	if inAir {
		pitch *= airBobDamping
		yaw *= airBobDamping
	}
	return pitch, yaw
}

// walkSpeedThreshold is the speed above which the stride is counted as a
// sprint stride. It sits above the vanilla walk speed and below the sprint
// speed so the switch happens when the body is genuinely running rather than
// at a boundary that acceleration crosses repeatedly.
const walkSpeedThreshold float32 = 5.0

// bobPitchAmplitude is the vertical nod. It is larger at a sprint because the
// longer stride displaces the head more vertically than sideways.
func bobPitchAmplitude(speed float32) float32 {
	if speed > walkSpeedThreshold {
		return sprintBobPitch
	}
	return walkBobPitch
}

// bobYawAmplitude is the lateral sway, which stays small in every gait.
func bobYawAmplitude(speed float32) float32 {
	if speed > walkSpeedThreshold {
		return sprintBobYaw
	}
	return walkBobYaw
}

// StridePhase accumulates horizontal travel so the bob can be driven by
// distance. It lives for the life of the connection for the same reason
// GaitState does: the phase has to be continuous across ticks, and a value
// rebuilt every tick would restart the bob from zero on every frame.
type StridePhase struct {
	// cycles is the running bob position, accumulated rather than recomputed
	// from distance. Recomputing it as travelled/stride looks equivalent and is
	// not: stride switches between a walk length and a sprint length with
	// speed, so a recomputed phase jumps by whatever fraction of a cycle the
	// two lengths differ by the instant the speed crosses the threshold.
	// Accumulating cycles keeps the stride length as a rate, so switching it
	// changes how fast the phase advances and never where it is.
	cycles float64
	speed  float32
}

// Advance folds one tick of movement into the phase.
//
// Only the horizontal plane counts. Vertical travel is excluded on purpose: a
// jump carries the body three blocks through the air, and counting it would
// race the bob through several cycles in a single hop.
func (s *StridePhase) Advance(dx, dz float32) {
	if s == nil {
		return
	}
	step := float32(math.Hypot(float64(dx), float64(dz)))
	s.speed = step * ticksPerSecond
	if step <= 0 {
		return
	}
	stride := walkStrideLength
	if s.speed > walkSpeedThreshold {
		stride = sprintStrideLength
	}
	s.cycles += float64(step) / float64(stride)
	// Float64 cannot add 0.0001 to 10^8 without stalling, so a connection left
	// running for a year would eventually freeze its bob at a fixed phase.
	// Folding by whole cycles changes nothing about where the phase is.
	if s.cycles > strideRebaseThreshold {
		s.cycles -= math.Floor(s.cycles)
	}
}

// strideRebaseThreshold is where the cycle accumulator is folded back down. It
// is many cycles wide, so it never lands inside one.
const strideRebaseThreshold = 1 << 20

// ticksPerSecond converts a per-tick distance into the speed the stride length
// is chosen against.
const ticksPerSecond float32 = 20

// Bob returns this tick's head offset, and whether the bot is walking at all.
func (s *StridePhase) Bob(inAir bool) (pitch, yaw float32, walking bool) {
	if s == nil || s.speed <= 0 {
		return 0, 0, false
	}
	phase := s.cycles * 2 * math.Pi
	pitch = bobPitchAmplitude(s.speed) * float32(math.Sin(phase))
	yaw = bobYawAmplitude(s.speed) * float32(math.Sin(phase+math.Pi/2))
	if inAir {
		pitch *= airBobDamping
		yaw *= airBobDamping
	}
	return pitch, yaw, true
}

// SprintingStride reports whether the phase is currently being advanced at the
// sprint stride, which is what makes a walk-to-sprint transition observable
// from outside.
func (s *StridePhase) SprintingStride() bool {
	if s == nil {
		return false
	}
	return s.speed > walkSpeedThreshold
}

// Cycles is the accumulated bob position in whole strides. It exists so a test
// can tell "the phase resumed" apart from "the phase reset", which the bob
// values alone cannot show once the sine has come back round to the same place.
func (s *StridePhase) Cycles() float64 {
	if s == nil {
		return 0
	}
	return s.cycles
}
