package gathering_test

import (
	"math"
	"testing"
	"time"

	"bedrock-ai/internal/bot/gathering"
	"bedrock-ai/internal/bot/movement/animation"

	"github.com/go-gl/mathgl/mgl32"
)

// Scaffolding is the one place the bot has to place a block into its own space
// and rise onto it, which is a sequence rather than a click: aim, jump, place,
// land. Get any step wrong and the bot does not climb — it just stands under a
// tower, swinging its head, until it runs out of blocks.

// TestTheScaffoldAimIsTheTopOfTheBlockBelow is the aiming rule. The bot used to
// look two blocks below its own feet, re-issued every iteration, 50ms apart.
//
// A look target is smoothed towards over roughly a second, so a new one every
// 50ms is a target the head can never reach: the result is a head permanently
// chasing downwards, which reads as a tremor. And two blocks below the feet is
// not the block being placed at all — the placement is on the TOP face of the
// block the bot is standing on, whose centre is half a block under the feet.
func TestTheScaffoldAimIsTheTopOfTheBlockBelow(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{10.3, 64.0, -5.7}
	aim := gathering.ScaffoldPlaceAim(feet)

	// Half a block down — the top face of the block under the feet. Compared
	// with a tolerance rather than a one-sided bound, because a bound that
	// rejects 63.5 rejects the very value it names in its own message.
	if got := aim.Y(); math.Abs(float64(got-(feet.Y()-0.5))) > 0.05 {
		t.Errorf("aim Y = %v, want the top face of the block below the feet (~%v)", got, feet.Y()-0.5)
	}
	if aim.X() < 10 || aim.X() >= 11 {
		t.Errorf("aim X = %v, want the centre of the column at X=10", aim.X())
	}
	if aim.Z() < -6 || aim.Z() >= -5 {
		t.Errorf("aim Z = %v, want the centre of the column at Z=-6", aim.Z())
	}
}

// TestTheScaffoldAimIsStableWhileTheBotStandsStill is the half that actually
// causes the tremor. A stand-still bot produces the same aim on every call, so
// the look ease has time to settle instead of being handed a fresh target
// forever.
func TestTheScaffoldAimIsStableWhileTheBotStandsStill(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{10.3, 64.0, -5.7}
	first := gathering.ScaffoldPlaceAim(feet)
	for i := 0; i < 10; i++ {
		if got := gathering.ScaffoldPlaceAim(feet); got != first {
			t.Fatalf("the aim moved while the bot stood still: %v then %v", first, got)
		}
	}
}

// TestTheScaffoldPlacementHasTimeToLand pins the two waits in the sequence. The
// settle has to outlast the look ease or the bot places at whatever it happened
// to be pointing at, and the jump has to be held long enough for the rise to
// begin before the block goes down underneath it.
func TestTheScaffoldPlacementHasTimeToLand(t *testing.T) {
	t.Parallel()

	if gathering.ScaffoldSettle < 200*time.Millisecond {
		t.Errorf("settle is %v; the look ease needs longer than that, so the bot places at the wrong angle",
			gathering.ScaffoldSettle)
	}
	if gathering.ScaffoldJumpTicks < 5 {
		t.Errorf("jump is held for %d ticks; at 20Hz that is not long enough to leave the ground",
			gathering.ScaffoldJumpTicks)
	}
	if gathering.ScaffoldSettle <= animation.SwingMin {
		t.Error("the settle is shorter than a swing pause; the two are pacing the same gesture")
	}
}
