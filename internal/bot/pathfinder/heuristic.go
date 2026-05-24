package pathfinder

import "math"

// Distance calculates the Euclidean distance between two Nodes
func Distance(a, b Node) float32 {
	dx := float64(a.X - b.X)
	dy := float64(a.Y - b.Y)
	dz := float64(a.Z - b.Z)
	return float32(math.Sqrt(dx*dx + dy*dy + dz*dz))
}
