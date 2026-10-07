package player_test

import (
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/network/player"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// TestUnflaggedPositionEchoIsNotATeleport is the regression guard for the
// Geyser "walks in place" failure. A Geyser front-end relays ordinary client
// position updates as unflagged MovePlayer packets at tick rate, so
// acknowledging every one of them left the input loop permanently on its
// teleport branch: steering and physics are skipped on the ack tick, and a tick
// never came around that steered. The body stood still with a full set of walk
// inputs on the wire.
func TestUnflaggedPositionEchoIsNotATeleport(t *testing.T) {
	t.Parallel()

	local := mgl32.Vec3{87.0, 74.0, -54.0}

	// A one-centimetre echo of the bot's own position is what the relay sends
	// while the bot is walking normally.
	echo := local.Add(mgl32.Vec3{0.01, 0, -0.01})
	if player.ShouldAckTeleport(packet.MoveModeNormal, echo, local) {
		t.Fatal("an unflagged echo of the bot's own position was treated as a teleport; " +
			"the movement loop would never steer")
	}

	// The bot is mid-stride and the server's view lags slightly. Still not a
	// teleport.
	lag := local.Add(mgl32.Vec3{0.4, 0.1, 0.3})
	if player.ShouldAckTeleport(packet.MoveModeNormal, lag, local) {
		t.Fatal("a sub-block position lag was treated as a teleport")
	}

	// A 1-block descent with latency (~1.41 blocks away) must not be treated as a teleport.
	ledgeLag := local.Add(mgl32.Vec3{1.0, 1.0, 0.0})
	if player.ShouldAckTeleport(packet.MoveModeNormal, ledgeLag, local) {
		t.Fatal("a 1-block drop position lag was treated as a teleport")
	}
}

// TestFlaggedTeleportIsAcknowledged keeps the other half of the contract: a
// MovePlayer the server actually marks as a teleport or reset must still be
// acknowledged, or the server keeps re-sending the correction.
func TestFlaggedTeleportIsAcknowledged(t *testing.T) {
	t.Parallel()

	local := mgl32.Vec3{87.0, 74.0, -54.0}
	dest := mgl32.Vec3{200.0, 64.0, 300.0}

	for _, mode := range []byte{packet.MoveModeTeleport, packet.MoveModeReset} {
		if !player.ShouldAckTeleport(mode, dest, local) {
			t.Errorf("mode %d: flagged teleport was not acknowledged", mode)
		}
	}
}

// TestUnflaggedLargeJumpIsAcknowledged covers the host that moves the player
// without marking the packet: distance is the only signal left, so a jump well
// past the block the bot could have walked in one tick must still be acked.
func TestUnflaggedLargeJumpIsAcknowledged(t *testing.T) {
	t.Parallel()

	local := mgl32.Vec3{87.0, 74.0, -54.0}
	far := local.Add(mgl32.Vec3{30.0, 0, 0})

	if !player.ShouldAckTeleport(packet.MoveModeNormal, far, local) {
		t.Fatal("a 30-block unflagged jump was not acknowledged as a teleport")
	}

	boundaryFar := local.Add(mgl32.Vec3{2.5, 0, 0})
	if !player.ShouldAckTeleport(packet.MoveModeNormal, boundaryFar, local) {
		t.Fatal("a 2.5-block unflagged jump was not acknowledged as a teleport")
	}
}

func TestApplySelfMovePlayerPreservesLocalPosDuringWalk(t *testing.T) {
	t.Parallel()

	local := mgl32.Vec3{71.0, 72.0, -52.0}
	b := &bot.Bot{
		MovementState: "walk_to",
		Pos:           local,
	}

	// Echo from top of ledge (1-block drop lag ~1.41 blocks away)
	echo := mgl32.Vec3{72.0, 73.0, -52.0}
	ack := player.ApplySelfMovePlayer(b, packet.MoveModeNormal, echo)

	if ack {
		t.Fatal("normal 1-block drop echo was acknowledged as teleport")
	}
	if b.Pos != local {
		t.Fatalf("b.Pos = %v, want local %v preserved during walk", b.Pos, local)
	}
	if b.TeleportAckPending {
		t.Fatal("TeleportAckPending must not be set for unflagged lag during walk")
	}
	if b.ServerGroundY != 73.0 {
		t.Fatalf("ServerGroundY = %v, want 73.0", b.ServerGroundY)
	}
}

func TestApplySelfMovePlayerUpdatesPosWhenIdleOrTeleport(t *testing.T) {
	t.Parallel()

	// 1. Idle bot accepts position echo
	idleBot := &bot.Bot{
		MovementState: "idle",
		Pos:           mgl32.Vec3{10, 64, 10},
	}
	newPos := mgl32.Vec3{10.5, 64, 10.5}
	ack := player.ApplySelfMovePlayer(idleBot, packet.MoveModeNormal, newPos)
	if ack {
		t.Fatal("idle sub-block echo should not be acked as teleport")
	}
	if idleBot.Pos != newPos {
		t.Fatalf("idleBot.Pos = %v, want %v updated while idle", idleBot.Pos, newPos)
	}

	// 2. Active walk with real teleport mode updates pos and sets TeleportAckPending
	walkBot := &bot.Bot{
		MovementState: "walk_to",
		Pos:           mgl32.Vec3{10, 64, 10},
	}
	tpPos := mgl32.Vec3{100, 70, 100}
	ack = player.ApplySelfMovePlayer(walkBot, packet.MoveModeTeleport, tpPos)
	if !ack {
		t.Fatal("MoveModeTeleport must be acknowledged")
	}
	if walkBot.Pos != tpPos {
		t.Fatalf("walkBot.Pos = %v, want %v", walkBot.Pos, tpPos)
	}
	if !walkBot.TeleportAckPending {
		t.Fatal("TeleportAckPending must be true for MoveModeTeleport")
	}
}
