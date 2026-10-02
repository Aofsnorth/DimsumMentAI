package recipe_test

import "github.com/go-gl/mathgl/mgl32"

// mgl32Zero is the bot's feet position in the tests: standing on the origin, so
// a block seeded at (0,0,0) is adjacent and in reach.
func mgl32Zero() mgl32.Vec3 {
	return mgl32.Vec3{0.5, 0, 0.5}
}
