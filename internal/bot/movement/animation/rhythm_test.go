package animation

import (
	"math"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

// TestCadenceVariesLikeAHand covers the "natural" side of the rhythm: a fixed
// tick between swings is the clearest tell of a bot, and because each swing is
// also what makes a client emit its dig sound, an unvarying interval is heard
// as a machine-gun rattle. The pause must vary, stay inside its bounds, and
// stay longer between bursts than inside them.
func TestCadenceVariesLikeAHand(t *testing.T) {
	t.Parallel()

	seen := map[time.Duration]int{}
	for swing := 0; swing < 24; swing++ {
		wait := Cadence(swing)
		if wait < SwingMin || wait > RecoveryMax {
			t.Fatalf("swing %d: cadence %v outside [%v, %v]", swing, wait, SwingMin, RecoveryMax)
		}
		seen[wait]++

		isRecoverySlot := swing%BurstLength == BurstLength-1
		if isRecoverySlot && wait < RecoveryMin {
			t.Fatalf("swing %d: recovery slot cadence %v shorter than the recovery floor %v", swing, wait, RecoveryMin)
		}
		if !isRecoverySlot && wait > RecoveryMax {
			t.Fatalf("swing %d: burst cadence %v longer than the burst ceiling", swing, wait)
		}
	}
	if len(seen) < 4 {
		t.Fatalf("cadence produced only %d distinct values over 24 swings, want a varied rhythm", len(seen))
	}
}

// TestCadenceNeverOutrunsTheSwingAnimation is the pacing rule itself: a viewer
// replays the whole ~300ms arm-swing cycle on every Animate packet, so a pause
// under that restarts the cycle mid-flight and the arm vibrates instead of
// swinging.
func TestCadenceNeverOutrunsTheSwingAnimation(t *testing.T) {
	t.Parallel()

	if SwingMin < 250*time.Millisecond {
		t.Fatalf("swing floor %v is under the ~250ms animation cycle; viewers see a vibration, not a swing", SwingMin)
	}
	if SwingMax <= SwingMin {
		t.Fatalf("burst range [%v, %v] is empty", SwingMin, SwingMax)
	}
	if RecoveryMin <= SwingMax {
		t.Fatalf("recovery floor %v does not sit above the burst ceiling %v, so a burst is indistinguishable", RecoveryMin, SwingMax)
	}
	if RecoveryMax <= RecoveryMin {
		t.Fatalf("recovery range [%v, %v] is empty", RecoveryMin, RecoveryMax)
	}
}

// TestWindUpStaysInBounds checks the tool raise before the first swing: long
// enough to read as a wind-up, short enough not to eat the break time and short
// enough to stay a separate beat from the swings.
func TestWindUpStaysInBounds(t *testing.T) {
	t.Parallel()

	for i := 0; i < 20; i++ {
		w := WindUp()
		if w < WindUpMin || w > WindUpMax {
			t.Fatalf("wind-up %v outside [%v, %v]", w, WindUpMin, WindUpMax)
		}
	}
	if WindUpMin >= SwingMin {
		t.Fatal("wind-up floor should stay under the swing floor so it reads as a separate beat")
	}
}

// TestJitteredAimStaysOnTarget checks the aim drifts a little instead of being
// welded to the exact block centre, but never far enough to miss.
func TestJitteredAimStaysOnTarget(t *testing.T) {
	t.Parallel()

	center := mgl32.Vec3{10.5, 64.5, -3.5}
	moved := false

	for i := 0; i < 40; i++ {
		aim := JitteredAim(center)
		if math.Abs(float64(aim.X()-center.X())) > AimJitter {
			t.Fatalf("aim X drifted %v, want at most %v", aim.X()-center.X(), AimJitter)
		}
		if math.Abs(float64(aim.Y()-center.Y())) > AimJitter {
			t.Fatalf("aim Y drifted %v, want at most %v", aim.Y()-center.Y(), AimJitter)
		}
		if math.Abs(float64(aim.Z()-center.Z())) > AimJitter {
			t.Fatalf("aim Z drifted %v, want at most %v", aim.Z()-center.Z(), AimJitter)
		}
		if aim != center {
			moved = true
		}
	}
	if !moved {
		t.Fatal("aim never moved off the block centre, want visible jitter")
	}
}

// TestBeatsNeverOvershootTheBreak is the safety half. An early PredictDestroy on
// a server-authoritative host is silently rejected — the block stays while the
// bot walks off convinced it broke it — so the rhythm has to stay inside the
// break duration it was given.
func TestBeatsNeverOvershootTheBreak(t *testing.T) {
	t.Parallel()

	aim := mgl32.Vec3{4.5, 64.5, 8.5}
	for _, breakTime := range []time.Duration{
		150 * time.Millisecond,
		920 * time.Millisecond,
		3 * time.Second,
	} {
		beats := Beats(breakTime, aim)
		if len(beats) < 1 || beats[0].Wait < WindUpMin || beats[0].Wait > WindUpMax {
			t.Fatalf("break of %v: first beat %v is not a wind-up", breakTime, beats[0].Wait)
		}
		if beats[0].Aim != aim {
			t.Fatalf("break of %v: wind-up aim %v, want the block centre", breakTime, beats[0].Aim)
		}

		// Account for the wind-up conservatively, exactly as the break loops do.
		total := WindUpMin
		for i, beat := range beats[1:] {
			if beat.Wait < SwingMin {
				t.Fatalf("break of %v: swing %d pauses only %v, too fast for the arm cycle to read as a swing", breakTime, i+1, beat.Wait)
			}
			total += beat.Wait
		}
		if total > breakTime {
			t.Fatalf("break of %v: beats total %v, want no overshoot", breakTime, total)
		}
	}
}

// TestBeatsSwingOftenEnoughToFinish covers the opposite failure: a break that
// the server expects to take two seconds must not be finished after one swing.
func TestBeatsSwingOftenEnoughToFinish(t *testing.T) {
	t.Parallel()

	beats := Beats(2*time.Second, mgl32.Vec3{0.5, 64.5, 0.5})
	if len(beats) < 4 {
		t.Fatalf("a two-second break produced %d beats, want several swings", len(beats))
	}
}

// TestBeatsNeverEndOnARuntSwing is the reason Beats refuses to trim a last beat
// to the leftovers. A swing 86ms after the one before it restarts the viewer's
// arm-swing cycle mid-flight, so the final thing a viewer sees is the exact
// vibration the rhythm exists to prevent — and because each swing drives a dig
// sound, it is a stutter in the audio too. Every pause after the wind-up has to
// clear the swing floor, on every break length, not just the ones that happen
// to divide evenly.
func TestBeatsNeverEndOnARuntSwing(t *testing.T) {
	t.Parallel()

	aim := mgl32.Vec3{0.5, 64.5, 0.5}
	for ms := 200; ms <= 4000; ms += 7 {
		breakTime := time.Duration(ms) * time.Millisecond
		beats := Beats(breakTime, aim)
		for i, beat := range beats[1:] {
			if beat.Wait < SwingMin {
				t.Fatalf("break of %v: swing %d pauses %v, want at least the %v swing floor", breakTime, i+1, beat.Wait, SwingMin)
			}
		}
	}
}
