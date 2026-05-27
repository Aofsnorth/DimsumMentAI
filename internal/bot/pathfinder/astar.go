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

	openMap := make(map[string]*Node)
	closedMap := make(map[string]bool)

	start := &Node{
		X: startNode.X,
		Y: startNode.Y,
		Z: startNode.Z,
		G: 0,
		H: Distance(startNode, targetNode),
	}
	start.F = start.G + start.H

	heap.Push(openSet, start)
	openMap[key(start.X, start.Y, start.Z)] = start

	// Dynamic maxIterations based on distance to target
	// Go is extremely fast, so we can afford much higher iterations for complex environments
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
		currentKey := key(current.X, current.Y, current.Z)
		delete(openMap, currentKey)
		closedMap[currentKey] = true

		if isTargetReached(current, targetNode) {
			// If we reached adjacent node, make sure to add targetNode to path if it is passable
			path := reconstructPath(current)
			if !current.Equal(&targetNode) {
				path = append(path, targetNode)
			}
			return path
		}

		dist := Distance(*current, targetNode)
		if dist < closestDistance {
			closestDistance = dist
			bestNode = current
		}

		neighbors := world.GetNeighbors(*current)
		for _, neighbor := range neighbors {
			nKey := key(neighbor.X, neighbor.Y, neighbor.Z)
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
					H:        Distance(neighbor, targetNode),
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
		return reconstructPath(bestNode)
	}

	return nil
}

func key(x, y, z int32) string {
	return fmt.Sprintf("%d,%d,%d", x, y, z)
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
