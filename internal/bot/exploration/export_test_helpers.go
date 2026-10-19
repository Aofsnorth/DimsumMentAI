package exploration

import "github.com/go-gl/mathgl/mgl32"

// Test-support accessors.
//
// Whether an exploration run is in progress is guarded by the explorer's mutex
// and set by the Explore* methods. A test that wants to exercise the waypoint
// walk needs it armed without running a whole exploration loop, and it cannot
// reach the field from outside the package. This is that way in.

// SetExploring arms or clears the in-progress flag directly.
func (e *Explorer) SetExploring(on bool) {
	e.mu.Lock()
	e.isExploring = on
	e.mu.Unlock()
}

// AreaKeyForTest exposes the chunk-level visited key.
//
// The key used to be string(rune(cx))+","+string(rune(cz)), which maps every
// negative coordinate to U+FFFD — so the entire west/north quadrant collapsed
// into one "already visited" chunk and the explorer skipped it.
func AreaKeyForTest(pos mgl32.Vec3) string {
	return areaKey(pos)
}
