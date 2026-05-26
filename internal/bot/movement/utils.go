package movement

import (
	"math"

	"bedrock-ai/internal/bot"
	"github.com/go-gl/mathgl/mgl32"
)

// InterpolateAngle smoothly moves current angle towards target by at most maxStep, handling 360 wrap-around
func InterpolateAngle(current, target, maxStep float32) float32 {
	diff := target - current
	for diff < -180 {
		diff += 360
	}
	for diff > 180 {
		diff -= 360
	}
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	res := current + diff
	for res < 0 {
		res += 360
	}
	for res >= 360 {
		res -= 360
	}
	return res
}

// InterpolatePitch smoothly moves current pitch towards target by at most maxStep
func InterpolatePitch(current, target, maxStep float32) float32 {
	diff := target - current
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	res := current + diff
	if res > 90 {
		res = 90
	} else if res < -90 {
		res = -90
	}
	return res
}

func StopMovement(b *bot.Bot) {
	b.Stop()
}

func LookAt(b *bot.Bot, pos mgl32.Vec3) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	dx := pos.X() - b.Pos.X()
	dy := pos.Y() - b.Pos.Y()
	dz := pos.Z() - b.Pos.Z()

	distH := math.Sqrt(float64(dx*dx + dz*dz))
	if distH < 0.001 {
		distH = 0.001
	}

	yawRad := math.Atan2(float64(dz), float64(dx))
	yawDeg := yawRad * 180 / math.Pi
	b.Yaw = float32(yawDeg) - 90
	if b.Yaw < 0 {
		b.Yaw += 360
	}

	pitchRad := -math.Atan2(float64(dy), distH)
	b.Pitch = float32(pitchRad * 180 / math.Pi)
}
