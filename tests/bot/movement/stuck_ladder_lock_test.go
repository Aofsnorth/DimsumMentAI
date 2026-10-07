package movement_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// TestStuckRecalcKeepsARouteWithoutDeadlocking is the regression for a hard
// self-deadlock, and the shape of the test is the assertion.
//
// handleStuckRecalcLocked is the locked half of a pair: its caller, handleStuck,
// has already taken b.Mu. Every sibling helper in that file respects the
// convention by releasing b.Mu around the work it delegates —
// recalculatePathLocked, markCurrentPathTempSolidLocked and
// breakPathObstacleLocked all do. The stuck-ladder step instead called
// b.Mu.Lock() to read one field, and sync.Mutex does not nest, so the movement
// tick blocked on a lock it was holding itself.
//
// Live, that was a total freeze: the process kept its connection, the bot stopped
// ticking, and the stall watchdog started dumping goroutines every five seconds
// with `handleStuckRecalcLocked ... steering.go:554 [sync.Mutex.Lock]` at the top
// of the movement goroutine. It is not a pathfinding failure and no amount of
// world-model work would have moved it.
//
// The test drives the function with b.Mu held, the way the tick does, and gives
// it a short deadline: a re-locking implementation wedges here and fails on the
// timeout rather than hanging the suite forever.
func TestStuckRecalcKeepsARouteWithoutDeadlocking(t *testing.T) {
	t.Parallel()

	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(pathfinder.Node{X: 6, Y: 71, Z: -6}, pathfinder.Node{X: 0, Y: 73, Z: 0})

	feet := mgl32.Vec3{6.5, 71, -5.5}
	path := []pathfinder.Node{
		{X: 6, Y: 71, Z: -6},
		{X: 5, Y: 71, Z: -7, LinkType: pathfinder.LinkWalk},
		{X: 4, Y: 72, Z: -7, LinkType: pathfinder.LinkStepJump},
	}

	b := &bot.Bot{
		MovementState: "walk_to",
		TargetPos:     mgl32.Vec3{0.5, 73, 0.5},
		WorldModel:    model,
		Pos:           feet,
		CurrentPath:   path,
		PathIndex:     1,
		// Past the third rung, which is the branch that reads CurrentPath back.
		ConsecutiveStuckCount: 4,
		Logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	tc := movement.TickContextForTest(b, feet)

	done := make(chan struct{})
	go func() {
		defer close(done)
		tc.HandleStuckRecalcForTest()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleStuckRecalcLocked wedged with b.Mu already held: " +
			"it re-acquired a non-reentrant mutex, which freezes the movement tick " +
			"and shows up live as a goroutine dump with no further logging")
	}

	// The ladder must not have thrown away a route that is still walkable. A
	// nil path here leaves the bot with no steering at all.
	if len(b.CurrentPath) == 0 {
		t.Error("stuck ladder discarded the route; the bot would have nothing to walk")
	}
}
