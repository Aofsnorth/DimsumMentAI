package pathfinder_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/bot/pathfinder"
)

func TestDistance_SameNode(t *testing.T) {
	t.Parallel()
	a := pathfinder.Node{X: 5, Y: 10, Z: 15}
	d := pathfinder.Distance(a, a)
	if d != 0 {
		t.Errorf("Distance(same) = %f, want 0", d)
	}
}

func TestDistance_UnitAxis(t *testing.T) {
	t.Parallel()
	a := pathfinder.Node{X: 0, Y: 0, Z: 0}
	tests := []struct {
		name string
		b    pathfinder.Node
		want float32
	}{
		{"+X", pathfinder.Node{X: 1, Y: 0, Z: 0}, 1},
		{"+Y", pathfinder.Node{X: 0, Y: 1, Z: 0}, 1},
		{"+Z", pathfinder.Node{X: 0, Y: 0, Z: 1}, 1},
		{"diagonal XY", pathfinder.Node{X: 3, Y: 4, Z: 0}, 5},
		{"3D diagonal", pathfinder.Node{X: 1, Y: 2, Z: 2}, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := pathfinder.Distance(a, tc.b)
			if math.Abs(float64(d-tc.want)) > 0.001 {
				t.Errorf("Distance = %f, want %f", d, tc.want)
			}
		})
	}
}

func TestDistance_Symmetric(t *testing.T) {
	t.Parallel()
	a := pathfinder.Node{X: 1, Y: 2, Z: 3}
	b := pathfinder.Node{X: 4, Y: 6, Z: 8}
	d1 := pathfinder.Distance(a, b)
	d2 := pathfinder.Distance(b, a)
	if math.Abs(float64(d1-d2)) > 0.001 {
		t.Errorf("Distance not symmetric: %f vs %f", d1, d2)
	}
}

func TestDistance_Negative(t *testing.T) {
	t.Parallel()
	a := pathfinder.Node{X: -3, Y: -4, Z: 0}
	b := pathfinder.Node{X: 0, Y: 0, Z: 0}
	d := pathfinder.Distance(a, b)
	if math.Abs(float64(d-5)) > 0.001 {
		t.Errorf("Distance with negatives = %f, want 5", d)
	}
}

func TestHeuristic_ZeroDistance(t *testing.T) {
	t.Parallel()
	a := pathfinder.Node{X: 5, Y: 10, Z: 15}
	h := pathfinder.Heuristic(a, a)
	if h != 0 {
		t.Errorf("pathfinder.Heuristic(same) = %f, want 0", h)
	}
}

func TestHeuristic_Cardinal(t *testing.T) {
	t.Parallel()
	a := pathfinder.Node{X: 0, Y: 0, Z: 0}
	b := pathfinder.Node{X: 5, Y: 0, Z: 0}
	h := pathfinder.Heuristic(a, b)
	// 5 cardinal steps * D(1.0) * weight(1.4) = 7.0
	if math.Abs(float64(h-7.0)) > 0.01 {
		t.Errorf("pathfinder.Heuristic(cardinal 5) = %f, want ~7.0", h)
	}
}

func TestHeuristic_Diagonal(t *testing.T) {
	t.Parallel()
	a := pathfinder.Node{X: 0, Y: 0, Z: 0}
	b := pathfinder.Node{X: 3, Y: 0, Z: 3}
	h := pathfinder.Heuristic(a, b)
	// Octile: D*(3+3) + (D2-2D)*3 = 6 + (1.414-2)*3 = 6 - 1.758 = 4.242 * 1.4 = 5.939
	if h <= 0 {
		t.Errorf("pathfinder.Heuristic(diagonal) = %f, want > 0", h)
	}
}

func TestHeuristic_NeverNegative(t *testing.T) {
	t.Parallel()
	a := pathfinder.Node{X: -5, Y: -10, Z: -15}
	b := pathfinder.Node{X: 5, Y: 10, Z: 15}
	h := pathfinder.Heuristic(a, b)
	if h < 0 {
		t.Errorf("heuristic = %f, should never be negative", h)
	}
}
