// Test-support accessors for the swim controller.
//
// The two things a black-box test in tests/bot/movement needs to arrange — "the
// head has been under for eleven seconds" and "the body is three blocks down" —
// are deliberately unexported and guarded by the controller's mutex, because
// they are its own bookkeeping and letting callers write them would put the
// lock discipline in everyone's hands. Aging the submersion clock through a
// replaced clock is the difference between a test that proves the breath
// reflex fires before drowning and a test that sleeps for eleven seconds to
// find out.

package movement

import (
	"math"
	"time"

	"bedrock-ai/internal/bot"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// ApplyIdleLookForTest runs one tick of the stationary gaze, so a test can see
// what the head actually does with a latched gaze hint.
//
// It is the real function, not a reimplementation: the thing worth testing about
// a gaze hint is whether it changes the subject without changing the rhythm, and
// that can only be checked against the code that produces both.
func (tc *TickContext) ApplyIdleLookForTest() {
	tc.applyIdleLook()
}

// AdvancePathForTest runs one step of the path cursor.
//
// It is exposed so a test can reach the end-of-path branch, which is where the
// trip's state is torn down from inside a held lock. That branch is the one a
// test cannot reach from the outside: nothing else empties CurrentPath.
func (tc *TickContext) AdvancePathForTest(maxHeightDiff float32) {
	tc.advancePath(maxHeightDiff)
}

// CheckWalkToArrivalForTest runs the tolerance-based arrival check, the other
// way a trip ends.
func (tc *TickContext) CheckWalkToArrivalForTest() {
	tc.checkWalkToArrival()
}

// RecordPlacedSupportForTest writes a confirmed scaffold placement into the
// world model, the way placeScaffoldBlock does after the server confirms.
//
// It is the write alone, separated from the packet path, because the packet
// path needs a live connection and this is the part that was missing: a
// placement the server honoured that the world model never heard of leaves the
// bot standing on air, and a bot standing on air is not grounded, and a bot that
// is not grounded never consumes a jump request. The climb then fails on the
// very next step with "the body never cleared the cell", forever.
func RecordPlacedSupportForTest(b *bot.Bot, cell protocol.BlockPos) {
	if b == nil || b.WorldModel == nil {
		return
	}
	b.WorldModel.SetSolid(cell.X(), cell.Y(), cell.Z(), true)
}

// IsGroundedInWorldModelForTest runs the same grounded test the physics phase
// runs, so a test can ask the question the tick actually asks.
func IsGroundedInWorldModelForTest(b *bot.Bot, feet mgl32.Vec3) bool {
	if b == nil || b.WorldModel == nil {
		return false
	}
	cy := int32(math.Floor(float64(feet.Y() - 0.01)))
	for _, off := range []float32{-0.3, 0, 0.3} {
		cx := int32(math.Floor(float64(feet.X() + off)))
		cz := int32(math.Floor(float64(feet.Z() + off)))
		if b.WorldModel.IsSolid(cx, cy, cz) {
			return true
		}
	}
	return false
}

// SetClock replaces the controller's time source. Production leaves it on
// time.Now; a test sets it to a hand-advanced value.
func (s *SwimController) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now == nil {
		return
	}
	s.now = now
}

// SubmergedSince reports when the head last went under, and the zero time when
// it is currently out of the water.
func (s *SwimController) SubmergedSince() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.under
}

// SubmersionAt samples the world against a position the test supplies, instead
// of the bot's own coordinates.
//
// The controller reads its position from the bot, which a test can move but
// only as a whole; this lets a test ask what the plan would be at a depth the
// bot has not reached yet, which is how the descent itself gets checked.
func (s *SwimController) SubmersionAt(x, y, z int32) (Submersion, bool) {
	world := s.Water()
	if world == nil {
		return Submersion{}, false
	}
	return SampleSubmersion(world, x, y, z), true
}

// SteerForTest runs the real steering step, which is where the drop gate lives.
//
// It matters that this is the actual function and not a copy. The gate was
// measured correctly by one test and never exercised by another, and the
// measurement is not the behaviour: a bot can measure a cliff perfectly and walk
// off it anyway. Only driving the real steering step answers the question that
// matters, which is whether the body stops.
func (tc *TickContext) SteerForTest() {
	tc.performActiveSteering()
}

// DropsOverTest reports whether the tick refused its step to a cliff.
func (tc *TickContext) DropsOverTest() bool { return tc.stopsAtLedge() }
