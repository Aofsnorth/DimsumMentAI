package movement

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// GaitState is the per-connection record of the three level-triggered gait
// inputs a desktop client sends: sprint, sneak and jump.
//
// Why this exists
// ---------------
// InputFlagSprinting, InputFlagSneaking and InputFlagJumping say "this input is
// held right now". They are level flags: a running client leaves Sprinting set
// on every single tick of a run, and that part was already correct. What was
// missing is that a real client ALSO emits an edge on the tick the input
// changes:
//
//	StartSprinting  StopSprinting
//	StartSneaking   StopSneaking
//	StartJumping
//
// The level flag tells the server what is true now; the edge flag tells it that
// something just changed. Entering and leaving water already got this right
// (see ApplySwimInputFlags, which has emitted StartSwimming/StopSwimming all
// along), which is why entering a pool reads correctly while breaking into a
// sprint does not.
//
// The practical effect is asymmetry rather than breakage: sprinting still works,
// sneaking still works. What the server cannot see is the moment of transition.
// A client that teleports between "not sprinting" and "sprinting" with no edge
// is reporting state without ever reporting change. That is the same class of
// tell as the missing BlockBreakingDelayEnabled flag that turned up in MITM
// capture, and it is the kind of difference that is invisible in a functional
// test and obvious in a packet diff against a real client.
//
// Why the state is per-connection and not per-tick
// ------------------------------------------------
// An edge is a comparison against the previous tick, so the previous tick has
// to survive. TickContext is rebuilt every tick by SendInputLoop, so comparing
// against a fresh zero value every tick would report StartSprinting on all 20
// ticks of a second and never report StopSprinting at all. GaitState is created
// once per connection for exactly the reason the swim controller is: an edge is
// only meaningful relative to the tick before it.
type GaitState struct {
	sprinting bool
	sneaking  bool
	jumping   bool
}

// NewGaitState returns the state a connection starts from: not sprinting, not
// sneaking, not jumping.
//
// A real client has pressed nothing on the tick it connects, so the first tick
// after spawn must not claim a transition out of a state it was never in.
func NewGaitState() *GaitState {
	return &GaitState{}
}

// ApplyGaitEdges writes the transition flags implied by moving from the recorded
// state to the level flags actually present in this tick's inputData, then
// records the new state for the next tick.
//
// It reads the level state back out of inputData rather than taking it as an
// argument, because more than one subsystem writes these flags: the land gait in
// buildInputData and the swim intent in applySwimInputFlags each set Sprinting,
// Sneaking and Jumping independently. Reading the assembled set is the only way
// the edge is guaranteed not to contradict the level flag travelling on the same
// packet -- an edge announcing a sprint start under a packet that does not carry
// Sprinting is strictly worse than no edge at all, because it is actively
// inconsistent rather than merely absent.
//
// The sprint/sneak edges are emitted only in the direction that changed. A jump
// has no matching StopJumping flag in the protocol: the level flag clearing is
// the release, which is why this only ever raises StartJumping.
func (g *GaitState) ApplyGaitEdges(inputData *protocol.InputFlags) {
	if g == nil || inputData == nil || !inputData.Present() {
		return
	}

	sprinting := inputData.Load(packet.InputFlagSprinting)
	sneaking := inputData.Load(packet.InputFlagSneaking)
	jumping := inputData.Load(packet.InputFlagJumping)

	switch {
	case sprinting && !g.sprinting:
		setInputFlag(inputData, packet.InputFlagStartSprinting)
	case !sprinting && g.sprinting:
		setInputFlag(inputData, packet.InputFlagStopSprinting)
	}

	switch {
	case sneaking && !g.sneaking:
		setInputFlag(inputData, packet.InputFlagStartSneaking)
	case !sneaking && g.sneaking:
		setInputFlag(inputData, packet.InputFlagStopSneaking)
	}

	if jumping && !g.jumping {
		setInputFlag(inputData, packet.InputFlagStartJumping)
	}

	g.sprinting, g.sneaking, g.jumping = sprinting, sneaking, jumping
}

// Sprinting reports whether the previous tick was sent as a sprint, for the
// callers that need to know the gait without contributing to it.
func (g *GaitState) Sprinting() bool {
	if g == nil {
		return false
	}
	return g.sprinting
}
