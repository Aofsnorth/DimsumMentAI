// The server anchor and the jump arc share the same air, and the anchor must
// never win at the apex. "Lompat" rendered as a fraction-block hop under
// heavy gravity because the pin fired exactly where a rise crosses zero:
// velocity dips under the movement guard, the (now longer-fresh) anchor sees
// a grounded-false body near its confirmed Y, and yanks NextY straight back
// to the floor while the server's own simulation has the body a full jump up.
package movement_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"

	"github.com/go-gl/mathgl/mgl32"
)

func newAnchorTestBot() *bot.Bot {
	return &bot.Bot{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:   "TestBot",
	}
}

func freshAnchor(b *bot.Bot, y float32) {
	b.ServerGroundY = y
	b.ServerGroundAt = time.Now()
}

// TestAnchorDoesNotPinAJumpApex: a body a full jump above the confirmed Y
// with its rise just spent is mid-flight. The anchor must leave it alone and
// let gravity finish the arc the server is also simulating.
func TestAnchorDoesNotPinAJumpApex(t *testing.T) {
	t.Parallel()
	b := newAnchorTestBot()
	freshAnchor(b, 100.0)

	apex := &movement.TickContext{
		B:       b,
		CurrPos: mgl32.Vec3{0.5, 101.25, 0.5}, // full vanilla jump above the floor
		VelY:    0.02,                         // the rise just spent — the hole the old guard had
	}
	apex.ApplyServerPositionAnchor()

	if apex.IsGrounded {
		t.Fatal("anchor grounded the body at its jump apex; that pin is the sun-gravity hop")
	}
	if apex.NextY != 0 {
		t.Fatalf("anchor wrote NextY %.2f at the apex; flight must keep its own arc", apex.NextY)
	}
	if apex.VelY != 0.02 {
		t.Fatalf("anchor zeroed VelY %.3f at the apex; the rise must not be cancelled", apex.VelY)
	}
}

// TestAnchorStillHoldsASaggingBody is the half that must not regress: the
// anchor exists for the body that sagged BELOW the server's confirmed Y
// because the local model cannot see the floor (slabs, snow, undecoded
// chunks). That direction keeps the pin.
func TestAnchorStillHoldsASaggingBody(t *testing.T) {
	t.Parallel()
	b := newAnchorTestBot()
	freshAnchor(b, 100.0)

	sagging := &movement.TickContext{
		B:       b,
		CurrPos: mgl32.Vec3{0.5, 99.6, 0.5}, // half a block below the confirmed Y
		VelY:    0,
	}
	sagging.ApplyServerPositionAnchor()

	if !sagging.IsGrounded {
		t.Fatal("a sagging body must still be held by the server anchor")
	}
	if sagging.NextY != 100.0 {
		t.Fatalf("sagging body NextY = %.2f, want the confirmed 100.0", sagging.NextY)
	}
	if sagging.VelY != 0 {
		t.Fatalf("sagging body VelY = %.3f, want zeroed by the hold", sagging.VelY)
	}
}

// TestAnchorHoldsBodyAtConfirmedY: the ordinary idle case — feet at the
// confirmed Y over a floor the model cannot see — stays held.
func TestAnchorHoldsBodyAtConfirmedY(t *testing.T) {
	t.Parallel()
	b := newAnchorTestBot()
	freshAnchor(b, 100.0)

	idle := &movement.TickContext{
		B:       b,
		CurrPos: mgl32.Vec3{0.5, 100.0, 0.5},
		VelY:    0,
	}
	idle.ApplyServerPositionAnchor()

	if !idle.IsGrounded {
		t.Fatal("an idle body at the confirmed Y must be held grounded")
	}
	if idle.NextY != 100.0 {
		t.Fatalf("held body NextY = %.2f, want the confirmed 100.0", idle.NextY)
	}
}
