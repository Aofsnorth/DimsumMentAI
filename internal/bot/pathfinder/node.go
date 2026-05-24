package pathfinder

type Node struct {
	X, Y, Z int32
	G, H, F float32
	Parent  *Node
	Index   int // index in the priority queue
}

func (n *Node) Equal(other *Node) bool {
	if other == nil {
		return false
	}
	return n.X == other.X && n.Y == other.Y && n.Z == other.Z
}
