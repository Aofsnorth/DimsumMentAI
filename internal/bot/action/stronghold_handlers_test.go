package action

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// --- strongholdWaypoints ---

func TestStrongholdWaypoints_RingZeroIsOrigin(t *testing.T) {
	t.Parallel()
	got := strongholdWaypoints(0, strongholdRingStep)
	if len(got) != 1 || got[0] != [2]int{0, 0} {
		t.Fatalf("strongholdWaypoints(0, %d) = %v, want [[0 0]]", strongholdRingStep, got)
	}
}

func TestStrongholdWaypoints_InvalidInputsFallBackToOrigin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ ring, step int }{
		{-1, 32},
		{1, 0},
		{2, -8},
	} {
		got := strongholdWaypoints(tc.ring, tc.step)
		if len(got) != 1 || got[0] != [2]int{0, 0} {
			t.Errorf("strongholdWaypoints(%d, %d) = %v, want [[0 0]]", tc.ring, tc.step, got)
		}
	}
}

func TestStrongholdWaypoints_RingSitsOnSquarePerimeter(t *testing.T) {
	t.Parallel()
	const step = 32
	for ring := 1; ring <= 4; ring++ {
		pts := strongholdWaypoints(ring, step)
		if len(pts) != 8 {
			t.Fatalf("ring %d: got %d waypoints, want 8", ring, len(pts))
		}
		seen := make(map[[2]int]bool, len(pts))
		for _, p := range pts {
			chebyshev := int(math.Max(math.Abs(float64(p[0])), math.Abs(float64(p[1]))))
			if chebyshev != ring*step {
				t.Errorf("ring %d: waypoint %v sits at Chebyshev distance %d, want %d", ring, p, chebyshev, ring*step)
			}
			if seen[p] {
				t.Errorf("ring %d: waypoint %v repeats within the ring", ring, p)
			}
			seen[p] = true
		}
	}
}

func TestStrongholdWaypoints_RingOneShape(t *testing.T) {
	t.Parallel()
	want := [][2]int{
		{32, -32}, {32, 0}, {32, 32}, {0, 32},
		{-32, 32}, {-32, 0}, {-32, -32}, {0, -32},
	}
	got := strongholdWaypoints(1, 32)
	if len(got) != len(want) {
		t.Fatalf("strongholdWaypoints(1, 32) returned %d points, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("waypoint %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// --- isStrongholdCore ---

func TestIsStrongholdCore(t *testing.T) {
	t.Parallel()
	cores := []string{"end_portal_frame", "end_portal", "Minecraft:End_Portal_Frame"}
	for _, name := range cores {
		if !isStrongholdCore(name) {
			t.Errorf("isStrongholdCore(%q) = false, want true", name)
		}
	}
	notCores := []string{"end_gateway", "chiseled_stone_bricks", "stone", ""}
	for _, name := range notCores {
		if isStrongholdCore(name) {
			t.Errorf("isStrongholdCore(%q) = true, want false", name)
		}
	}
}

// --- bestStrongholdHint ---

func hintAt(x, z int32, strong bool) strongholdHint {
	return strongholdHint{pos: protocol.BlockPos{x, 0, z}, strong: strong}
}

func TestBestStrongholdHint_Empty(t *testing.T) {
	t.Parallel()
	if _, ok := bestStrongholdHint(mgl32.Vec3{}, nil); ok {
		t.Error("bestStrongholdHint on no hints should report not found")
	}
}

func TestBestStrongholdHint_StrongBeatsCloserWeak(t *testing.T) {
	t.Parallel()
	from := mgl32.Vec3{0, 64, 0}
	weakNear := hintAt(2, 0, false)
	strongFar := hintAt(60, 0, true)
	best, ok := bestStrongholdHint(from, []strongholdHint{weakNear, strongFar})
	if !ok || best != strongFar {
		t.Errorf("bestStrongholdHint = %+v, %v; want the strong far hint", best, ok)
	}
}

func TestBestStrongholdHint_SameStrengthPicksNearest(t *testing.T) {
	t.Parallel()
	from := mgl32.Vec3{0, 64, 0}
	far := hintAt(50, 0, false)
	near := hintAt(10, 0, false)
	best, ok := bestStrongholdHint(from, []strongholdHint{far, near})
	if !ok || best != near {
		t.Errorf("bestStrongholdHint = %+v, %v; want the nearer weak hint", best, ok)
	}
}

func TestBestStrongholdHint_UsesHorizontalDistanceOnly(t *testing.T) {
	t.Parallel()
	// A hint deep underground at the same XZ must still read as distance 0:
	// the walk is horizontal, the depth is a digging problem.
	from := mgl32.Vec3{0, 64, 0}
	deep := strongholdHint{pos: protocol.BlockPos{0, -30, 5}, strong: false}
	shallow := strongholdHint{pos: protocol.BlockPos{0, 64, 9}, strong: false}
	best, ok := bestStrongholdHint(from, []strongholdHint{shallow, deep})
	if !ok || best != deep {
		t.Errorf("bestStrongholdHint = %+v, %v; want the horizontally nearer deep hint", best, ok)
	}
}

// --- convergeOnHint ---

func TestConvergeOnHint_RecentersOnHintCell(t *testing.T) {
	t.Parallel()
	x, z := convergeOnHint(strongholdHint{pos: protocol.BlockPos{120, -12, -48}})
	if x != 120 || z != -48 {
		t.Errorf("convergeOnHint = (%d, %d), want (120, -48)", x, z)
	}
}
