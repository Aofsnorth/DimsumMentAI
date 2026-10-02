package movement_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"
)

// Arriving at the end of a path tears the trip's state down — including the
// sprint latch — from inside the movement tick's critical section. When that
// teardown went through the locking ClearSprintHint, the tick waited for a lock
// only it could release: sync.Mutex is not reentrant. Every other bot goroutine
// (packet handling, the AGI loop, survival, the chat listener) then piled up
// behind that one held mutex and the whole bot froze mid-world.
//
// A test cannot prove a deadlock by timing alone, so it proves the lock
// discipline instead: the tick must return, and the mutex must still be
// acquirable afterwards, which is only true if the latch is dropped without
// re-entering the lock.
func TestArrivingAtEndOfPathDoesNotDeadlockOnSprintHint(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.Default()}
	b.SetSprintHint(true)
	b.MovementState = "walk_to"
	b.CurrentPath = []pathfinder.Node{{X: 1, Y: 2, Z: 3}}
	b.PathIndex = 0
	b.Pos = mgl32.Vec3{1, 2, 3}

	tc := &movement.TickContext{
		B:       b,
		CurrPos: b.Pos,
		TPos:    b.Pos,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		tc.AdvancePathForTest(2.5)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("advancePath never returned: it re-locked b.Mu while holding it")
	}

	if sprint, hop, latched := b.SprintHint(); latched {
		t.Errorf("arrival left the sprint latch set: sprint=%t hop=%t", sprint, hop)
	}
	if b.CurrentPath != nil {
		t.Error("arrival left a finished path on the bot")
	}

	// The mutex must be free for the next goroutine, not merely unlocked by the
	// deferred call of a tick that is still waiting inside it.
	acquired := make(chan struct{})
	go func() {
		b.Mu.Lock()
		b.Mu.Unlock()
		close(acquired)
	}()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("b.Mu stayed locked after the movement tick returned")
	}
}

// The mid-path step must keep the latch: only arrival ends a trip, so a
// checkpoint crossed on the way must not drop Jev's chosen gait.
func TestAdvancingMidPathKeepsSprintHint(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.Default()}
	b.SetSprintHint(false)
	b.MovementState = "walk_to"
	b.CurrentPath = []pathfinder.Node{{X: 1, Y: 2, Z: 3}, {X: 2, Y: 2, Z: 3}}
	b.PathIndex = 0
	b.Pos = mgl32.Vec3{1, 2, 3}

	tc := &movement.TickContext{
		B:       b,
		CurrPos: b.Pos,
		TPos:    b.Pos,
	}
	tc.AdvancePathForTest(2.5)

	if sprint, hop, latched := b.SprintHint(); !latched || !sprint || hop {
		t.Errorf("mid-path latch = (%t,%t,%t), want (true,false,true)", sprint, hop, latched)
	}
	if b.PathIndex != 1 {
		t.Errorf("path index = %d, want 1", b.PathIndex)
	}
	if len(b.CurrentPath) != 2 {
		t.Errorf("mid-path step discarded the path: %d nodes left", len(b.CurrentPath))
	}
}

// The public ClearSprintHint must still take the lock — the unlocked variant
// exists for one caller, not as a licence to skip locking everywhere.
func TestClearSprintHintStillLocks(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.Default()}
	b.SetSprintHint(true)

	b.ClearSprintHint()
	if _, _, latched := b.SprintHint(); latched {
		t.Fatal("ClearSprintHint left the latch set")
	}
}

// Arriving by target tolerance rather than by exhausting the path clears the
// latch too. That branch already released the lock before calling, and the two
// must stay that way.
func TestArrivingByToleranceClearsSprintHint(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.Default()}
	b.SetSprintHint(true)
	b.MovementState = "walk_to"
	b.Pos = mgl32.Vec3{4, 5, 6}
	b.CurrentPath = []pathfinder.Node{{X: 10, Y: 5, Z: 6}}
	b.PathIndex = 0

	tc := &movement.TickContext{
		B:       b,
		CurrPos: b.Pos,
		TPos:    b.Pos,
		Dx:      0,
		Dz:      0,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		tc.CheckWalkToArrivalForTest()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("checkWalkToArrival never returned")
	}

	if _, _, latched := b.SprintHint(); latched {
		t.Error("arrival by tolerance left the sprint latch set")
	}
	if b.MovementState != "idle" {
		t.Errorf("movement state = %q, want idle", b.MovementState)
	}
}
