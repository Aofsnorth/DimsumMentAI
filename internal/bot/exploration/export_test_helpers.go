package exploration

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
