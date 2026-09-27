package movement

import (
	"math"
	"testing"
)

// TestFixationDurationsAreSkewedNotUniform is the anti-metronome guard.
//
// The old idle look drew its hold time uniformly from a 2-5 second range, which
// produced an evenly spaced rhythm. A person resting somewhere does the
// opposite: most glances are brief, and settling on something is the
// exception. A uniform sample's mean sits at the midpoint of its range and its
// distribution is flat; the skewed sample must sit well below that midpoint.
func TestFixationDurationsAreSkewedNotUniform(t *testing.T) {
	t.Parallel()

	const samples = 20000
	var sum, below, above float64
	for i := 0; i < samples; i++ {
		d := float64(sampleFixation())
		sum += d
		if d < 1.5 {
			below++
		}
		if d > 3.0 {
			above++
		}
	}
	mean := sum / samples
	shortFrac := below / samples
	longFrac := above / samples

	// A uniform draw over [0.35, 5.5] would mean ~2.93s. The skewed sample must
	// be clearly below that.
	if mean > 2.2 {
		t.Errorf("mean fixation = %.2fs, want clearly below the uniform midpoint 2.93s", mean)
	}
	// Most glances are short...
	if shortFrac < 0.55 {
		t.Errorf("short fixations = %.0f%%, want the majority under 1.5s", shortFrac*100)
	}
	// ...but a real tail exists, so the head still settles sometimes.
	if longFrac < 0.02 {
		t.Errorf("long fixations = %.1f%%, want a sparse tail above 3s", longFrac*100)
	}
}

// TestSaccadeAmplitudesAreSkewedSmall is the anti-"robot scanning" guard.
//
// Real saccades are small far more often than they are large. The old code
// jumped a uniform ±45° every couple of seconds, which reads as a turret
// sweeping for targets. A cubic draw must put most movements under 20°.
func TestSaccadeAmplitudesAreSkewedSmall(t *testing.T) {
	t.Parallel()

	const samples = 20000
	var small, large, sum float64
	for i := 0; i < samples; i++ {
		amp := float64(sampleSaccadeAmplitude())
		sum += amp
		if amp < 20 {
			small++
		}
		if amp > 70 {
			large++
		}
	}
	smallFrac := small / samples
	largeFrac := large / samples
	mean := sum / samples

	if smallFrac < 0.7 {
		t.Errorf("small saccades (<20 deg) = %.0f%%, want the clear majority", smallFrac*100)
	}
	// The old uniform ±45° meant the mean magnitude was 22.5°. The skewed
	// sample must be well under that.
	if mean > 14 {
		t.Errorf("mean saccade = %.1f deg, want well under the uniform 22.5 deg", mean)
	}
	// But the head must still be able to re-orient, not only twitch.
	if largeFrac > 0.12 {
		t.Errorf("large saccades (>70 deg) = %.1f%%, want rare but possible", largeFrac*100)
	}
}

// TestGroundGlancesActuallyHappen guards the detail that separates a person
// from a floating camera: everybody looks down at their own feet sometimes.
func TestGroundGlancesActuallyHappen(t *testing.T) {
	t.Parallel()

	const samples = 20000
	downs := 0
	for i := 0; i < samples; i++ {
		sample := nextGazeSample(0, 0)
		if sample.dPitch < -20 {
			downs++
		}
	}
	frac := float64(downs) / samples
	if frac < 0.05 || frac > 0.30 {
		t.Errorf("downward glances = %.0f%%, want a noticeable but not constant share", frac*100)
	}
}

// TestGazeNeverTipsIntoGroundOrSky keeps the saccade model inside a believable
// head range. A fixation that lands at ±90° reads as a bug, not as looking
// somewhere.
func TestGazeNeverTipsIntoGroundOrSky(t *testing.T) {
	t.Parallel()

	for _, in := range []float32{-90, -45, 0, 45, 90} {
		if got := gazePitchFloor(in); got < -70 {
			t.Errorf("gazePitchFloor(%v) = %v, want clamped above -70", in, got)
		}
	}
	if got := gazePitchFloor(-30); got != -30 {
		t.Errorf("gazePitchFloor(-30) = %v, want a normal pitch left alone", got)
	}
}

// TestMicroSaccadesAreRareAndTiny pins the corrective jitter: it must be small
// enough to read as attention and rare enough not to read as vibration.
func TestMicroSaccadesAreRareAndTiny(t *testing.T) {
	t.Parallel()

	const samples = 20000
	nonZero := 0
	for i := 0; i < samples; i++ {
		yaw, pitch := microSaccade()
		if yaw != 0 || pitch != 0 {
			nonZero++
			if math.Abs(float64(yaw)) > microMaxDeg+0.01 {
				t.Fatalf("micro yaw %.2f exceeds the intended bound %.2f", yaw, microMaxDeg)
			}
			if math.Abs(float64(pitch)) > microMaxDeg+0.01 {
				t.Fatalf("micro pitch %.2f exceeds the intended bound %.2f", pitch, microMaxDeg)
			}
		}
	}
	frac := float64(nonZero) / samples
	if frac < 0.01 || frac > 0.15 {
		t.Errorf("micro-saccade rate = %.1f%%, want a small intermittent sprinkle", frac*100)
	}
}
