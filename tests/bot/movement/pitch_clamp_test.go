package movement_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/bot/movement"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// A pitch of exactly -90.0 or exactly +90.0 is a value no human client reports.
// The protocol accepts it, but it sits where the pitch derivative vanishes, so
// any eased angle looking straight down rounds onto the boundary and then stays
// there for as long as the bot keeps looking at its own feet. Mining does
// exactly that, which turned the boundary from a rounding artefact into a
// standing value.

// buildPacketAtPitch runs the real packet builder at a given pitch, because the
// clamp has to hold on the wire and not merely where the gaze was computed. Any
// of the several routes into the packet -- eased pitch, walking gaze offsets,
// look emote sweeps, organic drift -- would defeat a test aimed at only one.
func buildPacketAtPitch(pitch float32) *packet.PlayerAuthInput {
	tc := &movement.TickContext{Pitch: pitch, Yaw: 12, HeadYaw: 14}
	return tc.BuildPlayerAuthInputPacketForTest()
}

func TestClampPitchNeverReturnsTheBoundary(t *testing.T) {
	t.Parallel()

	for _, in := range []float32{-180, -90, -89.999, 90, 90.001, 180, 1e9, -1e9} {
		got := movement.ClampPitch(in)
		if got == 90 || got == -90 {
			t.Fatalf("ClampPitch(%v) returned the boundary value %v exactly", in, got)
		}
		if math.Abs(float64(got)) > movement.PitchBoundary {
			t.Fatalf("ClampPitch(%v) escaped the protocol limit: %v", in, got)
		}
		if got >= movement.PitchBoundary || got <= -movement.PitchBoundary {
			t.Fatalf("ClampPitch(%v) = %v is not strictly inside the limit", in, got)
		}
	}
}

func TestClampPitchLeavesOrdinaryAnglesAlone(t *testing.T) {
	t.Parallel()

	// Every angle a player actually looks at must pass through untouched. A
	// clamp that nudged the middle of the range would silently mis-aim the bot
	// on every block it interacted with.
	for _, in := range []float32{0, -30, -45, 15, 60, 89, -89.9} {
		if got := movement.ClampPitch(in); got != in {
			t.Fatalf("ClampPitch(%v) = %v, want it unchanged", in, got)
		}
	}
}

func TestPacketPitchIsNeverExactlyStraightDownOrUp(t *testing.T) {
	t.Parallel()

	// Straight down is the mining case and straight up is the sky case; both
	// are angles the eased pitch settles onto and then holds.
	for _, pitch := range []float32{-90, -100, -1e6, 90, 100, 1e6} {
		pk := buildPacketAtPitch(pitch)
		if pk.Pitch == 90 || pk.Pitch == -90 {
			t.Fatalf("pitch %.0f went out on the wire as exactly %.1f", pitch, pk.Pitch)
		}
		if math.Abs(float64(pk.Pitch)) >= movement.PitchBoundary {
			t.Fatalf("pitch %.0f escaped the limit: %.3f", pitch, pk.Pitch)
		}
	}
}

func TestPacketInteractPitchIsClampedToo(t *testing.T) {
	t.Parallel()

	// InteractPitch carries the drift on top of the pitch, so it can reach the
	// boundary from a perfectly legal pitch. Clamping only Pitch would leave the
	// crosshair direction -- the one the server validates block interaction
	// against -- sitting on the impossible value.
	for _, pitch := range []float32{-90, -120, 120} {
		pk := buildPacketAtPitch(pitch)
		if pk.InteractPitch == 90 || pk.InteractPitch == -90 {
			t.Fatalf("InteractPitch for pitch %.0f went out as exactly %.1f", pitch, pk.InteractPitch)
		}
	}
}

func TestPacketStillAimsAtBlocksBelowTheBot(t *testing.T) {
	t.Parallel()

	// The margin has to be too small to matter for aiming. Half a tenth of a
	// degree over the four-block reach a player has on a block is about two
	// millimetres, which is well inside the width of a block face.
	margin := float64(movement.PitchMargin)
	reach := 4.0
	offset := math.Sin(margin*math.Pi/180) * reach
	if offset > 0.01 {
		t.Fatalf("a %.2f degree margin shifts the crosshair %.4f blocks at %.0f blocks reach, "+
			"which is enough to miss a block face", movement.PitchMargin, offset, reach)
	}
}
