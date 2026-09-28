package bot

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// TestWalkToReusesTheRouteForTheSameTarget is the regression for the A* loop in
// the log: "recalculating path using A*" repeated three times inside 750ms for a
// target one block away. Callers re-assert their destination while they wait, so
// a repeat of the same target must reuse the live route.
func TestWalkToReusesTheRouteForTheSameTarget(t *testing.T) {
	t.Parallel()

	live := shouldReplanForWalkTo("walk_to", true, mgl32.Vec3{10, 64, 10}, mgl32.Vec3{10, 64, 10})
	if live {
		t.Fatal("re-asserting the same target re-planned, want the live route reused")
	}

	// A dropped item creeps; a small drift is not a new destination.
	creep := shouldReplanForWalkTo("walk_to", true, mgl32.Vec3{10, 64, 10}, mgl32.Vec3{10.1, 64, 10.1})
	if creep {
		t.Fatalf("a %.2f block drift re-planned, want a route for anything inside %v", 0.14, walkToReplanEpsilon)
	}
}

// TestWalkToReplansWhenTheTargetMoves keeps the guard from freezing a stale
// route: a destination that genuinely moves (a running player, a far corner)
// has to be re-planned.
func TestWalkToReplansWhenTheTargetMoves(t *testing.T) {
	t.Parallel()

	far := shouldReplanForWalkTo("walk_to", true, mgl32.Vec3{10, 64, 10}, mgl32.Vec3{12, 64, 10})
	if !far {
		t.Fatal("a destination two blocks away kept the stale route, want a re-plan")
	}

	higher := shouldReplanForWalkTo("walk_to", true, mgl32.Vec3{10, 64, 10}, mgl32.Vec3{10, 66, 10})
	if !higher {
		t.Fatal("a destination two blocks up kept the stale route, want a re-plan")
	}
}

// TestWalkToReplansWhenThereIsNoRouteOrModeIsWrong covers the two states the
// guard must never suppress: a walk_to whose route was wiped (the host corrects
// position and clears it), and a fresh mode taking over from another.
func TestWalkToReplansWhenThereIsNoRouteOrModeIsWrong(t *testing.T) {
	t.Parallel()

	same := mgl32.Vec3{10, 64, 10}
	if !shouldReplanForWalkTo("walk_to", false, same, same) {
		t.Fatal("a walk_to with no route kept walking without one, want a re-plan")
	}
	if !shouldReplanForWalkTo("follow", true, same, same) {
		t.Fatal("a follow replaced by walk_to kept the old route, want a re-plan")
	}
	if !shouldReplanForWalkTo("idle", true, same, same) {
		t.Fatal("an idle bot was told to walk without planning, want a re-plan")
	}
}
