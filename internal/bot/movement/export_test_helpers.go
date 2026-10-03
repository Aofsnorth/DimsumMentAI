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
	"time"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

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
// runs, so a test can ask the question the tick actually asks. It does not
// reimplement the test: the probe runs through a throwaway TickContext so a
// divergence between this and UpdateGroundedState is a compile error or an
// obviously wrong answer, never a silent second definition of "grounded".
func IsGroundedInWorldModelForTest(b *bot.Bot, feet mgl32.Vec3) bool {
	if b == nil || b.WorldModel == nil {
		return false
	}
	tc := &TickContext{B: b, CurrPos: feet}
	tc.UpdateGroundedState()
	return tc.IsGrounded
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

// ApplyVerticalVelocityForTest runs the real vertical integration, the one the
// tick runs, and reports the predicted height afterwards.
//
// It exists because the ladder bug was not in the ladder maths. The maths was
// right; the predicted height it was supposed to write back was never written,
// and nothing outside this file could see that field. A test that asserts on
// the ladder's own constants would have passed against the broken code.
func (tc *TickContext) ApplyVerticalVelocityForTest() {
	tc.ApplyVerticalVelocity()
}

// NextYForTest reports the height the tick will predict the body at.
func (tc *TickContext) NextYForTest() float32 { return tc.NextY }

// VelYForTest reports the vertical velocity the tick settled on.
func (tc *TickContext) VelYForTest() float32 { return tc.VelY }

// HandleEmoteLookAroundForTest runs the real look-around emote, which is where
// an unsigned underflow hides: the sweep is built from Tick%50-25, and a
// uint64 remainder below 25 wraps instead of going negative.
func (tc *TickContext) HandleEmoteLookAroundForTest(isPathfinding bool) {
	tc.handleEmoteLookAround(isPathfinding)
}

// BuildInputDataForTest runs the real flag assembly, which is the only place in
// the package that produces the input flags a PlayerAuthInput carries. A gait
// edge is not a behaviour a test can observe any other way: the level flags it
// sits beside look identical with and without it, so the seam is the whole
// point of the assertion.
// SetSyncedGroundedForTest writes the grounded flag the jump-request path
// reads. It is deliberately the bot's flag and not TickContext's: TickContext
// is rebuilt every tick and its own grounded field is only filled in later, by
// the physics phase, which is exactly why the request path reads this one.
func (tc *TickContext) SetSyncedGroundedForTest(grounded bool) {
	if tc.B == nil {
		return
	}
	tc.B.Mu.Lock()
	tc.B.IsGrounded = grounded
	tc.B.Mu.Unlock()
}

// TakeRequestedJumpForTest runs the one and only place a latched jump request
// can become a jump.
// ResetEmoteJumpForTest clears the one-hop latch so a test can stage a fresh
// emote window on a grounded body.
func (tc *TickContext) ResetEmoteJumpForTest() {
	if tc == nil || tc.B == nil {
		return
	}
	tc.B.Mu.Lock()
	tc.B.EmoteJumpSpent = false
	tc.B.Mu.Unlock()
}

// EmoteJumpSpentForTest reports whether the current emote window already
// bought its one physical hop.
func (tc *TickContext) EmoteJumpSpentForTest() bool {
	if tc == nil || tc.B == nil {
		return false
	}
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	return tc.B.EmoteJumpSpent
}

func (tc *TickContext) TakeRequestedJumpForTest() {
	tc.takeRequestedJump()
}

func (tc *TickContext) BuildInputDataForTest(emoteJump, emoteSneak bool) protocol.InputFlags {
	return tc.buildInputData(emoteJump, emoteSneak)
}

// BuildPlayerAuthInputPacketForTest builds the real movement packet, which is
// the only place a pitch becomes bytes on the wire. Clamping the gaze target is
// not the same guarantee as clamping the packet, and only the packet is what a
// server reads.
func (tc *TickContext) BuildPlayerAuthInputPacketForTest() *packet.PlayerAuthInput {
	return tc.BuildPlayerAuthInputPacket(protocol.NewInputFlags(packet.InputFlagCount), nil, nil, nil)
}
