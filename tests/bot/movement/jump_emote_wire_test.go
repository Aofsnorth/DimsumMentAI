package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// The player-facing bug behind this file, verbatim: the bot is told to jump,
// rises, and then jumps again off the sky, with no gravity between the hops.
// ("lompat di udara naik terus lagi ... ngak ada gravity jir setiap dia lompat")
//
// The command path runs through the jump *emote* (emoteDefaults maps "jump" to
// the EmoteState of the same name), and the emote's window is ~80 ticks of
// flag-high. Two defects stacked on top of each other:
//
//  1. The packet derived its ground truth from TickContext.IsGrounded, which
//     the physics phase of the SAME tick has not written yet. Teardown order
//     means the live context still carries the previous tick's answer, except
//     the field was being rebuilt mid-tick by the time the packet writer ran,
//     so a waved emote could claim grounded on a falling body and pair
//     InputFlagJumping with VerticalCollision. The server reads that pair as
//     an unsupported jump, patches the prediction gap on its next correction,
//     and the client renders a second hop on the landing -- where the player
//     reads "no gravity, it just jumps again".
//  2. With the flag high for 80 ticks and no lifetime, every landing inside
//     the window re-bought the impulse, which is a held jump key on the wire,
//     which climbs the air on any server that honours it.
func TestJumpEmoteDoesNotPairJumpingWithAirborneCollision(t *testing.T) {
	t.Parallel()

	// The body is falling (previous tick's physics says airborne).
	b := newJumpTestBot()
	tc := &movement.TickContext{B: b}
	tc.SetSyncedGroundedForTest(false)

	flags := tc.BuildInputDataForTest(true, false)

	if flags.Load(packet.InputFlagJumping) {
		t.Fatal("emote jump flag high on an airborne body still sets InputFlagJumping; " +
			"that unsupported-jump pair is what the server patches into a visible second hop")
	}
	if flags.Load(packet.InputFlagVerticalCollision) {
		t.Fatal("emote jump flag high on an airborne body still sets VerticalCollision; " +
			"an airborne body has no floor, and the pair with Jumping is what the server rejects")
	}
}

func TestJumpEmoteBuysExactlyOnePhysicalHop(t *testing.T) {
	t.Parallel()

	b := newJumpTestBot()
	tc := &movement.TickContext{B: b, Gait: movement.NewGaitState()}
	tc.SetSyncedGroundedForTest(true)
	tc.ResetEmoteJumpForTest()

	// First grounded tick inside the window: the hop is bought.
	first := tc.BuildInputDataForTest(true, false)
	if !first.Load(packet.InputFlagJumping) {
		t.Fatal("the first grounded tick of a jump emote must set InputFlagJumping, " +
			"or the emote never leaves the floor at all")
	}
	if !tc.EmoteJumpSpentForTest() {
		t.Fatal("the one-hop latch must be spent by the tick that bought the impulse")
	}

	// The emote is still waving and the body is still grounded (server has not
	// even processed the hop yet): the wire must NOT carry a second impulse,
	// or the window reads as a held jump key.
	second := tc.BuildInputDataForTest(true, false)
	if second.Load(packet.InputFlagJumping) {
		t.Fatal("second grounded tick of the same emote still sets InputFlagJumping; " +
			"an 80-tick window of flag-high is a held key, which climbs the air")
	}
}

func TestJumpEmoteRemainsSpentUntilNextEmoteStarts(t *testing.T) {
	t.Parallel()

	b := newJumpTestBot()
	tc := &movement.TickContext{B: b, Gait: movement.NewGaitState()}
	tc.SetSyncedGroundedForTest(true)
	tc.ResetEmoteJumpForTest()

	// First grounded tick: hop fires.
	if got := tc.BuildInputDataForTest(true, false); !got.Load(packet.InputFlagJumping) {
		t.Fatal("precondition: the first hop must fire")
	}

	// Airborne: wire carries nothing, and latch STAYS spent so that landing
	// during this same emote window does not buy a second jump.
	tc.SetSyncedGroundedForTest(false)
	if got := tc.BuildInputDataForTest(true, false); got.Load(packet.InputFlagJumping) {
		t.Fatal("airborne tick of an emote jump must not set InputFlagJumping")
	}
	if !tc.EmoteJumpSpentForTest() {
		t.Fatal("the latch must stay spent while airborne so landing mid-emote does not hop again")
	}

	// Body lands while the first emote is still active (e.g. tick 12 of an 80-tick window):
	// MUST NOT jump again!
	tc.SetSyncedGroundedForTest(true)
	if got := tc.BuildInputDataForTest(true, false); got.Load(packet.InputFlagJumping) {
		t.Fatal("landing while the first emote is still ticking must not re-trigger a jump")
	}

	// Player sends a SECOND "lompat" command in chat!
	// TriggerEmoteFor resets EmoteJumpSpent for the new emote.
	b.TriggerEmoteFor("jump", 40)
	if got := tc.BuildInputDataForTest(true, false); !got.Load(packet.InputFlagJumping) {
		t.Fatal("a subsequent jump command must be able to jump again; " +
			"this was the 'pas aku suruh lompat lagi dia ngak bisa' bug")
	}
}
