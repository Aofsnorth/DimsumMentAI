// The click point has to name a point on the face being clicked.
//
// ClickedPosition is measured from the corner of the block named by
// BlockPosition, and the server checks it against BlockFace. A block's centre —
// {0.5, 0.5, 0.5}, by far the most natural-looking constant to type — is
// inside the block and on none of its six faces. It is the value every
// placement in the codebase reached for, and it is wrong for all six.
//
// This was found in four places that all sent BlockFace with a centre point,
// while a sibling function in the same file sent the right one. The reason it
// survived: a centre point is not obviously wrong, and a test that only checks
// "does the packet contain a ClickedPosition" passes against it.

package interact_test

import (
	"testing"

	"bedrock-ai/internal/bot/interact"

	"github.com/go-gl/mathgl/mgl32"
)

// TestAClickPointLandsOnTheFaceItNames is the property that matters. For each
// face, exactly one axis must sit on that face's boundary and the other two must
// sit at the block's centre line.
func TestAClickPointLandsOnTheFaceItNames(t *testing.T) {
	t.Parallel()

	// face -> the single axis that must be on a boundary, and which way.
	cases := []struct {
		face      int32
		name      string
		boundary  func(mgl32.Vec3) float32 // returns the value of the axis on the face
		atLowEnd  bool                     // true if the boundary is 0 rather than 1
		otherAxes [2]int                   // which of x/y/z must be 0.5
	}{
		{0, "down", func(v mgl32.Vec3) float32 { return v.Y() }, true, [2]int{0, 2}},
		{1, "up", func(v mgl32.Vec3) float32 { return v.Y() }, false, [2]int{0, 2}},
		{2, "north", func(v mgl32.Vec3) float32 { return v.Z() }, true, [2]int{0, 1}},
		{3, "south", func(v mgl32.Vec3) float32 { return v.Z() }, false, [2]int{0, 1}},
		{4, "west", func(v mgl32.Vec3) float32 { return v.X() }, true, [2]int{1, 2}},
		{5, "east", func(v mgl32.Vec3) float32 { return v.X() }, false, [2]int{1, 2}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := interact.FaceClickedPosition(tc.face)
			axes := [3]float32{v.X(), v.Y(), v.Z()}

			got := tc.boundary(v)
			if tc.atLowEnd {
				if got > 0.2 {
					t.Errorf("%v face click point %v is not near the low boundary "+
						"(z/x/y = %v): it must sit on the face, not inside the block",
						tc.face, v, got)
				}
			} else if got < 0.8 {
				t.Errorf("%v face click point %v is not near the high boundary "+
					"(x/y/z = %v): it must sit on the face, not inside the block",
					tc.face, v, got)
			}

			for _, i := range tc.otherAxes {
				if axes[i] != 0.5 {
					t.Errorf("%v face click point %v has axis %d at %v, want 0.5: "+
						"the click must be centred on the face, not on an edge or corner",
						tc.face, v, i, axes[i])
				}
			}
		})
	}
}

// TestNoFaceClickPointIsTheBlockCentre is the guard the bug slipped past. The
// centre is the one value that is wrong for every face, so it is the one value
// that can be rejected outright.
func TestNoFaceClickPointIsTheBlockCentre(t *testing.T) {
	t.Parallel()

	centre := mgl32.Vec3{0.5, 0.5, 0.5}
	for face := int32(0); face <= 5; face++ {
		if got := interact.FaceClickedPosition(face); got == centre {
			t.Errorf("face %d returns the block centre %v, which is inside the block "+
				"and on none of its faces", face, centre)
		}
	}
}

// TestAnUnknownFaceIsNotSilentlyTreatedAsCentre pins the fallback. A face the
// code does not know has no defensible click point, and quietly substituting one
// that is guaranteed to be wrong is how an unknown face becomes a block placed
// in the wrong cell.
func TestAnUnknownFaceIsNotSilentlyTreatedAsCentre(t *testing.T) {
	t.Parallel()

	centre := mgl32.Vec3{0.5, 0.5, 0.5}
	if got := interact.FaceClickedPosition(99); got == centre {
		t.Error("an unknown face returns the block centre; a caller with a bad face " +
			"value gets a click that lands inside a block instead of on it")
	}
}
