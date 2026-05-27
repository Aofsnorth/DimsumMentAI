package movement

import (
	"math"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/pathfinder"
	"github.com/go-gl/mathgl/mgl32"
)

// PathPreview is a read-only pathfinding result for the visualizer.
type PathPreview struct {
	Start        pathfinder.Node   `json:"start"`
	Target       pathfinder.Node   `json:"target"`
	Path         []pathfinder.Node `json:"path"`
	Found        bool              `json:"found"`
	UsedScaffold bool              `json:"used_scaffold"`
	UsedFallback bool              `json:"used_fallback"`
}

// PreviewPath runs A* from the bot's current position without changing movement state.
func PreviewPath(b *bot.Bot, dest mgl32.Vec3) PathPreview {
	b.Mu.Lock()
	start := pathfinder.Node{
		X: int32(math.Floor(float64(b.Pos.X()))),
		Y: int32(math.Floor(float64(b.Pos.Y() + 0.1))),
		Z: int32(math.Floor(float64(b.Pos.Z()))),
	}
	b.Mu.Unlock()

	target := pathfinder.Node{
		X: int32(math.Floor(float64(dest.X()))),
		Y: int32(math.Floor(float64(dest.Y()))),
		Z: int32(math.Floor(float64(dest.Z()))),
	}

	b.WorldModel.PurgeFalseSolidOverrides()

	if standTarget, ok := nearestStandableNode(b, start, target, 3); ok {
		target = standTarget
	}

	result := PathPreview{
		Start:  start,
		Target: target,
	}

	path := pathfinder.FindPath(start, target, b.WorldModel, false)
	if len(path) == 0 {
		b.WorldModel.AllowScaffold = true
		path = pathfinder.FindPath(start, target, b.WorldModel, false)
		b.WorldModel.AllowScaffold = false
		if len(path) > 0 {
			result.UsedScaffold = true
		}
	}
	if len(path) == 0 {
		path = pathfinder.FindPath(start, target, b.WorldModel, true)
		if len(path) > 0 {
			result.UsedFallback = true
		}
	}

	result.Path = path
	result.Found = len(path) > 0
	return result
}
