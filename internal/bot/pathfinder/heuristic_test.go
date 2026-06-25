package pathfinder

import (
	"math"
	"testing"
)

func TestDistance_SameNode(t *testing.T) {
	t.Parallel()
	a := Node{X: 5, Y: 10, Z: 15}
	d := Distance(a, a)
	if d != 0 {
		t.Errorf("Distance(same) = %f, want 0", d)
	}
}

func TestDistance_UnitAxis(t *testing.T) {
	t.Parallel()
	a := Node{X: 0, Y: 0, Z: 0}
	tests := []struct {
		name string
		b    Node
		want float32
	}{
		{"+X", Node{X: 1, Y: 0, Z: 0}, 1},
		{"+Y", Node{X: 0, Y: 1, Z: 0}, 1},
		{"+Z", Node{X: 0, Y: 0, Z: 1}, 1},
		{"diagonal XY", Node{X: 3, Y: 4, Z: 0}, 5},
		{"3D diagonal", Node{X: 1, Y: 2, Z: 2}, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Distance(a, tc.b)
			if math.Abs(float64(d-tc.want)) > 0.001 {
				t.Errorf("Distance = %f, want %f", d, tc.want)
			}
		})
	}
}

func TestDistance_Symmetric(t *testing.T) {
	t.Parallel()
	a := Node{X: 1, Y: 2, Z: 3}
	b := Node{X: 4, Y: 6, Z: 8}
	d1 := Distance(a, b)
	d2 := Distance(b, a)
	if math.Abs(float64(d1-d2)) > 0.001 {
		t.Errorf("Distance not symmetric: %f vs %f", d1, d2)
	}
}

func TestDistance_Negative(t *testing.T) {
	t.Parallel()
	a := Node{X: -3, Y: -4, Z: 0}
	b := Node{X: 0, Y: 0, Z: 0}
	d := Distance(a, b)
	if math.Abs(float64(d-5)) > 0.001 {
		t.Errorf("Distance with negatives = %f, want 5", d)
	}
}
