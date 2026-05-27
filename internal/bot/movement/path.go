package movement

import (
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/pathfinder"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/go-gl/mathgl/mgl32"
)

// RecalculatePath computes the shortest path to targetPos using A* search.
// A* runs without holding b.Mu so SendInputLoop is not blocked for the whole search.
func RecalculatePath(b *bot.Bot) {
	b.Mu.Lock()
	start := pathfinder.Node{
		X: int32(math.Floor(float64(b.Pos.X()))),
		Y: int32(math.Floor(float64(b.Pos.Y() + 0.1))),
		Z: int32(math.Floor(float64(b.Pos.Z()))),
	}
	targetY := b.TargetPos.Y()
	target := pathfinder.Node{
		X: int32(math.Floor(float64(b.TargetPos.X()))),
		Y: int32(math.Floor(float64(targetY))),
		Z: int32(math.Floor(float64(b.TargetPos.Z()))),
	}
	movementState := b.MovementState
	lastTickPos := b.Pos
	b.Mu.Unlock()

	b.Logger.Debug("recalculating path using A*",
		"start_x", start.X, "start_y", start.Y, "start_z", start.Z,
		"target_x", target.X, "target_y", target.Y, "target_z", target.Z,
		"movement_state", movementState,
	)

	if standTarget, ok := nearestStandableNode(b, start, target, 2); ok {
		target = standTarget
	}

	startRID, _ := b.WorldCache.GetBlockRID(start.X, start.Y, start.Z)
	startHeadRID, _ := b.WorldCache.GetBlockRID(start.X, start.Y+1, start.Z)
	startFloorRID, _ := b.WorldCache.GetBlockRID(start.X, start.Y-1, start.Z)
	targetRID, _ := b.WorldCache.GetBlockRID(target.X, target.Y, target.Z)
	targetHeadRID, _ := b.WorldCache.GetBlockRID(target.X, target.Y+1, target.Z)
	targetFloorRID, _ := b.WorldCache.GetBlockRID(target.X, target.Y-1, target.Z)

	startBlockLeg, _, _ := chunk.RuntimeIDToState(startRID)
	startBlockHead, _, _ := chunk.RuntimeIDToState(startHeadRID)
	startBlockFloor, _, _ := chunk.RuntimeIDToState(startFloorRID)
	targetBlockLeg, _, _ := chunk.RuntimeIDToState(targetRID)
	targetBlockHead, _, _ := chunk.RuntimeIDToState(targetHeadRID)
	targetBlockFloor, _, _ := chunk.RuntimeIDToState(targetFloorRID)

	b.Logger.Debug("A* Path Nodes block debug",
		"start_leg", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockLeg, startRID, b.WorldCache.IsRIDSolid(startRID)),
		"start_head", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockHead, startHeadRID, b.WorldCache.IsRIDSolid(startHeadRID)),
		"start_floor", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockFloor, startFloorRID, b.WorldCache.IsRIDSolid(startFloorRID)),
		"target_leg", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockLeg, targetRID, b.WorldCache.IsRIDSolid(targetRID)),
		"target_head", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockHead, targetHeadRID, b.WorldCache.IsRIDSolid(targetHeadRID)),
		"target_floor", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockFloor, targetFloorRID, b.WorldCache.IsRIDSolid(targetFloorRID)),
	)

	path := pathfinder.FindPath(start, target, b.WorldModel)
	if len(path) == 0 {
		b.WorldModel.AllowScaffold = true
		b.Logger.Info("Normal pathfinding failed, retrying with scaffolding and mining allowed...")
		path = pathfinder.FindPath(start, target, b.WorldModel)
		b.WorldModel.AllowScaffold = false
	}

	b.Mu.Lock()
	defer b.Mu.Unlock()
	if len(path) > 0 {
		b.CurrentPath = path
		if len(path) > 1 {
			b.PathIndex = 1
		} else {
			b.PathIndex = 0
		}
		b.TicksStuck = 0
		b.LastTickPos = lastTickPos
		b.LastPathRecalcTime = time.Now()
		b.ConsecutiveStuckCount = 0
		nodeCoords := make([]string, len(path))
		for i, n := range path {
			nodeCoords[i] = fmt.Sprintf("(%d,%d,%d)", n.X, n.Y, n.Z)
		}
		b.Logger.Debug("A* pathfinding completed", "nodes", len(path), "path", strings.Join(nodeCoords, " -> "), "movement_state", movementState)
	} else {
		b.CurrentPath = nil
		b.LastPathRecalcTime = time.Now()
		b.Logger.Warn("A* pathfinding failed to resolve walkable path to destination",
			"start", start, "target", target, "movement_state", movementState)
	}
}

func NavigateTo(b *bot.Bot, pos mgl32.Vec3) {
	b.WalkTo(pos)
}

func NavigateToBlock(b *bot.Bot, x, y, z int32, tolerance float32) bool {
	block := mgl32.Vec3{float32(x) + 0.5, float32(y), float32(z) + 0.5}
	target := block
	start := pathfinder.Node{
		X: int32(math.Floor(float64(b.GetCoords().X()))),
		Y: int32(math.Floor(float64(b.GetCoords().Y() + 0.1))),
		Z: int32(math.Floor(float64(b.GetCoords().Z()))),
	}
	if standTarget, ok := nearestStandableNode(b, start, pathfinder.Node{X: x, Y: y, Z: z}, 3); ok {
		target = mgl32.Vec3{float32(standTarget.X) + 0.5, float32(standTarget.Y), float32(standTarget.Z) + 0.5}
	}
	b.WalkTo(target)

	for i := 0; i < 25; i++ {
		time.Sleep(200 * time.Millisecond)
		b.Mu.Lock()
		curPos := b.Pos
		mState := b.MovementState
		hasPath := b.CurrentPath != nil
		b.Mu.Unlock()

		dx := curPos.X() - block.X()
		dy := curPos.Y() - block.Y()
		dz := curPos.Z() - block.Z()
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
		if dist <= tolerance {
			return true
		}
		if mState == "idle" || (mState == "walk_to" && !hasPath) {
			break
		}
	}
	return false
}

func nearestStandableNode(b *bot.Bot, start, target pathfinder.Node, radius int32) (pathfinder.Node, bool) {
	if isStandable(b, target.X, target.Y, target.Z) {
		return target, true
	}

	best := pathfinder.Node{}
	bestScore := float32(math.MaxFloat32)
	for r := int32(1); r <= radius; r++ {
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if abs32(dx) != r && abs32(dz) != r {
					continue
				}
				for dy := int32(-1); dy <= 2; dy++ {
					candidate := pathfinder.Node{X: target.X + dx, Y: target.Y + dy, Z: target.Z + dz}
					if !isStandable(b, candidate.X, candidate.Y, candidate.Z) {
						continue
					}
					score := pathfinder.Distance(start, candidate) + pathfinder.Distance(candidate, target)*0.25
					if score < bestScore {
						bestScore = score
						best = candidate
					}
				}
			}
		}
		if bestScore < float32(math.MaxFloat32) {
			return best, true
		}
	}
	return pathfinder.Node{}, false
}

func isStandable(b *bot.Bot, x, y, z int32) bool {
	return !b.WorldModel.IsSolid(x, y, z) &&
		!b.WorldModel.IsSolid(x, y+1, z) &&
		!b.WorldModel.IsHazard(x, y, z) &&
		!b.WorldModel.IsHazard(x, y+1, z) &&
		b.WorldModel.IsSolid(x, y-1, z) &&
		!b.WorldModel.IsHazard(x, y-1, z)
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
