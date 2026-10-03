// The regression this package exists for.
//
// Every assertion below is a coordinate the one-liner `int32(v)` gets wrong.
// They are the whole reason blockcell is a package rather than a comment: a
// comment is not executable, so it cannot fail when someone writes the one-liner
// anyway, and it cannot be regressed against.
package blockcell_test

import (
	"testing"

	"bedrock-ai/internal/blockcell"
)

// Bedrock reports an entity at the centre of the cell it stands in, so the
// coordinates that matter in practice always carry a .5. These are the real
// inputs; the integers are here only to pin the boundary.
func TestOfResolvesTheCellOnBothHalvesOfTheAxis(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   float32
		want int32
	}{
		// Positive half: truncation happens to agree. Pinned so a change that
		// breaks this is caught too, not just one that breaks the negative half.
		{"centre of cell 0", 0.5, 0},
		{"centre of cell 1", 1.5, 1},
		{"centre of cell 100", 100.5, 100},

		// Negative half: this is where truncation is wrong. The centre of cell
		// -1 is -0.5 and int32(-0.5) is 0, one cell east of where the entity is.
		{"centre of cell -1", -0.5, -1},
		{"centre of cell -2", -1.5, -2},
		{"centre of cell -100", -99.5, -100},

		// Cell boundaries. A cell spans [n, n+1), so the lower bound belongs to
		// the cell and the upper bound belongs to the next one.
		{"origin", 0, 0},
		{"upper bound of cell -1 is cell 0", 0.999, 0},
		{"lower bound of cell -1 is cell -1", -1, -1},
		{"just inside cell -1", -0.999, -1},
		{"just inside cell -2", -1.001, -2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := blockcell.Of(tc.in); got != tc.want {
				t.Errorf("blockcell.Of(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestOfAgreesWithFloor is the invariant the package is named for. Stating it
// directly means a future change to the implementation has to argue with this
// line rather than quietly pass.
func TestOfAgreesWithFloor(t *testing.T) {
	t.Parallel()

	// A range wide enough to cross zero and cover several negative cells, so
	// the disagreement region is sampled rather than asserted once.
	for half := -400; half <= 400; half++ {
		for tenth := 1; tenth < 10; tenth++ {
			v := float32(half) + float32(tenth)/10
			got := blockcell.Of(v)
			truncated := int32(v)

			if got == truncated && v != float32(int32(v)) {
				// v has a fractional part, so the two must differ somewhere on
				// the negative half. Landing on agreement here means the
				// conversion has started truncating again.
				if v < 0 {
					t.Fatalf("blockcell.Of(%v) = %d, which matches truncation; "+
						"the negative half of the axis has regressed to int32(v)", v, got)
				}
			}
		}
	}
}

// TestXYZKeepsAxesAligned is the reason this returns three values instead of a
// slice: an x, y, z triple that cannot be indexed out of order.
func TestXYZKeepsAxesAligned(t *testing.T) {
	t.Parallel()

	x, y, z := blockcell.XYZ(-10.5, 64.5, -20.5)
	if x != -11 {
		t.Errorf("x = %d, want -1%d", x, 1)
	}
	if y != 64 {
		t.Errorf("y = %d, want 64", y)
	}
	if z != -21 {
		t.Errorf("z = %d, want -2%d", z, 1)
	}
}

// TestXYZIsNotSwappableUnderSymmetricInput gives a swapped-axis bug somewhere
// to be caught. With distinct per-axis values a swap would be visible; with
// this input it is not, so the explicit expectations above carry the weight.
func TestXYZIsNotSwappableUnderSymmetricInput(t *testing.T) {
	t.Parallel()

	x, y, z := blockcell.XYZ(1.5, 2.5, 3.5)
	if x != 1 || y != 2 || z != 3 {
		t.Errorf("XYZ(1.5, 2.5, 3.5) = (%d, %d, %d), want (1, 2, 3)", x, y, z)
	}
}
