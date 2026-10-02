// Idle gaze: how the bot's head behaves while it is standing still.
//
// The old implementation picked a fresh random look target every couple of
// seconds: a uniform ±45° yaw jump, a uniform ±7° pitch, a hold time drawn
// uniformly from a fixed range. That is visibly wrong in three separate ways,
// and each one is a different tell:
//
//  1. UNIFORM AMPLITUDE. Real saccades are heavily skewed small: most eye
//     movements are a few degrees of correction within one area of interest,
//     and only occasionally is the whole head re-oriented. A uniform ±45°
//     jump every two seconds is the "robot scanning the room" look.
//  2. UNIFORM DURATION. Gaze fixations are not evenly spaced in time. A
//     person rests somewhere for a fraction of a second, glances across, and
//     only settles on something when it actually holds their attention. The
//     bot instead held every target for the same 2-5 seconds, so it read as a
//     metronome.
//  3. NO MEMORY. It re-picked the same block over and over, with no sense that
//     it had just looked there.
//
// The model here is a three-part gaze cycle: a fixation on a point, occasional
// micro-saccades inside that point, then a re-fixation somewhere new whose
// amplitude and duration both come from skewed distributions.
package movement

import (
	"bedrock-ai/internal/bot/rand"
	"math"
)

const (
	// fixMinSec/fixMaxSec bound a fixation. The distribution inside is skewed
	// (see SampleFixation), so most fixations are short and a few are long —
	// which is how a person actually looks at a scene.
	fixMinSec = 0.35
	fixMaxSec = 5.5

	// microProb is the chance, per tick, that the head makes a small corrective
	// movement while holding a fixation. This is the difference between a
	// statue that stares and a person whose eyes are never perfectly still.
	microProb = 0.045

	// MicroMaxDeg is the size of a corrective micro-saccade. Small enough to
	// read as attention rather than as a turn.
	MicroMaxDeg = 4.5

	// revisitChance is the probability that a new fixation lands close to the
	// previous one rather than sweeping across the scene. People re-check the
	// thing they were just looking at; they do not always move on.
	revisitChance = 0.30

	// returnRangeDeg bounds how far a "revisit" may drift from the last
	// fixation, so it reads as a return rather than as a new target.
	returnRangeDeg = 14.0

	// scanRangeDeg is the scale of a deliberate scan to a new point of
	// interest. It is the CAP, not the typical value — see
	// SampleSaccadeAmplitude: the distribution is heavily skewed so a large
	// re-orient is the exception, not the rule.
	scanRangeDeg = 84.0

	// saccadeSkew controls how sharply saccade amplitude concentrates near
	// zero. At 6, the mean lands at roughly a seventh of the cap: most
	// movements are a few degrees of correction, and only a small tail of them
	// re-orients the head. A lower exponent would put the mean back up near
	// the old uniform value, which is exactly the "scanning turret" look.
	saccadeSkew = 6.0

	// horizonPitchDeg is how far above level a scan tends to land. A person
	// looking around a room mostly looks at and slightly below the horizon,
	// because that is where the interesting things are.
	horizonPitchDeg = 4.0

	// groundGlanceChance is the chance a fixation ends in a look at the ground
	// near the bot's feet. Every player does this; a bot that never looks down
	// reads as a floating camera.
	groundGlanceChance = 0.12

	// groundPitchDeg is how far down a ground glance goes.
	groundPitchDeg = 32.0
)

// GazeSample is one planned head movement: where to aim and for how long.
type GazeSample struct {
	DYaw   float32
	DPitch float32
	Sec    float32
}

// NextGazeSample draws the next fixation, relative to the current gaze.
//
// The three branches mirror the three things a person's eyes do while idle:
// glance down at the ground, re-check the thing they were just looking at, or
// move attention somewhere genuinely new.
func NextGazeSample(currentYaw, currentPitch float32) GazeSample {
	roll := rand.Float64()

	switch {
	case roll < groundGlanceChance:
		// Down at the feet. A short, decisive look — which is what makes it
		// read as a glance rather than as the head getting stuck.
		return GazeSample{
			DYaw:   float32(rand.Float64()*18 - 9),
			DPitch: float32(-groundPitchDeg - rand.Float64()*12),
			Sec:    SampleFixation() * 0.6,
		}

	case roll < groundGlanceChance+revisitChance:
		// Back to roughly where it was, with a small correction. This is the
		// "I looked away and back" motion, and the small offset is what keeps
		// it from being a mechanical return to an identical angle.
		return GazeSample{
			DYaw:   float32(rand.Float64()*2*returnRangeDeg - returnRangeDeg),
			DPitch: float32(rand.Float64()*8 - 4),
			Sec:    SampleFixation(),
		}

	default:
		// A genuine scan. The amplitude is skewed so most scans are a modest
		// turn and only some re-orient the whole head.
		amp := SampleSaccadeAmplitude()
		bias := float32(rand.Float64()) // sign
		if bias < 0.5 {
			amp = -amp
		}
		// Scan pitch hovers around slightly-below horizon, with a spread.
		pitch := float32(horizonPitchDeg) + float32(rand.Float64()*16-10)
		if pitch > 0 {
			pitch = -pitch // Bedrock pitch is negative looking down
		}
		return GazeSample{
			DYaw:   amp,
			DPitch: pitch - currentPitch,
			Sec:    SampleFixation(),
		}
	}
}

// SampleFixation draws a hold duration.
//
// The distribution is the point: two thirds of fixations are short (under a
// second and a half, the restless scanning of someone with nothing to do) and
// the rest are long. A uniform draw from a fixed range — what this did before —
// produces an evenly spaced rhythm that reads as a loop.
func SampleFixation() float32 {
	// Two draws summed: the second is scaled down, so the result clusters near
	// the low end with a long, sparse tail. That is the shape of real fixation
	// durations, and it is what breaks the metronome.
	t := rand.Float64()
	if t < 0.70 {
		return float32(fixMinSec + (fixMaxSec-fixMinSec)*0.28*t/0.70)
	}
	// The tail: rarely, the bot settles on something for a while.
	return float32(fixMinSec + (fixMaxSec-fixMinSec)*(0.28+0.72*(t-0.70)/0.30))
}

// SampleSaccadeAmplitude draws a scan size in degrees, skewed small.
//
// Most saccades are a few degrees; a large re-orient is the exception. Raising
// a uniform draw to saccadeSkew concentrates the mass near zero while still
// leaving a sparse tail, which is the shape real saccade amplitudes have — and
// which is what the old uniform ±45° jump did not have.
func SampleSaccadeAmplitude() float32 {
	t := rand.Float64()
	return float32(scanRangeDeg * math.Pow(t, saccadeSkew))
}

// MicroSaccade returns a tiny corrective offset to apply inside a fixation.
// Returns zero most ticks, which is what makes the motion read as organic
// rather than as jitter.
func MicroSaccade() (dYaw, dPitch float32) {
	if rand.Float64() > microProb {
		return 0, 0
	}
	return float32(rand.Float64()*2*MicroMaxDeg - MicroMaxDeg),
		float32(rand.Float64()*2*MicroMaxDeg*0.6 - MicroMaxDeg*0.6)
}

// GazePitchFloor keeps an idle scan from tipping the head into the ground or
// the sky, where the geometry would look broken rather than thoughtful.
func GazePitchFloor(pitch float32) float32 {
	return float32(math.Max(float64(pitch), -70))
}
