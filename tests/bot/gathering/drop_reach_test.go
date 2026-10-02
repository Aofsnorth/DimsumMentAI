package gathering_test

import (
	"testing"

	"bedrock-ai/internal/bot/gathering"

	"github.com/go-gl/mathgl/mgl32"
)

// A drop the bot cannot see is a drop the bot does not collect, and the log
// said so plainly: three runs of "Starting item sweep" with no "heading to item
// drop" after any of them, followed by "Wood broken but not picked up" and a
// gather that reported failure on a bot holding 39 logs.
//
// The vertical window is the cause. It was hardcoded at three blocks up and four
// down, and a trunk is chopped by towering — the bot climbs, breaks the logs
// high up, and the drops fall to the base. Every one of them was rejected before
// the distance check even ran.

// TestADropOnTheGroundBelowATowerIsWithinReach is the reported case. The body is
// six blocks up because that is where the axe was, and the log is at its feet.
func TestADropOnTheGroundBelowATowerIsWithinReach(t *testing.T) {
	t.Parallel()

	// The bot is up the tower it built to reach the trunk.
	body := mgl32.Vec3{10.5, 88, 10.5}
	// The log dropped to the ground at the base when the trunk was broken.
	drop := mgl32.Vec3{10.5, 82, 10.5}

	if !gathering.DropWithinReach(body, drop, 8.0) {
		t.Errorf("a drop 6 blocks below the body was out of reach for an 8 block sweep: dy = %.1f", drop.Y()-body.Y())
	}
}

// TestTheVerticalWindowFollowsTheSweepRadius is the property, not the one case.
// A sweep cannot reject vertically what it would happily walk to horizontally —
// that inconsistency is the bug, and a single example of it would be fixed by
// widening the constant again the next time a taller tree came along.
func TestTheVerticalWindowFollowsTheSweepRadius(t *testing.T) {
	t.Parallel()

	body := mgl32.Vec3{0, 0, 0}

	for _, radius := range []float32{2, 4, 8, 16, 32} {
		for _, drop := range []mgl32.Vec3{
			{0, radius - 0.5, 0},    // just inside, above
			{0, -(radius - 0.5), 0}, // just inside, below
			{0, radius + 1, 0},      // just outside, above
			{0, -(radius + 1), 0},   // just outside, below
		} {
			got := gathering.DropWithinReach(body, drop, radius)
			want := drop.Y() <= radius && drop.Y() >= -radius
			if got != want {
				t.Errorf("radius %.0f, drop dy %.1f: within = %t, want %t", radius, drop.Y(), got, want)
			}
		}
	}
}

// TestADropDirectlyUnderfootIsWithinReach is the floor of the policy. A bot
// standing on a pile of its own drops is the most obviously collectable
// situation there is, and rejecting it is not a judgement call.
func TestADropDirectlyUnderfootIsWithinReach(t *testing.T) {
	t.Parallel()

	body := mgl32.Vec3{3.5, 70, -2.5}

	if !gathering.DropWithinReach(body, body, 8) {
		t.Error("a drop at the body's own position was out of reach")
	}
}

// TestADropWellBeyondTheRadiusIsStillRefused keeps the function a filter rather
// than a rubber stamp. A bot that chases every drop on the server is a bot that
// never finishes anything.
func TestADropWellBeyondTheRadiusIsStillRefused(t *testing.T) {
	t.Parallel()

	body := mgl32.Vec3{0, 0, 0}
	far := mgl32.Vec3{0, 100, 0}

	if gathering.DropWithinReach(body, far, 8) {
		t.Error("a drop 100 blocks up was accepted for an 8 block sweep")
	}
}
