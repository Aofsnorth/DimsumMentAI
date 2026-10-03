package animation_test

import (
	"math"
	"testing"
	"time"

	"bedrock-ai/internal/bot/movement/animation"

	"github.com/go-gl/mathgl/mgl32"
)

// TestCadenceVariesLikeAHand covers the "natural" side of the rhythm: a fixed
// tick between swings is the clearest tell of a bot, and because each swing is
// also what makes a client emit its dig sound, an unvarying interval is heard
// as a machine-gun rattle. The pause must vary — but it must stay inside the
// width of one swing animation, because mining is continuous work and a pause
// wider than the animation parks the arm at rest in the middle of a break.
func TestCadenceVariesLikeAHand(t *testing.T) {
	t.Parallel()

	seen := map[time.Duration]int{}
	for swing := 0; swing < 24; swing++ {
		wait := animation.Cadence()
		if wait < animation.SwingMin || wait > animation.SwingMax {
			t.Fatalf("swing %d: cadence %v outside [%v, %v]", swing, wait, animation.SwingMin, animation.SwingMax)
		}
		seen[wait]++
	}
	if len(seen) < 4 {
		t.Fatalf("cadence produced only %d distinct values over 24 swings, want a varied rhythm", len(seen))
	}
}

// TestCadenceStaysAboveAFloor pins the lower bound of the swing range.
//
// This used to assert the pause could not fall under the ~250ms arm-swing
// animation, on the theory that a swing arriving mid-arc restarts it and the arm
// vibrates instead of chopping. The theory was reasoned, not watched, and it did
// not survive contact with a live host: swinging faster than the arc renders
// smooth and simply reads as faster. What the floor is for now is narrower —
// the viewer's client still needs the swings spaced far enough apart to be
// distinct strikes rather than one continuous blur — so the test holds a modest
// floor instead of the arc length.
func TestCadenceStaysAboveAFloor(t *testing.T) {
	t.Parallel()

	if animation.SwingMin < 80*time.Millisecond {
		t.Fatalf("swing floor %v is tight enough to read as a blur rather than distinct chops", animation.SwingMin)
	}
	if animation.SwingMax <= animation.SwingMin {
		t.Fatalf("swing range [%v, %v] is empty", animation.SwingMin, animation.SwingMax)
	}
	if animation.SwingMid < animation.SwingMin || animation.SwingMid > animation.SwingMax {
		t.Fatalf("layout rate %v sits outside the swing range [%v, %v]", animation.SwingMid, animation.SwingMin, animation.SwingMax)
	}
}

// TestCadenceNeverParksTheArmMidBreak is the upper pacing rule.
//
// An item's swing animation is 0.3s by default (minecraft:swing_duration), and a
// real server re-triggers it every 5 ticks while mining — Dragonfly's
// ContinueBreaking does exactly that, with no pauses. A cadence at or above the
// arc length does not read as a player pausing to think; it reads as a bot that
// stopped mining while the block is still cracking.
func TestCadenceNeverParksTheArmMidBreak(t *testing.T) {
	t.Parallel()

	const swingAnimation = 300 * time.Millisecond

	if animation.SwingMax >= swingAnimation {
		t.Fatalf("swing ceiling %v reaches the %v swing animation; the arm comes to rest "+
			"in the middle of a break, which is the shipped bug", animation.SwingMax, swingAnimation)
	}
	if animation.WindUpMax >= animation.SwingMin {
		t.Fatalf("wind-up ceiling %v reaches the swing floor %v; a break can start on a full pause", animation.WindUpMax, animation.SwingMin)
	}
}

// TestWindUpStaysInBounds checks the tool raise before the first swing: long
// enough to read as a wind-up, short enough not to eat the break time and short
// enough to stay a separate beat from the swings.
func TestWindUpStaysInBounds(t *testing.T) {
	t.Parallel()

	for i := 0; i < 20; i++ {
		w := animation.WindUp()
		if w < animation.WindUpMin || w > animation.WindUpMax {
			t.Fatalf("wind-up %v outside [%v, %v]", w, animation.WindUpMin, animation.WindUpMax)
		}
	}
	if animation.WindUpMin >= animation.SwingMin {
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
		aim := animation.JitteredAim(center)
		if math.Abs(float64(aim.X()-center.X())) > animation.AimJitter {
			t.Fatalf("aim X drifted %v, want at most %v", aim.X()-center.X(), animation.AimJitter)
		}
		if math.Abs(float64(aim.Y()-center.Y())) > animation.AimJitter {
			t.Fatalf("aim Y drifted %v, want at most %v", aim.Y()-center.Y(), animation.AimJitter)
		}
		if math.Abs(float64(aim.Z()-center.Z())) > animation.AimJitter {
			t.Fatalf("aim Z drifted %v, want at most %v", aim.Z()-center.Z(), animation.AimJitter)
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
		beats := animation.Beats(breakTime, aim)
		if len(beats) < 1 || beats[0].Wait < animation.WindUpMin || beats[0].Wait > animation.WindUpMax {
			t.Fatalf("break of %v: first beat %v is not a wind-up", breakTime, beats[0].Wait)
		}
		if beats[0].Aim != aim {
			t.Fatalf("break of %v: wind-up aim %v, want the block centre", breakTime, beats[0].Aim)
		}

		// Account for the wind-up conservatively, exactly as the break loops do.
		total := animation.WindUpMin
		for i, beat := range beats[1:] {
			if beat.Wait < animation.SwingMin {
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

	beats := animation.Beats(2*time.Second, mgl32.Vec3{0.5, 64.5, 0.5})
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
		beats := animation.Beats(breakTime, aim)
		for i, beat := range beats[1:] {
			if beat.Wait < animation.SwingMin {
				t.Fatalf("break of %v: swing %d pauses %v, want at least the %v swing floor", breakTime, i+1, beat.Wait, animation.SwingMin)
			}
		}
	}
}
