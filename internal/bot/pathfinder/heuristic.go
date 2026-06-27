package pathfinder

import "math"

// Distance calculates the Euclidean distance between two Nodes
func Distance(a, b Node) float32 {
	dx := float64(a.X - b.X)
	dy := float64(a.Y - b.Y)
	dz := float64(a.Z - b.Z)
	return float32(math.Sqrt(dx*dx + dy*dy + dz*dz))
}

// heuristic estimates the cost-to-go using octile distance, which is the
// tightest admissible heuristic for grid-based movement with diagonal
// (cost 1.414) and cardinal (cost 1.0) steps. A weighted factor (1.4)
// makes the search greedier — significantly faster while still producing
// near-optimal paths. The weight is safe because any overestimate is
// bounded by the weight factor, and the fallback path still works.
func heuristic(a, b Node) float32 {
	dx := abs32(a.X - b.X)
	dy := abs32(a.Y - b.Y)
	dz := abs32(a.Z - b.Z)
	// Octile: D * (dx + dz + dy) + (D2 - 2*D) * min(dx, dz)
	// where D=1.0 (cardinal cost), D2=1.414 (diagonal cost)
	const D = float32(1.0)
	const D2 = float32(1.414)
	const weight = float32(1.4) // weighted A* — greedier = faster

	horizMin := dx
	if dz < horizMin {
		horizMin = dz
	}
	horizMax := dx
	if dz > horizMax {
		horizMax = dz
	}
	// Octile horizontal + linear vertical (climbing is linear cost)
	h := D*(float32(horizMax)+float32(horizMin)+float32(dy)) + (D2-2*D)*float32(horizMin)
	return h * weight
}
