package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// A real Bedrock client reports every gait change twice: once as the level flag
// that says what is true now, and once as an edge that says something just
// changed. The bot sent the level flags correctly and the edges not at all, so
// sprinting and sneaking worked but the transition never appeared on the wire.
//
// These tests pin the edge half. They drive the real flag assembly through
// TickContext because the level flags and the edges leave together, and a test
// of GaitState alone would happily pass while buildInputData forgot to call it.

func flagsWithSprint(sprinting bool) protocol.InputFlags {
	f := protocol.NewInputFlags(packet.InputFlagCount)
	if sprinting {
		f.Set(packet.InputFlagSprinting)
	}
	return f
}

func TestGaitEdgesReportTheTickASprintBegins(t *testing.T) {
	t.Parallel()

	g := movement.NewGaitState()
	f := flagsWithSprint(true)
	g.ApplyGaitEdges(&f)

	if !f.Load(packet.InputFlagStartSprinting) {
		t.Fatal("first sprinting tick must carry StartSprinting; a real client " +
			"announces the transition, not just the resulting state")
	}
	if !f.Load(packet.InputFlagSprinting) {
		t.Fatal("the level flag must survive alongside the edge")
	}
}

func TestGaitEdgesStayQuietWhileTheGaitHolds(t *testing.T) {
	t.Parallel()

	// A sprint lasting 20 ticks is 20 level flags and exactly one edge.
	g := movement.NewGaitState()
	for tick := 0; tick < 20; tick++ {
		f := flagsWithSprint(true)
		g.ApplyGaitEdges(&f)

		if tick == 0 && !f.Load(packet.InputFlagStartSprinting) {
			t.Fatalf("tick 0 must announce the start of the sprint")
		}
		if tick > 0 && f.Load(packet.InputFlagStartSprinting) {
			t.Fatalf("tick %d re-announced a sprint that started at tick 0; "+
				"an edge is a transition, so re-sending it every tick is a different client", tick)
		}
		if f.Load(packet.InputFlagStopSprinting) {
			t.Fatalf("tick %d announced stopping while still sprinting", tick)
		}
	}
}

func TestGaitEdgesReportTheTickASprintEnds(t *testing.T) {
	t.Parallel()

	g := movement.NewGaitState()
	warm := flagsWithSprint(true)
	g.ApplyGaitEdges(&warm)

	f := flagsWithSprint(false)
	g.ApplyGaitEdges(&f)

	if !f.Load(packet.InputFlagStopSprinting) {
		t.Fatal("the first non-sprinting tick after a sprint must carry StopSprinting")
	}
	if f.Load(packet.InputFlagStartSprinting) {
		t.Fatal("a stop cannot also be a start")
	}
}

func TestGaitEdgesNeverClaimATransitionOutOfAnUnheldState(t *testing.T) {
	t.Parallel()

	// A fresh connection has pressed nothing. The first tick it ever sends must
	// not announce stopping a sprint it never started.
	g := movement.NewGaitState()
	f := flagsWithSprint(false)
	g.ApplyGaitEdges(&f)

	for _, id := range []int{
		packet.InputFlagStopSprinting,
		packet.InputFlagStartSprinting,
		packet.InputFlagStopSneaking,
		packet.InputFlagStartSneaking,
		packet.InputFlagStartJumping,
	} {
		if f.Load(id) {
			t.Fatalf("first tick of a connection set an edge flag it had no transition for")
		}
	}
}

func TestGaitEdgesFollowTheLevelFlagsActuallySent(t *testing.T) {
	t.Parallel()

	// Two subsystems write Sprinting independently -- the land gait and the swim
	// intent. If the edge were computed from one of them it could contradict
	// the other, announcing a start on a packet that does not carry Sprinting.
	// This drives it the way that failure would look.
	g := movement.NewGaitState()

	sprintLevelSet := flagsWithSprint(true)
	sprintLevelSet.Set(packet.InputFlagStartSwimming)
	g.ApplyGaitEdges(&sprintLevelSet)
	if !sprintLevelSet.Load(packet.InputFlagSprinting) || !sprintLevelSet.Load(packet.InputFlagStartSprinting) {
		t.Fatal("a packet carrying Sprinting must carry StartSprinting with it")
	}

	// The swim intent now turns sprinting off for a tick while the land gait
	// still believes it is running. The edge must follow the packet, not the
	// land gait's private opinion.
	noSprint := protocol.NewInputFlags(packet.InputFlagCount)
	g.ApplyGaitEdges(&noSprint)
	if !noSprint.Load(packet.InputFlagStopSprinting) {
		t.Fatal("edge must track the level flag that is actually on the packet")
	}
}

func TestGaitEdgesAreSafeOnAnAbsentOrUnsetFlagSet(t *testing.T) {
	t.Parallel()

	// The zero InputFlags has no size and Set panics on any index. A test or a
	// future caller can hand one over, and taking the movement loop down over
	// it would be a poor trade.
	g := movement.NewGaitState()

	var absent protocol.InputFlags
	g.ApplyGaitEdges(&absent)
	g.ApplyGaitEdges(nil)

	// A nil state is the "no transition history" case and must also be a no-op
	// rather than a panic.
	var nilState *movement.GaitState
	f := flagsWithSprint(true)
	nilState.ApplyGaitEdges(&f)
	if !f.Load(packet.InputFlagSprinting) {
		t.Fatal("a nil gait state must leave the level flags alone")
	}
}

func TestGaitStateReportsTheHeldGait(t *testing.T) {
	t.Parallel()

	g := movement.NewGaitState()
	if g.Sprinting() {
		t.Fatal("a new connection is not sprinting")
	}
	f := flagsWithSprint(true)
	g.ApplyGaitEdges(&f)
	if !g.Sprinting() {
		t.Fatal("the recorded state must follow the flags that were sent")
	}
	f = flagsWithSprint(false)
	g.ApplyGaitEdges(&f)
	if g.Sprinting() {
		t.Fatal("the recorded state must follow the sprint stopping")
	}
}

// TestGaitEdgesReachThePacket exercises the seam the tests above cannot: that
// buildInputData actually calls the gait state. Every other test in this file
// drives GaitState directly, so deleting the ApplyGaitEdges call from
// buildInputData would leave them all green while the bot went back to sending
// no transitions at all. Sneak is used rather than sprint because the emote
// path reaches the level flag without needing a live bot behind SprintHint.
func TestGaitEdgesReachThePacket(t *testing.T) {
	t.Parallel()

	gait := movement.NewGaitState()
	tc := &movement.TickContext{Gait: gait}

	// Tick one: the emote asks for sneak, so the level flag and its edge both go out.
	first := tc.BuildInputDataForTest(false, true)
	if !first.Load(packet.InputFlagSneaking) {
		t.Fatal("precondition: the emote sneak should set the level flag")
	}
	if !first.Load(packet.InputFlagStartSneaking) {
		t.Fatal("buildInputData did not emit StartSneaking; the gait state is not wired in")
	}

	// Tick two: sneak released. The edge must follow on the next tick and then go
	// quiet, which is the behaviour a per-tick context would get wrong.
	second := tc.BuildInputDataForTest(false, false)
	if !second.Load(packet.InputFlagStopSneaking) {
		t.Fatal("releasing sneak must emit StopSneaking")
	}
	third := tc.BuildInputDataForTest(false, false)
	if third.Load(packet.InputFlagStopSneaking) || third.Load(packet.InputFlagStartSneaking) {
		t.Fatal("a held release must not repeat its edge")
	}
}

// TestTickDeclaresCameraRelativeMovement previously asserted the opposite of
// what this file now says: that every packet must set bit 63. That test was
// written from reading the protocol source, before anything was run against a
// real server, and it was wrong. Setting the flag gets the connection dropped
// ~170ms after spawn with a PacketViolationWarning, every attempt.
//
// The guard lives in TestCameraRelativeMovementFlagStaysOff, which also pins
// the bit-57 baseline so it cannot pass vacuously.
