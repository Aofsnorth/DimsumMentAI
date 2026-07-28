package pathfinder

import (
	"container/heap"
	"fmt"
)

type PriorityQueue []*Node

func (pq PriorityQueue) Len() int           { return len(pq) }
func (pq PriorityQueue) Less(i, j int) bool { return pq[i].F < pq[j].F }
func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].Index = i
	pq[j].Index = j
}
func (pq *PriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*Node)
	item.Index = n
	*pq = append(*pq, item)
}
func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.Index = -1
	*pq = old[0 : n-1]
	return item
}

// packKey encodes a 3D block coordinate into a single int64 for use as a
// map key. This is ~10x faster than fmt.Sprintf-based string keys and
// eliminates GC pressure from string allocations during pathfinding.
// Coordinate range: x/z ±2,097,151 (21 bits), y -2048..+2047 (12 bits).
func packKey(x, y, z int32) int64 {
	// Bit-cast int32→uint64 via uint32 (two's complement) instead of
	// safecast.To: negative coordinates are the norm around spawn, and
	// safecast clamps them all to 0, collapsing every negative-x/z node
	// into the same key and making A* think all neighbors are duplicates.
	ux := uint64(uint32(x)) & 0x1FFFFF
	uy := uint64(uint32(y)) & 0xFFF
	uz := uint64(uint32(z)) & 0x1FFFFF
	return int64(ux<<33 | uy<<21 | uz)
}

// FindPath executes the A* algorithm in 3D grid space using the provided world walkability rules
func FindPath(startNode, targetNode Node, world WorldModel, allowFallback bool) []Node {
	type boundableWorld interface {
		SetPathBounds(start, target Node)
	}
	if bw, ok := world.(boundableWorld); ok {
		bw.SetPathBounds(startNode, targetNode)
	}

	openSet := &PriorityQueue{}
	heap.Init(openSet)

	openMap := make(map[int64]*Node)
	closedMap := make(map[int64]bool)

	start := &Node{
		X: startNode.X,
		Y: startNode.Y,
		Z: startNode.Z,
		G: 0,
		H: heuristic(startNode, targetNode),
	}
	start.F = start.G + start.H

	heap.Push(openSet, start)
	startKey := packKey(start.X, start.Y, start.Z)
	openMap[startKey] = start

	maxIterations := maxIterationsForDistance(Distance(startNode, targetNode))
	iterations := int32(0)

	bestNode := start
	closestDistance := Distance(*start, targetNode)

	for openSet.Len() > 0 && iterations < maxIterations {
		iterations++
		current := heap.Pop(openSet).(*Node)
		currentKey := packKey(current.X, current.Y, current.Z)
		delete(openMap, currentKey)
		closedMap[currentKey] = true

		if isTargetReached(current, targetNode) {
			path := reconstructPath(current)
			if !current.Equal(&targetNode) {
				path = append(path, targetNode)
			}
			return smoothPath(path, world)
		}

		dist := Distance(*current, targetNode)
		if dist < closestDistance {
			closestDistance = dist
			bestNode = current
		}

		for _, neighbor := range world.GetNeighbors(*current) {
			tryProcessNeighbor(openSet, openMap, closedMap, current, neighbor, targetNode)
		}
	}

	// Diagnostic: A* exhausted without reaching target. Report iterations,
	// open set residue, and best-node distance so we can see whether the
	// search space exploded or neighbors are being vetoed wholesale.
	fmt.Printf("[A* exhausted] iterations=%d maxIterations=%d openSetLen=%d closedSetLen=%d bestNodeDist=%.2f allowFallback=%v\n",
		iterations, maxIterations, openSet.Len(), len(closedMap), closestDistance, allowFallback)
	if iterations <= 2 {
		// Start had no neighbors — dump WHY. Probe each cardinal directly
		// through the same predicates GetNeighbors uses.
		if w, ok := world.(interface {
			DebugNeighborVeto(n Node) string
		}); ok {
			fmt.Printf("[A* neighbor veto] %s\n", w.DebugNeighborVeto(startNode))
		}
	}

	if allowFallback && bestNode != start {
		return smoothPath(reconstructPath(bestNode), world)
	}

	return nil
}

func maxIterationsForDistance(distance float32) int32 {
	switch {
	case distance < 20:
		return 5000
	case distance < 50:
		return 15000
	default:
		return 30000
	}
}

func tryProcessNeighbor(openSet *PriorityQueue, openMap map[int64]*Node, closedMap map[int64]bool, current *Node, neighbor Node, targetNode Node) {
	nKey := packKey(neighbor.X, neighbor.Y, neighbor.Z)
	if closedMap[nKey] {
		return
	}

	tentativeG := neighbor.G
	if tentativeG == 0 {
		tentativeG = current.G + Distance(*current, neighbor)
	}

	existing, inOpen := openMap[nKey]
	if !inOpen {
		newNode := &Node{
			X:        neighbor.X,
			Y:        neighbor.Y,
			Z:        neighbor.Z,
			G:        tentativeG,
			H:        heuristic(neighbor, targetNode),
			Parent:   current,
			Action:   neighbor.Action,
			LinkType: neighbor.LinkType,
		}
		newNode.F = newNode.G + newNode.H
		heap.Push(openSet, newNode)
		openMap[nKey] = newNode
		return
	}

	if tentativeG < existing.G {
		existing.G = tentativeG
		existing.F = existing.G + existing.H
		existing.Parent = current
		existing.Action = neighbor.Action
		existing.LinkType = neighbor.LinkType
		heap.Fix(openSet, existing.Index)
	}
}

func reconstructPath(endNode *Node) []Node {
	path := make([]Node, 0, 64)
	curr := endNode
	for curr != nil {
		path = append(path, *curr)
		curr = curr.Parent
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func isTargetReached(current *Node, target Node) bool {
	if current.X == target.X && current.Y == target.Y && current.Z == target.Z {
		return true
	}
	dx := abs32(current.X - target.X)
	dy := abs32(current.Y - target.Y)
	dz := abs32(current.Z - target.Z)
	return dx <= 1 && dz <= 1 && dy <= 1
}

func abs32(val int32) int32 {
	if val < 0 {
		return -val
	}
	return val
}

// smoothPath applies string-pulling to remove unnecessary intermediate
// waypoints. For each node, it checks if the bot can walk directly from
// the node before it to the node after it (skipping the middle node).
// This eliminates zigzag patterns common in grid-based A* paths and
// produces smoother, more natural movement.
func smoothPath(path []Node, world WorldModel) []Node {
	if len(path) <= 2 {
		return path
	}

	smoothed := []Node{path[0]}
	i := 0
	for i < len(path)-2 {
		// Try to skip as many intermediate nodes as possible
		j := len(path) - 1
		for j > i+1 {
			if canWalkDirectly(path[i], path[j], world) {
				break
			}
			j--
		}
		if j > i+1 {
			smoothed = append(smoothed, path[j])
			i = j
		} else {
			smoothed = append(smoothed, path[i+1])
			i++
		}
	}
	// Always include the final node if not already included
	if smoothed[len(smoothed)-1] != path[len(path)-1] {
		smoothed = append(smoothed, path[len(path)-1])
	}

	return smoothed
}

// canWalkDirectly checks if the bot can walk in a straight line between
// two path nodes without hitting solid blocks. It samples intermediate
// positions and verifies floor + head clearance at each step.
func canWalkDirectly(from, to Node, world WorldModel) bool {
	if !canSmoothLink(from, to) {
		return false
	}
	dx := to.X - from.X
	dz := to.Z - from.Z
	horizDist := max(abs32(dx), abs32(dz))
	if horizDist > 8 {
		return false
	}
	return canWalkLine(from, to, dx, dz, horizDist, world)
}

func canSmoothLink(from, to Node) bool {
	if from.Action != "" || to.Action != "" {
		return false
	}
	if from.LinkType != LinkWalk || to.LinkType != LinkWalk {
		return false
	}
	dy := to.Y - from.Y
	return dy <= 1 && dy >= -3
}

func canWalkLine(from, to Node, dx, dz, horizDist int32, world WorldModel) bool {
	dy := to.Y - from.Y
	steps := horizDist * 2
	if steps < 2 {
		steps = 2
	}
	for s := int32(1); s < steps; s++ {
		t := float32(s) / float32(steps)
		sx := float32(from.X) + 0.5 + float32(dx)*t
		sz := float32(from.Z) + 0.5 + float32(dz)*t
		sy := from.Y
		if dy != 0 {
			sy = from.Y + int32(float32(dy)*t)
		}

		if !canWalkAtSample(int32(sx), int32(sz), sy, dy, world) {
			return false
		}
	}
	return true
}

func canWalkAtSample(bx, bz, by, dy int32, world WorldModel) bool {
	if world.IsSolid(bx, by, bz) || world.IsSolid(bx, by+1, bz) {
		return false
	}
	if world.IsHazard(bx, by, bz) || world.IsHazard(bx, by+1, bz) {
		return false
	}
	if dy >= 0 && !world.IsSolid(bx, by-1, bz) {
		return false
	}
	return true
}
