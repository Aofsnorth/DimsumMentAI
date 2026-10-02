package action_test

import (
	"bedrock-ai/internal/bot/action"
	"math"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// --- registration ---

func TestStrongholdActions_Registered(t *testing.T) {
	t.Parallel()
	handler, ok := action.ActionHandlers["explorestronghold"]
	if !ok {
		t.Fatal("ActionHandlers has no \"explorestronghold\" entry")
	}
	for _, name := range []string{"findstronghold", "stronghold"} {
		alias, ok := action.ActionHandlers[name]
		if !ok {
			t.Errorf("ActionHandlers has no %q alias", name)
			continue
		}
		if reflect.ValueOf(alias).Pointer() != reflect.ValueOf(handler).Pointer() {
			t.Errorf("ActionHandlers[%q] is a different handler than explorestronghold", name)
		}
	}
}

// --- StrongholdWaypoints ---

func TestStrongholdWaypoints_RingZeroIsOrigin(t *testing.T) {
	t.Parallel()
	got := action.StrongholdWaypoints(0, action.StrongholdRingStep)
	if len(got) != 1 || got[0] != [2]int{0, 0} {
		t.Fatalf("StrongholdWaypoints(0, %d) = %v, want [[0 0]]", action.StrongholdRingStep, got)
	}
}

func TestStrongholdWaypoints_InvalidInputsFallBackToOrigin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ ring, step int }{
		{-1, 32},
		{1, 0},
		{2, -8},
	} {
		got := action.StrongholdWaypoints(tc.ring, tc.step)
		if len(got) != 1 || got[0] != [2]int{0, 0} {
			t.Errorf("StrongholdWaypoints(%d, %d) = %v, want [[0 0]]", tc.ring, tc.step, got)
		}
	}
}

func TestStrongholdWaypoints_RingSitsOnSquarePerimeter(t *testing.T) {
	t.Parallel()
	const step = 32
	for ring := 1; ring <= 4; ring++ {
		pts := action.StrongholdWaypoints(ring, step)
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
	got := action.StrongholdWaypoints(1, 32)
	if len(got) != len(want) {
		t.Fatalf("StrongholdWaypoints(1, 32) returned %d points, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("waypoint %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// --- IsStrongholdCore ---

func TestIsStrongholdCore(t *testing.T) {
	t.Parallel()
	cores := []string{"end_portal_frame", "end_portal", "Minecraft:End_Portal_Frame"}
	for _, name := range cores {
		if !action.IsStrongholdCore(name) {
			t.Errorf("IsStrongholdCore(%q) = false, want true", name)
		}
	}
	notCores := []string{"end_gateway", "chiseled_stone_bricks", "stone", ""}
	for _, name := range notCores {
		if action.IsStrongholdCore(name) {
			t.Errorf("IsStrongholdCore(%q) = true, want false", name)
		}
	}
}

// --- bestStrongholdHint ---

func hintAt(x, z int32, strong bool) action.StrongholdHint {
	return action.StrongholdHint{Pos: protocol.BlockPos{x, 0, z}, Strong: strong}
}

func TestBestStrongholdHint_Empty(t *testing.T) {
	t.Parallel()
	if _, ok := action.BestStrongholdHint(mgl32.Vec3{}, nil); ok {
		t.Error("BestStrongholdHint on no hints should report not found")
	}
}

func TestBestStrongholdHint_StrongBeatsCloserWeak(t *testing.T) {
	t.Parallel()
	from := mgl32.Vec3{0, 64, 0}
	weakNear := hintAt(2, 0, false)
	strongFar := hintAt(60, 0, true)
	best, ok := action.BestStrongholdHint(from, []action.StrongholdHint{weakNear, strongFar})
	if !ok || best != strongFar {
		t.Errorf("BestStrongholdHint = %+v, %v; want the strong far hint", best, ok)
	}
}

func TestBestStrongholdHint_SameStrengthPicksNearest(t *testing.T) {
	t.Parallel()
	from := mgl32.Vec3{0, 64, 0}
	far := hintAt(50, 0, false)
	near := hintAt(10, 0, false)
	best, ok := action.BestStrongholdHint(from, []action.StrongholdHint{far, near})
	if !ok || best != near {
		t.Errorf("BestStrongholdHint = %+v, %v; want the nearer weak hint", best, ok)
	}
}

func TestBestStrongholdHint_UsesHorizontalDistanceOnly(t *testing.T) {
	t.Parallel()
	// A hint deep underground at the same XZ must still read as distance 0:
	// the walk is horizontal, the depth is a digging problem.
	from := mgl32.Vec3{0, 64, 0}
	deep := action.StrongholdHint{Pos: protocol.BlockPos{0, -30, 5}, Strong: false}
	shallow := action.StrongholdHint{Pos: protocol.BlockPos{0, 64, 9}, Strong: false}
	best, ok := action.BestStrongholdHint(from, []action.StrongholdHint{shallow, deep})
	if !ok || best != deep {
		t.Errorf("BestStrongholdHint = %+v, %v; want the horizontally nearer deep hint", best, ok)
	}
}

// --- ConvergeOnHint ---

func TestConvergeOnHint_RecentersOnHintCell(t *testing.T) {
	t.Parallel()
	x, z := action.ConvergeOnHint(action.StrongholdHint{Pos: protocol.BlockPos{120, -12, -48}})
	if x != 120 || z != -48 {
		t.Errorf("ConvergeOnHint = (%d, %d), want (120, -48)", x, z)
	}
}
