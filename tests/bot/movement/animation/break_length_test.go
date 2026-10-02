package animation_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/movement/animation"

	"github.com/go-gl/mathgl/mgl32"
)

// The rhythm was already correct about how often to swing. What was wrong was
// the two things around it: the break was being cut short, and the aim was
// being re-rolled on every single swing.

// TestBeatsConsumeTheWholeBreak is the early-finish rule.
//
// Beats stopped adding beats as soon as the next one would overshoot, and threw
// away whatever time was left. The shortfall is not a rounding error — it is up
// to a full recovery pause, nearly eight tenths of a second. So the bot stopped
// swinging well before the block gave, and the last thing a viewer saw was a
// raised arm and then a block that was simply gone: a strike that never lands.
//
// On a server-authoritative host that is worse than cosmetic, because an early
// PredictDestroy is rejected outright and the block stays while the bot moves on
// convinced it broke it.
func TestBeatsConsumeTheWholeBreak(t *testing.T) {
	t.Parallel()

	aim := mgl32.Vec3{4.5, 64.5, 8.5}
	// No threshold is needed here, and inventing one would be a guess: whether a
	// break just above the swing floor carries a swing at all depends on the roll
	// of the cadence, and both answers are correct. What is never correct is
	// laying down a swing and then stopping short of the break.
	checked := 0
	for ms := 200; ms <= 4000; ms += 3 {
		breakTime := time.Duration(ms) * time.Millisecond
		beats := animation.Beats(breakTime, aim)
		if len(beats) < 2 {
			continue
		}
		checked++

		// The break loops account for the wind-up at its floor, so this is the
		// sum they will actually wait out before finishing the block.
		waited := animation.WindUpMin
		for _, beat := range beats[1:] {
			waited += beat.Wait
		}
		if waited != breakTime {
			t.Fatalf("break of %v: the swings wait out %v, leaving %v %s the break",
				breakTime, waited, absDuration(breakTime-waited), tooEarlyOrLate(breakTime, waited))
		}
	}
	if checked < 100 {
		t.Fatalf("only %d break lengths carried a swing; the sweep is not exercising the rule", checked)
	}
}

// TestAShortBreakGetsNoRuntSwing keeps the other half honest. There is no honest
// way to spend the time left on a break too short to hold a swing, and the
// tempting fix — stretching one to fit — is the vibration this rhythm exists to
// avoid. An instant block is better served by no swing at all.
func TestAShortBreakGetsNoRuntSwing(t *testing.T) {
	t.Parallel()

	aim := mgl32.Vec3{4.5, 64.5, 8.5}
	for ms := 1; ms <= 4000; ms++ {
		breakTime := time.Duration(ms) * time.Millisecond
		beats := animation.Beats(breakTime, aim)
		for i, beat := range beats[1:] {
			if beat.Wait < animation.SwingMin {
				t.Fatalf("break of %vms: swing %d pauses %v, under the %v swing floor",
					ms, i+1, beat.Wait, animation.SwingMin)
			}
		}
	}
}

// TestTheAimSettlesForTheWholeBreak is the head-tremor rule.
//
// The jitter existed so the head would not be welded to a single pixel, which is
// a fair thing to want. Re-rolling it on every swing is not that: the look ease
// converges at 0.22 a tick, so it needs the better part of a second to settle,
// and a new random target arrived every three hundred milliseconds. The head was
// therefore permanently chasing a target it never reached, and did it in step
// with the arm — which reads as the swing itself looking wrong, because the
// body it is attached to is twitching through every strike.
//
// This repo has already ruled that shape a bug once, in the movement loop's
// pitch easing. The same rule applies here: one aim per break, so the head
// settles and stays settled while the arm works.
func TestTheAimSettlesForTheWholeBreak(t *testing.T) {
	t.Parallel()

	aim := mgl32.Vec3{4.5, 64.5, 8.5}
	beats := animation.Beats(3*time.Second, aim)
	if len(beats) < 3 {
		t.Fatalf("a three-second break produced %d beats, too few to say anything", len(beats))
	}

	first := beats[1].Aim
	for i, beat := range beats[2:] {
		if beat.Aim != first {
			t.Fatalf("swing %d aims at %v, swing 1 aimed at %v; the head will chase a new target mid-break",
				i+2, beat.Aim, first)
		}
	}

	// It still has to be off the exact centre, or the head is welded to a pixel
	// and the break reads as automated in the other direction.
	if first == aim {
		t.Error("every swing aims at the exact block centre; the jitter is the point of it")
	}
}

// TestTheWindUpAimsStraightAtTheBlock keeps the one re-aim that is allowed. The
// first beat is the tool raise, before anything has been swung, and it should
// land on the block rather than on a drifting copy of it.
func TestTheWindUpAimsStraightAtTheBlock(t *testing.T) {
	t.Parallel()

	aim := mgl32.Vec3{4.5, 64.5, 8.5}
	beats := animation.Beats(2*time.Second, aim)
	if beats[0].Aim != aim {
		t.Errorf("wind-up aims at %v, want the block centre %v", beats[0].Aim, aim)
	}
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func tooEarlyOrLate(breakTime, total time.Duration) string {
	if total < breakTime {
		return "before"
	}
	return "after"
}
