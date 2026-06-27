package pathfinder

import "testing"

// mockWorld is a minimal WorldModel implementation for deterministic A*
// testing. It uses a simple solid-map so tests can construct any terrain
// layout without touching the real chunk cache.
type mockWorld struct {
	solid    map[[3]int32]bool
	hazard   map[[3]int32]bool
	ladder   map[[3]int32]bool
	boundsOK bool
}

func newMockWorld() *mockWorld {
	return &mockWorld{
		solid:  make(map[[3]int32]bool),
		hazard: make(map[[3]int32]bool),
		ladder: make(map[[3]int32]bool),
	}
}

func (m *mockWorld) setSolid(x, y, z int32)      { m.solid[[3]int32{x, y, z}] = true }
func (m *mockWorld) setHazard(x, y, z int32)     { m.hazard[[3]int32{x, y, z}] = true }
func (m *mockWorld) setLadder(x, y, z int32)     { m.ladder[[3]int32{x, y, z}] = true }
func (m *mockWorld) IsSolid(x, y, z int32) bool  { return m.solid[[3]int32{x, y, z}] }
func (m *mockWorld) IsHazard(x, y, z int32) bool { return m.hazard[[3]int32{x, y, z}] }
func (m *mockWorld) IsLadder(x, y, z int32) bool { return m.ladder[[3]int32{x, y, z}] }
func (m *mockWorld) SetPathBounds(start, target Node) {
	m.boundsOK = true
}

// GetNeighbors produces walkable cardinal neighbors for flat-floor layouts.
// This is a simplified neighbor generator sufficient for deterministic A*
// path tests — it does not implement jumps, drops, or parkour.
func (m *mockWorld) GetNeighbors(node Node) []Node {
	var neighbors []Node
	cardinal := []struct{ dx, dz int32 }{
		{0, -1}, {0, 1}, {1, 0}, {-1, 0},
	}
	for _, off := range cardinal {
		tx, tz := node.X+off.dx, node.Z+off.dz
		// Can stand at (tx, node.Y, tz) if feet+head clear and floor solid.
		if !m.IsSolid(tx, node.Y, tz) && !m.IsSolid(tx, node.Y+1, tz) &&
			!m.IsHazard(tx, node.Y, tz) && !m.IsHazard(tx, node.Y+1, tz) &&
			m.IsSolid(tx, node.Y-1, tz) && !m.IsHazard(tx, node.Y-1, tz) {
			neighbors = append(neighbors, Node{
				X: tx, Y: node.Y, Z: tz,
				G: node.G + 1.0, LinkType: LinkWalk,
			})
		}
	}
	return neighbors
}

// --- Tests ---

func TestFindPath_DirectAdjacent(t *testing.T) {
	t.Parallel()
	w := newMockWorld()
	// Build a flat floor at y=0 so the bot can stand at y=1 on top of it.
	for x := int32(-2); x <= 2; x++ {
		for z := int32(-2); z <= 2; z++ {
			w.setSolid(x, 0, z)
		}
	}

	start := Node{X: 0, Y: 1, Z: 0}
	target := Node{X: 1, Y: 1, Z: 0}

	path := FindPath(start, target, w, false)
	if len(path) == 0 {
		t.Fatal("FindPath returned empty path for adjacent target")
	}
	// Path should start at start and end at or near target.
	if path[0].X != start.X || path[0].Y != start.Y || path[0].Z != start.Z {
		t.Errorf("path start = %+v, want %+v", path[0], start)
	}
	last := path[len(path)-1]
	if !isTargetReached(&last, target) {
		t.Errorf("path end = %+v, should reach target %+v", last, target)
	}
}

func TestFindPath_StraightLine(t *testing.T) {
	t.Parallel()
	w := newMockWorld()
	// Long corridor floor.
	for x := int32(0); x <= 10; x++ {
		w.setSolid(x, 0, 0)
	}

	start := Node{X: 0, Y: 1, Z: 0}
	target := Node{X: 5, Y: 1, Z: 0}

	path := FindPath(start, target, w, false)
	if len(path) == 0 {
		t.Fatal("FindPath returned empty path for straight line")
	}
	last := path[len(path)-1]
	if last.X != target.X {
		t.Errorf("path end X = %d, want %d", last.X, target.X)
	}
}

func TestFindPath_BlockedReturnsNilOrFallback(t *testing.T) {
	t.Parallel()
	w := newMockWorld()
	// Floor under start only — target area has no floor.
	w.setSolid(0, 0, 0)

	start := Node{X: 0, Y: 1, Z: 0}
	target := Node{X: 10, Y: 1, Z: 10}

	path := FindPath(start, target, w, false)
	// Without fallback, unreachable target should return nil.
	if path != nil {
		t.Errorf("FindPath without fallback should return nil for unreachable target, got %d nodes", len(path))
	}
}

func TestFindPath_FallbackReturnsClosest(t *testing.T) {
	t.Parallel()
	w := newMockWorld()
	// Partial floor: start + a few steps, then void.
	for x := int32(0); x <= 3; x++ {
		w.setSolid(x, 0, 0)
	}

	start := Node{X: 0, Y: 1, Z: 0}
	target := Node{X: 10, Y: 1, Z: 0}

	path := FindPath(start, target, w, true)
	if len(path) == 0 {
		t.Fatal("FindPath with fallback should return partial path, got empty")
	}
	// Fallback path should not reach the target (it's unreachable).
	last := path[len(path)-1]
	if last.X == target.X {
		t.Error("fallback path should not reach unreachable target")
	}
}

func TestFindPath_SameStartAndTarget(t *testing.T) {
	t.Parallel()
	w := newMockWorld()
	w.setSolid(0, 0, 0)

	start := Node{X: 0, Y: 1, Z: 0}
	path := FindPath(start, start, w, false)
	if len(path) == 0 {
		t.Fatal("FindPath with same start/target should return at least the start node")
	}
}

func TestReconstructPath_Order(t *testing.T) {
	t.Parallel()
	// Build a chain: n3 → n2 → n1 → n0 (parent links)
	n0 := &Node{X: 0, Y: 0, Z: 0}
	n1 := &Node{X: 1, Y: 0, Z: 0, Parent: n0}
	n2 := &Node{X: 2, Y: 0, Z: 0, Parent: n1}
	n3 := &Node{X: 3, Y: 0, Z: 0, Parent: n2}

	path := reconstructPath(n3)
	if len(path) != 4 {
		t.Fatalf("reconstructPath len = %d, want 4", len(path))
	}
	// Should be ordered from start (n0) to end (n3).
	if path[0].X != 0 {
		t.Errorf("path[0].X = %d, want 0 (start)", path[0].X)
	}
	if path[3].X != 3 {
		t.Errorf("path[3].X = %d, want 3 (end)", path[3].X)
	}
}

func TestIsTargetReached_Exact(t *testing.T) {
	t.Parallel()
	current := &Node{X: 5, Y: 10, Z: 15}
	target := Node{X: 5, Y: 10, Z: 15}
	if !isTargetReached(current, target) {
		t.Error("exact match should reach target")
	}
}

func TestIsTargetReached_Adjacent(t *testing.T) {
	t.Parallel()
	current := &Node{X: 5, Y: 10, Z: 15}
	target := Node{X: 6, Y: 11, Z: 16}
	if !isTargetReached(current, target) {
		t.Error("adjacent (within 1 block) should reach target")
	}
}

func TestIsTargetReached_FarAway(t *testing.T) {
	t.Parallel()
	current := &Node{X: 0, Y: 0, Z: 0}
	target := Node{X: 5, Y: 5, Z: 5}
	if isTargetReached(current, target) {
		t.Error("node 5 blocks away should not reach target")
	}
}

func TestAbs32_Positive(t *testing.T) {
	t.Parallel()
	if got := abs32(5); got != 5 {
		t.Errorf("abs32(5) = %d, want 5", got)
	}
}

func TestAbs32_Negative(t *testing.T) {
	t.Parallel()
	if got := abs32(-5); got != 5 {
		t.Errorf("abs32(-5) = %d, want 5", got)
	}
}

func TestAbs32_Zero(t *testing.T) {
	t.Parallel()
	if got := abs32(0); got != 0 {
		t.Errorf("abs32(0) = %d, want 0", got)
	}
}

func TestKey_Pack(t *testing.T) {
	t.Parallel()
	// packKey should produce unique values for different coordinates
	k1 := packKey(1, 2, 3)
	k2 := packKey(3, 2, 1)
	if k1 == k2 {
		t.Errorf("packKey(1,2,3) == packKey(3,2,1) = %d, expected different", k1)
	}
	// Same coordinates should produce same key
	k3 := packKey(1, 2, 3)
	if k1 != k3 {
		t.Errorf("packKey(1,2,3) != packKey(1,2,3): %d vs %d", k1, k3)
	}
}
