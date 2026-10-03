// Ladder climbing and the emote sweep are both integer bugs that no test could
// see, because both hid inside a field nothing outside the package reads.
//
// The ladder one is the worse of the two. applyLadderVerticalVelocity computed
// a correct climb velocity and then, on every branch except "already at the
// target", returned without writing the height that velocity applies to. The
// field it should have written is NextY, the predicted height the movement
// prediction reads back. TickContext is rebuilt every tick and NextY was not in
// its literal, so NextY was zero — the bot's predicted position came out at
// Y=0 on every rung, the server rejected the move, and the correction
// discarded the path it was climbing. Ladders did not work, and they failed in
// a way that looked like the bot was being shoved around rather than like a
// missing assignment.

package movement_test

import (
	"math"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"

	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// ladderBot builds a bot standing on a ladder with a path that climbs to
// targetY, one rung at a time.
func ladderBot(startY float32, targetY int32) *bot.Bot {
	b := &bot.Bot{}
	b.Pos = mgl32.Vec3{0.5, startY, 0.5}
	b.CurrentPath = []pathfinder.Node{{Y: targetY}}
	b.PathIndex = 0
	return b
}

// TestALadderClimbPredictsAHeightNearTheBody is the regression.
//
// Every assertion is that the predicted height is close to where the body
// actually is. The broken code predicted Y=0 regardless, so a bot climbing from
// Y=64 predicted 0 — sixty-four blocks of error, sent to the server as this
// tick's position.
func TestALadderClimbPredictsAHeightNearTheBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		startY   float32
		targetY  int32
		wantUp   bool
		wantDown bool
	}{
		{"climbing up", 64, 65, true, false},
		{"climbing up several rungs", 60, 66, true, false},
		{"descending", 70, 68, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := &movement.TickContext{
				B:          ladderBot(tc.startY, tc.targetY),
				CurrPos:    mgl32.Vec3{0.5, tc.startY, 0.5},
				IsOnLadder: true,
				HasPath:    true,
			}

			ctx.ApplyVerticalVelocityForTest()

			nextY := ctx.NextYForTest()
			if math.Abs(float64(nextY-tc.startY)) > 1.5 {
				t.Errorf("predicted height after one ladder tick = %v, but the body "+
					"is at %v: the climb predicted %v blocks of movement",
					nextY, tc.startY, nextY-tc.startY)
			}

			vel := ctx.VelYForTest()
			switch {
			case tc.wantUp && vel <= 0:
				t.Errorf("velocity = %v, want positive: the body is below its target rung", vel)
			case tc.wantDown && vel >= 0:
				t.Errorf("velocity = %v, want negative: the body is above its target rung", vel)
			case !tc.wantUp && !tc.wantDown && vel != 0:
				t.Errorf("velocity = %v, want 0: the body is level with its target rung", vel)
			}
		})
	}
}

// TestALadderWithNoPathStillPredictsTheBodyHeight covers the early return. A
// body on a ladder with nothing to climb is the state after the last rung, and
// it is the branch that returned before any assignment at all.
func TestALadderWithNoPathStillPredictsTheBodyHeight(t *testing.T) {
	t.Parallel()

	const startY float32 = 72
	b := &bot.Bot{}
	b.Pos = mgl32.Vec3{0.5, startY, 0.5}

	ctx := &movement.TickContext{
		B:          b,
		CurrPos:    mgl32.Vec3{0.5, startY, 0.5},
		IsOnLadder: true,
		HasPath:    false,
	}

	ctx.ApplyVerticalVelocityForTest()

	if got := ctx.NextYForTest(); math.Abs(float64(got-startY)) > 0.01 {
		t.Errorf("predicted height = %v, want %v: a body standing on a ladder with "+
			"no path left must hold its height, not fall to the prediction default", got, startY)
	}
}

// TestALadderAtItsTargetRungSnapsToIt pins the third branch, so the fix for the
// other two cannot have broken it.
func TestALadderAtItsTargetRungSnapsToIt(t *testing.T) {
	t.Parallel()

	const (
		startY  float32 = 65
		targetY int32   = 65
	)
	ctx := &movement.TickContext{
		B:          ladderBot(startY, targetY),
		CurrPos:    mgl32.Vec3{0.5, startY, 0.5},
		IsOnLadder: true,
		HasPath:    true,
	}

	ctx.ApplyVerticalVelocityForTest()

	if got := ctx.NextYForTest(); got != float32(targetY) {
		t.Errorf("predicted height = %v, want %v: a body level with its rung settles on it", got, float32(targetY))
	}
	if v := ctx.VelYForTest(); v != 0 {
		t.Errorf("velocity = %v, want 0 at the target rung", v)
	}
}

// TestTheLookAroundEmoteSweepsBothWays is the second integer bug.
//
// The emote adds Tick%50-25 to the yaw. Tick is a uint64, so for every tick
// whose remainder was below 25 the subtraction underwound to about 1.8e19, and
// InterpolateAngle clamped that to its maximum step. The sweep was designed to
// travel from -24 through +25 and back again; it turned right, hard, forever.
func TestTheLookAroundEmoteSweepsBothWays(t *testing.T) {
	t.Parallel()

	const emoteTicks = 5 // divisible by 5, so the emote fires

	var minYaw, maxYaw float32 = math.MaxFloat32, -math.MaxFloat32
	var minPitch, maxPitch float32 = math.MaxFloat32, -math.MaxFloat32
	initialised := false

	for tick := uint64(0); tick < 120; tick++ {
		b := &bot.Bot{}
		b.EmoteTicks = emoteTicks

		ctx := &movement.TickContext{
			B:    b,
			Tick: tick,
			Yaw:  90,
		}
		ctx.HandleEmoteLookAroundForTest(false)

		if !initialised {
			minYaw, maxYaw = ctx.Yaw, ctx.Yaw
			minPitch, maxPitch = ctx.Pitch, ctx.Pitch
			initialised = true
			continue
		}
		minYaw = min32(minYaw, ctx.Yaw)
		maxYaw = max32(maxYaw, ctx.Yaw)
		minPitch = min32(minPitch, ctx.Pitch)
		maxPitch = max32(maxPitch, ctx.Pitch)

		if math.IsNaN(float64(ctx.Yaw)) || math.IsInf(float64(ctx.Yaw), 0) {
			t.Fatalf("tick %d produced a non-finite yaw: %v", tick, ctx.Yaw)
		}
	}

	// The offset is built from a value in [-24, 25]. Anything clamped to the
	// interpolation limit on the negative side means the subtraction wrapped.
	if minYaw >= 90 {
		t.Errorf("yaw range [%v, %v] never went below the starting 90: the "+
			"negative half of the sweep underflowed", minYaw, maxYaw)
	}
	if maxYaw <= 90 {
		t.Errorf("yaw range [%v, %v] never went above the starting 90: the "+
			"positive half of the sweep is missing", minYaw, maxYaw)
	}
	if minPitch >= 0 {
		t.Errorf("pitch range [%v, %v] never went below 0: the downward half of "+
			"the sweep underflowed", minPitch, maxPitch)
	}
	if maxPitch <= 0 {
		t.Errorf("pitch range [%v, %v] never went above 0: the upward half is missing",
			minPitch, maxPitch)
	}
}

// min32 and max32 exist because math.Min and math.Max are float64, and routing
// every comparison through them would silently widen the values being tracked.
func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// TestInterpolateAngleTerminatesForAnyInput is the structural guard behind the
// emote bug.
//
// The wrap-around used to be folded by looping — `for diff < -180 { diff += 360
// }` — which is only correct while diff is small enough for the addition to
// change the value at all. A float32 near 1.8e19 has an ULP of about a billion,
// so the addition is a no-op and the loop spins forever. The value that reached
// it came from an unsigned subtraction that underwound instead of going
// negative, and the bot's movement goroutine hung on the first emote tick.
//
// Loops that decide how long a function takes are the defect. The guard is that
// no input may hang this one, whatever produced it.
func TestInterpolateAngleTerminatesForAnyInput(t *testing.T) {
	t.Parallel()

	// Values chosen to break a fold-by-iteration implementation: magnitudes
	// where float32 has no precision for a 360-degree step, plus the sign
	// combinations that decide which side of the wrap they land on.
	inputs := []float32{
		1.8e19, -1.8e19, 3.4e38, -3.4e38,
		1e10, -1e10, 1e7, -1e7,
		float32(math.Inf(1)), float32(math.Inf(-1)),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, target := range inputs {
			for _, current := range inputs {
				// Any result is acceptable; finishing is the assertion.
				_ = movement.InterpolateAngle(current, target, 25)
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("InterpolateAngle did not return for an extreme input: the " +
			"wrap-around fold loops forever once the step is below the value's " +
			"representation precision")
	}
}

// TestInterpolateAngleStillWrapsCorrectly keeps the rewrite honest. Closed-form
// folding has to agree with the loop it replaced on ordinary inputs, or the
// guard above has been bought with a behaviour change.
func TestInterpolateAngleStillWrapsCorrectly(t *testing.T) {
	t.Parallel()

	cases := []struct {
		current, target, maxStep, want float32
	}{
		{0, 90, 25, 25},    // clamped up
		{0, -90, 25, 335},  // clamped down, wrapped to [0,360)
		{350, 10, 25, 10},  // wraps forward across zero
		{10, 350, 25, 350}, // wraps backward across zero and lands on target
		{90, 90, 25, 90},   // already there
		{0, 180, 25, 25},   // half turn is the upper bound, not clamped
		{0, -180, 25, 335}, // half turn the other way
		{45, 46, 25, 46},   // small step, no clamping
	}

	for _, tc := range cases {
		if got := movement.InterpolateAngle(tc.current, tc.target, tc.maxStep); got != tc.want {
			t.Errorf("InterpolateAngle(%v, %v, %v) = %v, want %v",
				tc.current, tc.target, tc.maxStep, got, tc.want)
		}
	}
}
