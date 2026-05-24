package pathfinder

import "math"

// Distance calculates the Euclidean distance between two Nodes
func Distance(a, b Node) float32 {
	dx := float64(a.X - b.X)
	dy := float64(a.Y - b.Y)
	dz := float64(a.Z - b.Z)
	return float32(math.Sqrt(dx*dx + dy*dy + dz*dz))
}

// HeuristicCost calculates the heuristic cost for A* with diagonal movement support
// Uses Chebyshev distance for 8-directional movement (allows diagonals)
func HeuristicCost(a, b Node) float32 {
	dx := int32Abs(a.X - b.X)
	dy := int32Abs(a.Y - b.Y)
	dz := int32Abs(a.Z - b.Z)
	
	// For 3D with 8 directions in XZ plane:
	// Diagonal movement in XZ plane costs sqrt(2) ≈ 1.414
	// Vertical movement costs 1.0
	
	xzDiag := dx
	if dz > dx {
		xzDiag = dz
	}
	xzStraight := dx + dz - xzDiag
	
	// Cost = diagonal_steps * sqrt(2) + straight_steps * 1.0 + vertical_steps * 1.0
	return float32(xzDiag)*1.414 + float32(xzStraight) + float32(dy)
}

func int32Abs(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
