package animation_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/movement/animation"

	"github.com/go-gl/mathgl/mgl32"
)

// A tree is chopped in a stutter, and the cause was arithmetic rather than taste.
//
// The chopper ran a fresh break rhythm for every log. Each one began with a
// wind-up of 100-220ms, and the loop between logs only waited 20ms, so the gap
// between the last swing of one log and the first swing of the next came out at
// 120-240ms. The swing floor is 260ms: below it, the viewer's client restarts
// the arm cycle before the previous one has finished, and the arm reads as a
// vibration rather than a swing.
//
// On a six-log trunk that happened five times. The rhythm is now one chain across
// the whole tree, and these tests pin the property that fixes it.

// nextGap walks a chain for n steps and returns every wait it hands out.
func nextGap(c *animation.Chain, n int) []time.Duration {
	out := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		wait, _ := c.Next()
		out = append(out, wait)
	}
	return out
}

// TestTheWindUpIsOwedOnceForAWholeTree is the core of the fix. A chain serves the
// wind-up on its first step and never again, so the second log does not start
// with an arm raise in the middle of a swing.
func TestTheWindUpIsOwedOnceForAWholeTree(t *testing.T) {
	t.Parallel()

	centre := mgl32.Vec3{10.5, 64.5, 10.5}
	c := animation.NewChain(centre)

	wait, swing := c.Next()
	if swing {
		t.Error("the first step of a chain sent a swing; the wind-up raises the arm without striking")
	}
	if wait < animation.WindUpMin || wait > animation.WindUpMax {
		t.Errorf("wind-up = %v, want between %v and %v", wait, animation.WindUpMin, animation.WindUpMax)
	}
	if !c.Started() {
		t.Error("the chain does not know it has been started after serving the wind-up")
	}

	// Every step after the wind-up is a swing on a cadence pause, for as many
	// logs as the tree has.
	for i := 0; i < 20; i++ {
		_, swing := c.Next()
		if !swing {
			t.Fatalf("step %d after the wind-up did not send a swing", i+1)
		}
	}
}

// TestOneDeadBeatPerTrunkRatherThanOnePerLog is the regression proper.
//
// The tell is not that the bot swings too fast — it does not. It is that the old
// per-log rhythm inserted a wind-up in front of every log, and a wind-up is a
// beat that raises the arm and sends no strike. So between each log the arm went
// up, held, and came down again with nothing in between: a visible dead beat at
// every log boundary, and a six-log trunk had five of them.
//
// The number of silent beats is the property. One per trunk is a person starting
// to swing; one per log is a machine re-arming.
func TestOneDeadBeatPerTrunkRatherThanOnePerLog(t *testing.T) {
	t.Parallel()

	const logs = 6
	centre := mgl32.Vec3{10.5, 64.5, 10.5}

	// The old shape: one chain per log, which is what the chopper used to build.
	oldSilent := 0
	for log := 0; log < logs; log++ {
		c := animation.NewChain(centre)
		// Each log got a wind-up plus however many swings its break time fitted.
		for {
			_, swing := c.Next()
			if !swing {
				oldSilent++
			}
			if swing && c.Swings() >= 3 {
				break
			}
		}
	}

	// The new shape: one chain for the whole trunk.
	c := animation.NewChain(centre)
	newSilent := 0
	for step := 0; step < 18; step++ {
		_, swing := c.Next()
		if !swing {
			newSilent++
		}
	}

	if oldSilent != logs {
		t.Fatalf("the old per-log shape produced %d silent beats over %d logs, want %d; this test no longer models the defect", oldSilent, logs, logs)
	}
	if newSilent != 1 {
		t.Errorf("a whole trunk produced %d silent beats, want exactly 1 (the wind-up); the arm still stalls mid-tree", newSilent)
	}
}

// TestTheRhythmSpansTheTree rather than restarting inside every log.
//
// Every step after the wind-up is an ordinary cadence pause, so a trunk reads as
// one continuous swing cycle from the first log to the last. A rhythm that
// re-derived itself per log would re-serve the wind-up (a dead beat, see
// TestTheWindUpIsOwedOnceForAWholeTree) and reset its count.
func TestTheRhythmSpansTheTree(t *testing.T) {
	t.Parallel()

	c := animation.NewChain(mgl32.Vec3{1.5, 64.5, 1.5})
	_, _ = c.Next() // wind-up

	var swings int
	for i := 0; i < 18; i++ {
		wait, swing := c.Next()
		if !swing {
			t.Fatalf("step %d did not swing", i)
		}
		if wait < animation.SwingMin || wait > animation.SwingMax {
			t.Fatalf("step %d pauses %v, outside the swing range [%v, %v]", i, wait, animation.SwingMin, animation.SwingMax)
		}
		swings++
	}

	if swings != 18 {
		t.Fatalf("counted %d swings, want 18", swings)
	}
	if c.Swings() != 18 {
		t.Errorf("the chain counted %d swings, want 18", c.Swings())
	}
}

// TestReaimKeepsTheRhythm but moves the head.
//
// Climbing a trunk means the block being hit is a different one every log, so the
// aim has to follow. It has to be the only thing that changes: a re-aim that also
// reset the swing count or re-served the wind-up would reintroduce exactly the
// per-log restart this type exists to remove.
func TestReaimKeepsTheRhythmButMovesTheHead(t *testing.T) {
	t.Parallel()

	c := animation.NewChain(mgl32.Vec3{1.5, 64.5, 1.5})
	_, _ = c.Next() // wind-up
	_, _ = c.Next() // one swing
	before := c.Aim()

	c.Reaim(mgl32.Vec3{1.5, 67.5, 1.5})

	if c.Swings() != 1 {
		t.Errorf("re-aiming changed the swing count to %d, want 1; the rhythm restarted mid-tree", c.Swings())
	}
	if !c.Started() {
		t.Error("re-aiming forgot that the wind-up was already served")
	}
	if c.Aim() == before {
		t.Error("re-aiming did not move the head; the bot would keep swinging at the log it already felled")
	}
	// And the next step must still be a swing on a cadence pause, not a wind-up.
	_, swing := c.Next()
	if !swing {
		t.Error("the step after a re-aim did not swing; the wind-up was served twice")
	}
}

// TestTheSwingSpacingStillClearsTheAnimationFloor is the guard on the guard. The
// fix removed a dead beat, not a pause — every swing pause must still clear
// SwingMin, because below it the viewer's client restarts the arm cycle
// mid-flight and the whole rhythm defeats itself.
func TestTheSwingSpacingStillClearsTheAnimationFloor(t *testing.T) {
	t.Parallel()

	c := animation.NewChain(mgl32.Vec3{10.5, 64.5, 10.5})
	waits := nextGap(c, 40)
	for i, w := range waits {
		if i == 0 {
			continue // wind-up: an arm raise, not a swing gap
		}
		if w < animation.SwingMin {
			t.Errorf("swing step %d waits %v, under the %v floor", i, w, animation.SwingMin)
		}
		if w > animation.SwingMax {
			t.Errorf("swing step %d waits %v, over the %v ceiling; the arm parks mid-break", i, w, animation.SwingMax)
		}
	}
}

// TestJitterStaysOnTheBlockAfterReaim. The jitter is what keeps the head from
// being welded to a pixel, and a re-aim onto a higher log must not be able to
// wander far enough to look like the bot lost the block.
func TestJitterStaysOnTheBlockAfterReaim(t *testing.T) {
	t.Parallel()

	for y := float32(64); y < 80; y++ {
		centre := mgl32.Vec3{12.5, y + 0.5, 12.5}
		c := animation.NewChain(centre)
		c.Reaim(centre)

		aim := c.Aim()
		for _, d := range [][2]float32{{aim.X() - centre.X(), 0}, {aim.Y() - centre.Y(), 0}, {aim.Z() - centre.Z(), 0}} {
			if d[0] < -animation.AimJitter-1e-6 || d[0] > animation.AimJitter+1e-6 {
				t.Fatalf("aim %v drifted %v from centre %v, past the %v jitter bound",
					aim, d[0], centre, animation.AimJitter)
			}
		}
	}
}
