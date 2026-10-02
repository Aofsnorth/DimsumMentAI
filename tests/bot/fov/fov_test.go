package fov_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/bot/fov"

	"github.com/go-gl/mathgl/mgl32"
)

// eye is a fixed origin; heading 0 faces +Z in this project.
var eye = mgl32.Vec3{0, 64, 0}

// at builds a point `dist` blocks along the given yaw.
func at(yaw, dist float32) mgl32.Vec3 {
	rad := float64(yaw+90) * math.Pi / 180
	return eye.Add(mgl32.Vec3{
		float32(math.Cos(rad)) * dist,
		0,
		float32(math.Sin(rad)) * dist,
	})
}

// TestSomethingDirectlyAheadIsVisible is the base case the whole cone rests on.
func TestSomethingDirectlyAheadIsVisible(t *testing.T) {
	t.Parallel()
	if !fov.Within(at(0, 10), eye, 0) {
		t.Error("a block 10 blocks straight ahead was reported as outside the cone")
	}
}

// TestSomethingDirectlyBehindIsNotVisible is the behaviour this package exists
// for. Line of sight alone would have reported the tree behind the player; the
// cone is what makes the bot's world a world it is facing.
func TestSomethingDirectlyBehindIsNotVisible(t *testing.T) {
	t.Parallel()
	if fov.Within(at(180, 10), eye, 0) {
		t.Error("a block 10 blocks directly behind was reported as visible")
	}
}

// TestCloseRangeIsSeenInEveryDirection models peripheral vision. A creeper at
// the bot's feet is noticed even if it is behind it, and hiding it would create
// a death the bot cannot explain.
func TestCloseRangeIsSeenInEveryDirection(t *testing.T) {
	t.Parallel()
	if !fov.Within(at(180, fov.CloseRadius-0.5), eye, 0) {
		t.Error("a mob at arm's length behind the bot was reported as unseen")
	}
	// Just past the close radius, the same direction is behind and unseen.
	if fov.Within(at(180, fov.CloseRadius+2), eye, 0) {
		t.Error("a block past the close radius behind the bot was reported as visible")
	}
}

// TestConeEdgesAreSymmetric catches a cone that was accidentally lopsided.
func TestConeEdgesAreSymmetric(t *testing.T) {
	t.Parallel()
	// Facing +Z (heading 0), the cone reaches HalfAngleDeg to either side.
	if !fov.Within(at(fov.HalfAngleDeg-2, 8), eye, 0) {
		t.Errorf("a block just inside the +%.0f deg cone edge was reported as unseen", fov.HalfAngleDeg)
	}
	if !fov.Within(at(-(fov.HalfAngleDeg-2), 8), eye, 0) {
		t.Errorf("a block just inside the -%.0f deg cone edge was reported as unseen", fov.HalfAngleDeg)
	}
	if fov.Within(at(fov.HalfAngleDeg+5, 8), eye, 0) {
		t.Errorf("a block just outside the +%.0f deg cone edge was reported as visible", fov.HalfAngleDeg)
	}
}

// TestHeadingWrapAroundIsHandled is the classic angular bug: a point at +179
// and one at -179 are two degrees apart, but naive subtraction makes them 358
// apart and would push both outside the cone.
func TestHeadingWrapAroundIsHandled(t *testing.T) {
	t.Parallel()
	// Heading 350 degrees. Straight ahead is heading 350, which wraps near -10.
	heading := float32(350)
	forward := at(heading, 8)
	if !fov.Within(forward, eye, heading) {
		t.Errorf("a block straight ahead of a heading of %.0f deg was reported as unseen", heading)
	}
	// And a point at 179 deg is nearly opposite, even though |179-350| is 171.
	behind := at(179, 8)
	if fov.Within(behind, eye, heading) {
		t.Errorf("a block nearly opposite a heading of %.0f deg was reported as visible", heading)
	}
}

// TestVerticalBoundsAreHonoured makes sure the cone is not a flat disc, and that
// the sky above the bot is treated as out of view while its feet are not.
func TestVerticalBoundsAreHonoured(t *testing.T) {
	t.Parallel()
	// Straight up, 3 blocks: close radius catches this, still visible.
	if !fov.Within(eye.Add(mgl32.Vec3{0, 2, 0}), eye, 0) {
		t.Error("a block directly overhead at close range was reported as unseen")
	}
	// Far up and ahead: the vertical arc is a real bound, not just a horizontal
	// one. 60 blocks up at 8 blocks out is ~82 degrees, past AboveDeg.
	up := at(0, 8).Add(mgl32.Vec3{0, 60, 0})
	if fov.Within(up, eye, 0) {
		t.Error("a block far above the cone was reported as visible")
	}
}

// TestPeripheralTargetsStayVisible guards against over-tightening the cone into
// something that makes the bot look blind. A chest a few degrees off the
// heading is exactly what a player notices in the corner of their eye.
func TestPeripheralTargetsStayVisible(t *testing.T) {
	t.Parallel()
	// 30 degrees off the heading at 6 blocks: comfortably noticed.
	if !fov.Within(at(30, 6), eye, 0) {
		t.Error("a chest 30 degrees off the heading was reported as unseen")
	}
}
