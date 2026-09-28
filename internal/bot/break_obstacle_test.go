package bot

import (
	"math"
	"testing"
	"time"

	"bedrock-ai/internal/bot/movement/animation"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// TestObstacleBreakUsesTheSharedHumanRhythm is the regression for the
// "unnatural breaking" report. The unstick break ran on its own fixed 300 ms
// metronome, which restarts the viewer's arm-swing cycle (~300 ms) before it
// finishes: the arm reads as vibrating instead of swinging, and because each
// swing is what drives a client's dig sound, the same interval is heard as a
// machine-gun rattle. It now has to use the shared rhythm like every other
// break path.
func TestObstacleBreakUsesTheSharedHumanRhythm(t *testing.T) {
	t.Parallel()

	aim := obstacleAim(blockPosFixture())
	beats := animation.Beats(obstacleBreakDuration, aim)

	if len(beats) < 3 {
		t.Fatalf("an unstick break produced %d beats, want a wind-up plus several swings", len(beats))
	}
	// A fixed 300ms tick cannot be a wind-up: the wind-up is its own, shorter
	// beat, which is what stops the first swing from looking instant.
	if beats[0].Wait >= animation.SwingMin {
		t.Fatalf("first beat %v is not a wind-up, want a distinct beat under %v", beats[0].Wait, animation.SwingMin)
	}

	varied := map[time.Duration]bool{}
	total := animation.WindUpMin
	for _, beat := range beats[1:] {
		if beat.Wait < animation.SwingMin {
			t.Fatalf("swing pause %v is faster than the arm-swing animation, viewers see a vibration", beat.Wait)
		}
		varied[beat.Wait] = true
		total += beat.Wait
	}
	if len(varied) < 3 {
		t.Fatalf("only %d distinct swing pauses in one break, want a varied rhythm", len(varied))
	}
	if total > obstacleBreakDuration {
		t.Fatalf("beats total %v, want no overshoot of the %v break", total, obstacleBreakDuration)
	}
}

// TestObstacleBreakWaitsLongEnoughForTheBlock checks the other half: the bot is
// already wedged, so a rejected destroy — the block staying put while the bot
// pushes the same wall — is the expensive failure, and a generous wait is free.
func TestObstacleBreakWaitsLongEnoughForTheBlock(t *testing.T) {
	t.Parallel()

	beats := animation.Beats(obstacleBreakDuration, obstacleAim(blockPosFixture()))
	if len(beats) < 4 {
		t.Fatalf("break of %v produced %d beats, want several swings before the destroy", obstacleBreakDuration, len(beats))
	}
	if !hardToBreak("minecraft:deepslate") || hardToBreak("minecraft:dirt") {
		t.Fatal("deepslate is not treated as harder than dirt, want the longer wait")
	}
	if !hardToBreak("minecraft:cobblestone") {
		t.Error("cobblestone was treated as a soft block, want the stone wait")
	}
	if hardToBreak("minecraft:tall_grass") {
		t.Error("tall grass takes the hard-block wait, want the short one")
	}
}

// TestObstacleAimIsTheBlockFaceAndJitters checks the aim the shared rhythm is
// handed: the top face of the block the bot is wedged against, jittered only by
// as much as the rhythm allows so the head still tracks the same block.
func TestObstacleAimIsTheBlockFaceAndJitters(t *testing.T) {
	t.Parallel()

	pos := blockPosFixture()
	aim := obstacleAim(pos)
	if aim.X() != float32(pos.X())+0.5 || aim.Z() != float32(pos.Z())+0.5 {
		t.Fatalf("aim %v is not centred on the block %v", aim, pos)
	}
	if aim.Y() != float32(pos.Y())+float32(obstacleBreakFace) {
		t.Fatalf("aim %v does not match the %d face the break claims", aim, obstacleBreakFace)
	}

	for i := 0; i < 20; i++ {
		jittered := animation.JitteredAim(aim)
		if math.Abs(float64(jittered.X()-aim.X())) > animation.AimJitter ||
			math.Abs(float64(jittered.Y()-aim.Y())) > animation.AimJitter ||
			math.Abs(float64(jittered.Z()-aim.Z())) > animation.AimJitter {
			t.Fatalf("jittered aim %v wandered further than %v from %v", jittered, animation.AimJitter, aim)
		}
	}
}

// blockPosFixture is a single cell to aim at; nothing in these tests reads the
// world, so the position only has to be a well-formed block coordinate.
func blockPosFixture() protocol.BlockPos {
	return protocol.BlockPos{3, 64, -2}
}
