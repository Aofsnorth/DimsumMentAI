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
func RecalculatePath(b *bot.Bot) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

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

	b.Logger.Debug("recalculating path using A*",
		"start_x", start.X, "start_y", start.Y, "start_z", start.Z,
		"target_x", target.X, "target_y", target.Y, "target_z", target.Z,
		"movement_state", b.MovementState,
	)

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

	b.Logger.Info("A* Path Nodes block debug",
		"start_leg", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockLeg, startRID, b.WorldCache.IsRIDSolid(startRID)),
		"start_head", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockHead, startHeadRID, b.WorldCache.IsRIDSolid(startHeadRID)),
		"start_floor", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockFloor, startFloorRID, b.WorldCache.IsRIDSolid(startFloorRID)),
		"target_leg", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockLeg, targetRID, b.WorldCache.IsRIDSolid(targetRID)),
		"target_head", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockHead, targetHeadRID, b.WorldCache.IsRIDSolid(targetHeadRID)),
		"target_floor", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockFloor, targetFloorRID, b.WorldCache.IsRIDSolid(targetFloorRID)),
	)

	path := pathfinder.FindPath(start, target, b.WorldModel)
	if len(path) > 0 {
		b.CurrentPath = path
		if len(path) > 1 {
			b.PathIndex = 1
		} else {
			b.PathIndex = 0
		}
		b.TicksStuck = 0
		b.LastTickPos = b.Pos
		b.LastPathRecalcTime = time.Now()
		b.ConsecutiveStuckCount = 0
		nodeCoords := make([]string, len(path))
		for i, n := range path {
			nodeCoords[i] = fmt.Sprintf("(%d,%d,%d)", n.X, n.Y, n.Z)
		}
		b.Logger.Info("A* pathfinding completed", "nodes", len(path), "path", strings.Join(nodeCoords, " -> "), "movement_state", b.MovementState)
	} else {
		b.CurrentPath = nil
		b.LastPathRecalcTime = time.Now()
		b.Logger.Warn("A* pathfinding failed to resolve walkable path to destination",
			"start", start, "target", target, "movement_state", b.MovementState)
	}
}

func NavigateTo(b *bot.Bot, pos mgl32.Vec3) {
	b.WalkTo(pos)
}

func NavigateToBlock(b *bot.Bot, x, y, z int32, tolerance float32) bool {
	target := mgl32.Vec3{float32(x) + 0.5, float32(y), float32(z) + 0.5}
	b.WalkTo(target)

	for i := 0; i < 25; i++ {
		time.Sleep(200 * time.Millisecond)
		b.Mu.Lock()
		curPos := b.Pos
		mState := b.MovementState
		b.Mu.Unlock()

		dx := curPos.X() - target.X()
		dy := curPos.Y() - target.Y()
		dz := curPos.Z() - target.Z()
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
		if dist <= tolerance {
			return true
		}
		if mState == "idle" {
			break
		}
	}
	return false
}
