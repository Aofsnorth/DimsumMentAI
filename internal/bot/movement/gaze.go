// Package movement implements tick-level bot movement, path following, and look
// direction. It is responsible for steering, physics, collision resolution, and
// the PlayerAuthInput heartbeat sent to the server.
package movement

import (
	"math"

	"bedrock-ai/internal/bot"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// walkingGazeLookAhead is how far down the route the head aims while walking.
	//
	// A real player does not stare at the block under their own feet: they look
	// several metres ahead along the path they are taking. Aiming at the single
	// next waypoint instead — which is one block away — produced two artefacts
	// that read as robotic: the direction to that block swung with the bot's
	// sub-block position (pure parallax chatter), and it jumped discontinuously
	// every time PathIndex advanced. Looking ahead along the polyline removes
	// both, and it is also what makes the head start turning *into* a corner
	// before the body reaches it, the way a human head does.
	walkingGazeLookAhead float32 = 4.0

	// walkingGazeMaxLeadYaw caps how far the look-ahead aim may diverge from the
	// direction the body is actually travelling. Without it, a route that turns
	// sharply just ahead would swing the view onto a wall the bot is not walking
	// toward, which reads as a glitch rather than as anticipation.
	walkingGazeMaxLeadYaw float32 = 30.0

	// walkingGazeMaxPitch bounds the walking pitch, so that a cliff or a tall
	// staircase inside the look-ahead window tilts the view like a player
	// glancing at the terrain instead of whipping it to the horizon.
	walkingGazeMaxPitch float32 = 32.0

	// walkingGazeMinHorizontal is the shortest useful aim distance. Below it the
	// angle is dominated by numerical noise from the bot's own position.
	walkingGazeMinHorizontal float32 = 0.05
)

// walkingGazePoint returns the world point the head should aim at: a spot
// walkingGazeLookAhead metres further along the current route, lifted to eye
// height. ok is false when there is no route to look along, in which case the
// caller keeps the plain movement direction.
func (tc *TickContext) walkingGazePoint() (mgl32.Vec3, bool) {
	if tc.B == nil || !tc.HasPath {
		return mgl32.Vec3{}, false
	}

	tc.B.Mu.Lock()
	path := tc.B.CurrentPath
	idx := tc.B.PathIndex
	tc.B.Mu.Unlock()

	if idx < 0 || idx >= len(path) {
		return mgl32.Vec3{}, false
	}

	eyeHeight := float32(bot.PlayerEyeHeight)
	cursor := tc.CurrPos
	remaining := walkingGazeLookAhead

	for i := idx; i < len(path); i++ {
		node := mgl32.Vec3{
			float32(path[i].X) + 0.5,
			float32(path[i].Y) + eyeHeight,
			float32(path[i].Z) + 0.5,
		}
		segment := float32(math.Hypot(float64(node.X()-cursor.X()), float64(node.Z()-cursor.Z())))

		// Land on the last node even when the whole remaining route is shorter
		// than the look-ahead budget: the player is heading for that spot, so
		// that is what they look at.
		if segment >= remaining || i == len(path)-1 {
			t := float32(1)
			if segment > 1e-3 {
				t = remaining / segment
			}
			return mgl32.Vec3{
				cursor.X() + (node.X()-cursor.X())*t,
				node.Y(),
				cursor.Z() + (node.Z()-cursor.Z())*t,
			}, true
		}

		remaining -= segment
		cursor = node
	}

	return cursor, true
}

// walkingGazeAngles converts the look-ahead point into yaw/pitch for the head.
//
// The pitch comes out of the route geometry rather than being pinned to level:
// climbing a staircase lifts the aim and the head tips up, walking off a ledge
// drops it and the head tips down. On flat ground the aim sits at eye height so
// the level the existing walking gaze bias adds is the whole vertical motion.
func (tc *TickContext) walkingGazeAngles() (yaw, pitch float32, ok bool) {
	point, ok := tc.walkingGazePoint()
	if !ok {
		return 0, 0, false
	}

	dx := point.X() - tc.CurrPos.X()
	dy := point.Y() - (tc.CurrPos.Y() + bot.PlayerEyeHeight)
	dz := point.Z() - tc.CurrPos.Z()

	horizontal := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if horizontal < walkingGazeMinHorizontal {
		return 0, 0, false
	}

	yaw = normalizeYaw(float32(math.Atan2(float64(dz), float64(dx))*180/math.Pi) - 90)
	pitch = clampFloat32(
		float32(-math.Atan2(float64(dy), float64(horizontal))*180/math.Pi),
		-walkingGazeMaxPitch, walkingGazeMaxPitch,
	)
	return clampLeadYaw(yaw, tc.TargetYaw), pitch, true
}

// clampLeadYaw limits how far the head aim may lead the direction of travel.
func clampLeadYaw(yaw, moveYaw float32) float32 {
	switch lead := angleDifference(yaw, moveYaw); {
	case lead > walkingGazeMaxLeadYaw:
		return normalizeYaw(moveYaw + walkingGazeMaxLeadYaw)
	case lead < -walkingGazeMaxLeadYaw:
		return normalizeYaw(moveYaw - walkingGazeMaxLeadYaw)
	default:
		return yaw
	}
}
