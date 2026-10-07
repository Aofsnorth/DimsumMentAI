package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// The player-facing bugs behind this file, verbatim: the bot is told to jump,
// rises, and then jumps again off the sky with no gravity between the hops
// ("lompat di udara naik terus lagi ... ngak ada gravity jir setiap dia
// lompat"); and, once the one-hop latch landed, a hop that does happen but is
// only a fraction of a block tall ("bisa lompat tapi pendek bgt, ngak normal").
//
// Both are decided by WHERE the emote hop is bought. The purchase runs before
// the physics phase (BuyEmoteJumpForTest here, buyEmoteJump in the tick loop),
// inside the branch that integrates it, so the impulse the wire announces is
// the impulse the body actually performs. The previous version bought the hop
// at packet time — after the physics had already run — so InputFlagJumping
// went out on position claims that never left the floor: the server arced the
// body up, the bot's own claims stayed at ground level, and the tug of war
// rendered as a fraction-block hop.
//
// The tests stage the tick's velocity on tc.VelY, because that is the copy the
// jump gate reads; production seeds it from the bot's velocity when the tick
// context is built.
func TestJumpEmoteDoesNotPairJumpingWithAirborneCollision(t *testing.T) {
	t.Parallel()

	// The body is falling: previous tick's physics says airborne, and the
	// tick's velocity copy carries real downward motion.
	b := newJumpTestBot()
	tc := &movement.TickContext{B: b, VelY: -0.46}
	tc.SetSyncedGroundedForTest(false)

	tc.BuyEmoteJumpForTest()
	if tc.ShouldJump {
		t.Fatal("a jump emote bought a hop on a falling body; that impulse is what turns one hop into climbing the air")
	}

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
	b.TriggerEmoteFor("jump", 40)

	// First grounded tick inside the window: the hop is bought, and the
	// physics of that same tick integrates the impulse the wire will carry.
	tc := &movement.TickContext{B: b, Gait: movement.NewGaitState(), IsGrounded: true}
	tc.SetSyncedGroundedForTest(true)

	tc.BuyEmoteJumpForTest()
	if !tc.ShouldJump {
		t.Fatal("the first grounded tick of a jump emote must buy the hop, " +
			"or the emote never leaves the floor at all")
	}
	tc.ApplyVerticalVelocityForTest()
	if got := tc.VelYForTest(); got < 0.4 {
		t.Fatalf("bought hop integrated to velY %.2f, want the 0.42 impulse; "+
			"a wire flag without its impulse behind it is the truncated hop", got)
	}
	if !tc.EmoteJumpSpentForTest() {
		t.Fatal("the one-hop latch must be spent by the tick that bought the impulse")
	}
	if flags := tc.BuildInputDataForTest(true, false); !flags.Load(packet.InputFlagJumping) {
		t.Fatal("the tick that integrated the impulse must carry InputFlagJumping on the wire")
	}

	// A second tick of the same emote, body still grounded (the server has not
	// even processed the first hop yet): no second purchase, no second flag,
	// or the 80-tick window reads as a held jump key, which climbs the air.
	tc2 := &movement.TickContext{B: b, Gait: movement.NewGaitState(), IsGrounded: true}
	tc2.SetSyncedGroundedForTest(true)

	tc2.BuyEmoteJumpForTest()
	if tc2.ShouldJump {
		t.Fatal("second grounded tick of the same emote bought another hop")
	}
	tc2.ApplyVerticalVelocityForTest()
	if flags := tc2.BuildInputDataForTest(true, false); flags.Load(packet.InputFlagJumping) {
		t.Fatal("second grounded tick of the same emote still sets InputFlagJumping; " +
			"a held key is what climbs the air")
	}
}

func TestJumpEmoteRemainsSpentUntilNextEmoteStarts(t *testing.T) {
	t.Parallel()

	b := newJumpTestBot()
	b.TriggerEmoteFor("jump", 40)

	// First grounded tick: hop fires and integrates.
	tc := &movement.TickContext{B: b, Gait: movement.NewGaitState(), IsGrounded: true}
	tc.SetSyncedGroundedForTest(true)
	tc.BuyEmoteJumpForTest()
	tc.ApplyVerticalVelocityForTest()
	if flags := tc.BuildInputDataForTest(true, false); !flags.Load(packet.InputFlagJumping) {
		t.Fatal("precondition: the first hop must fire")
	}

	// Airborne: the purchase is refused, the wire carries nothing, and the
	// latch STAYS spent so landing mid-emote cannot hop again.
	tc2 := &movement.TickContext{B: b, Gait: movement.NewGaitState(), VelY: 0.34}
	tc2.SetSyncedGroundedForTest(false)
	tc2.BuyEmoteJumpForTest()
	if tc2.ShouldJump {
		t.Fatal("airborne tick of an emote jump bought a hop")
	}
	if flags := tc2.BuildInputDataForTest(true, false); flags.Load(packet.InputFlagJumping) {
		t.Fatal("airborne tick of an emote jump must not set InputFlagJumping")
	}
	if !tc2.EmoteJumpSpentForTest() {
		t.Fatal("the latch must stay spent while airborne so landing mid-emote does not hop again")
	}

	// Body lands while the first emote is still active (e.g. tick 12 of an
	// 80-tick window): MUST NOT jump again.
	tc3 := &movement.TickContext{B: b, Gait: movement.NewGaitState()}
	tc3.SetSyncedGroundedForTest(true)
	tc3.BuyEmoteJumpForTest()
	if tc3.ShouldJump {
		t.Fatal("landing while the first emote is still ticking must not re-trigger a jump")
	}

	// Player sends a SECOND "lompat" command in chat. TriggerEmoteFor resets
	// EmoteJumpSpent for the new emote.
	b.TriggerEmoteFor("jump", 40)
	tc4 := &movement.TickContext{B: b, Gait: movement.NewGaitState()}
	tc4.SetSyncedGroundedForTest(true)
	tc4.BuyEmoteJumpForTest()
	if !tc4.ShouldJump {
		t.Fatal("a subsequent jump command must be able to jump again; " +
			"this was the 'pas aku suruh lompat lagi dia ngak bisa' bug")
	}
}
