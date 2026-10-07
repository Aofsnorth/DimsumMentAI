package movement

import (
	"math"

	"bedrock-ai/internal/bot"

	"github.com/go-gl/mathgl/mgl32"
)

// pathLevelY converts a feet position into the Y level A* uses for it.
//
// A* models every solid block as a full cell, so a body standing on a bottom
// slab at cell c is "at level c+1" in the search. The physics, however, rests
// that body at c+0.5, and Floor(feet+0.1) reads c. Comparing a route node
// (c+1) against that raw floor made the body look permanently one level below
// its own path: every tick asked for a step-up, the jump fired, the body
// landed back on the slab and asked again — a bot hovering over a slab while
// going nowhere. A body resting on a partial block is reported at the level
// the planner gave it.
func pathLevelY(b *bot.Bot, pos mgl32.Vec3) int32 {
	level := int32(math.Floor(float64(pos.Y() + 0.1)))
	if b == nil || b.WorldModel == nil || b.WorldCache == nil {
		return level
	}
	x := int32(math.Floor(float64(pos.X())))
	z := int32(math.Floor(float64(pos.Z())))
	cell := int32(math.Floor(float64(pos.Y())))
	if !b.WorldModel.IsSolid(x, cell, z) {
		return level
	}
	top := b.WorldCache.TopOffsetAt(x, cell, z)
	if top >= 1.0 {
		return level
	}
	if pos.Y() >= float32(cell)+top-0.05 {
		return cell + 1
	}
	return level
}
