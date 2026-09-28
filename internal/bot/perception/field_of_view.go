// Perception reports what the bot can see. This file is the bridge from the
// bot's own head/eye state to the shared vision cone in the fov package.
//
// The cone itself lives in internal/bot/fov because two packages need it —
// this one, and the storage package (via the bot type) — and neither can import
// the other without a cycle. All the reasoning about *why* the cone has the
// shape it has lives with the geometry, in fov.go.
package perception

import (
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/fov"

	"github.com/go-gl/mathgl/mgl32"
)

// InFieldOfView reports whether a world point is inside the bot's vision cone.
//
// The cone follows the head, not the body. The head is decoupled from the torso
// while turning, so a player can glance at something beside them without the
// body rotating; using the head is what keeps the bot from failing to see
// something it is visibly looking at.
func InFieldOfView(b *bot.Bot, point mgl32.Vec3) bool {
	origin := b.GetCoords()
	eye := origin.Add(mgl32.Vec3{0, bot.PlayerEyeHeight, 0})
	return fov.Within(point, eye, HeadYaw(b))
}

// HeadYaw returns the yaw the bot's head is actually pointing along.
func HeadYaw(b *bot.Bot) float32 {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.HeadYaw
}
