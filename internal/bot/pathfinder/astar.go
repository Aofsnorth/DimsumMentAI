package pathfinder

import (
	"container/heap"
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
	ux := uint64(x) & 0x1FFFFF
	uy := uint64(y) & 0xFFF
	uz := uint64(z) & 0x1FFFFF
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

	// Dynamic maxIterations based on distance to target
	distanceToTarget := Distance(startNode, targetNode)
	var maxIterations int32 = 10000
	if distanceToTarget < 20 {
		maxIterations = 5000
	} else if distanceToTarget < 50 {
		maxIterations = 15000
	} else {
		maxIterations = 30000
	}
	iterations := int32(0)

	var bestNode *Node = start
	var closestDistance float32 = Distance(*start, targetNode)

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

		neighbors := world.GetNeighbors(*current)
		for _, neighbor := range neighbors {
			nKey := packKey(neighbor.X, neighbor.Y, neighbor.Z)
			if closedMap[nKey] {
				continue
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
			} else if tentativeG < existing.G {
				existing.G = tentativeG
				existing.F = existing.G + existing.H
				existing.Parent = current
				existing.Action = neighbor.Action
				existing.LinkType = neighbor.LinkType
				heap.Fix(openSet, existing.Index)
			}
		}
	}

	// Fallback to the closest node reached if perfect destination is blocked
	if allowFallback && bestNode != start {
		return smoothPath(reconstructPath(bestNode), world)
	}

	return nil
}

func reconstructPath(endNode *Node) []Node {
	var path []Node
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
	// Only smooth walk-type segments; don't smooth jumps, falls, or scaffolding
	if from.Action != "" || to.Action != "" {
		return false
	}
	if from.LinkType != LinkWalk || to.LinkType != LinkWalk {
		return false
	}
	// Only smooth same-Y or gentle descent (1 block down)
	dy := to.Y - from.Y
	if dy > 1 || dy < -3 {
		return false
	}

	dx := to.X - from.X
	dz := to.Z - from.Z
	horizDist := abs32(dx)
	if abs32(dz) > horizDist {
		horizDist = abs32(dz)
	}
	if horizDist > 8 {
		return false // limit smoothing distance for safety
	}

	// Sample points along the line at 0.5-block intervals
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

		bx := int32(sx)
		bz := int32(sz)
		by := sy

		// Check feet + head clearance and floor support
		if world.IsSolid(bx, by, bz) || world.IsSolid(bx, by+1, bz) {
			return false
		}
		if world.IsHazard(bx, by, bz) || world.IsHazard(bx, by+1, bz) {
			return false
		}
		// Floor must be solid (or we're descending)
		if dy >= 0 && !world.IsSolid(bx, by-1, bz) {
			return false
		}
	}

	return true
}
