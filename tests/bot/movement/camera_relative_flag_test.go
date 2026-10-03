package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// InputFlagCameraRelativeMovementEnabled is bit 63 of PlayerAuthInput's
// InputData. It is semantically correct -- the bot's move vector really is
// camera-space -- and setting it kills the connection.
//
// On a server whose protocol predates bit 63 the flag decodes as an
// out-of-range enum. The server replies with a PacketViolationWarning carrying
// "enum value is deprecated / readNoHeader failed! packetId: 144" (wire id 144
// is PlayerAuthInput) and closes the socket ~170ms after spawn, before the
// world is usable, on every single attempt.
//
// This pins the flag OFF so the failure cannot come back as an "obvious
// correctness fix". Bit 57 (BlockBreakingDelayEnabled) is unaffected and is
// asserted here too, because the two flags look alike and differ only in the
// protocol version that introduced them.
func TestCameraRelativeMovementFlagStaysOff(t *testing.T) {
	t.Parallel()

	tc := &movement.TickContext{Gait: movement.NewGaitState(), IsGrounded: true}
	flags := tc.BuildInputDataForTest(false, false)

	if flags.Load(packet.InputFlagCameraRelativeMovementEnabled) {
		t.Fatal("InputFlagCameraRelativeMovementEnabled (bit 63) is set; servers " +
			"below that protocol reject it with a PacketViolationWarning and kick " +
			"the connection ~170ms after spawn")
	}

	// The regression guard is only meaningful if the surrounding baseline still
	// goes out. If buildInputData stopped setting anything at all, the assertion
	// above would pass for the wrong reason.
	if !flags.Load(packet.InputFlagBlockBreakingDelayEnabled) {
		t.Fatal("precondition: bit 57 must still be set every tick, otherwise this " +
			"test passes vacuously")
	}
}
