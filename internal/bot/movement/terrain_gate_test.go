// The terrain gate, and the freeze it exists to prevent.
//
// The failure this guards against is not hypothetical. Right after a LAN join
// the bot receives its first UpdateAttributes with a lower health than the
// default, the damage handler fires, and it asks for a re-plan. At that instant
// the WorldCache holds a handful of chunks and the world model reports the
// ground under the bot as air. A* then expands over a world that is not there:
// every neighbour is vetoed for having no floor, the search burns all 30000
// iterations, and the fallback passes do the same. The log for that run was
// three full-budget searches and a "pathfinding failed" — and a bot that never
// moved again, because the one plan it ever made was aimed at the zero vector
// across a world the bot could not see.
//
// So: no search until the world can answer what is under the bot's feet.

package movement

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/bot/world"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/go-gl/mathgl/mgl32"
)

// unloadedQuerier is what the world looks like in the first moments after a
// join: the cache has been created, the model has a querier wired to it, and
// not one cell has been decoded. Every query reports "not loaded", which the
// pathfinder reads as air.
type unloadedQuerier struct{}

func (unloadedQuerier) GetBlockRID(x, y, z int32) (uint32, bool) { return 0, false }
func (unloadedQuerier) IsBlockAir(x, y, z int32) (bool, bool)    { return true, false }
func (unloadedQuerier) IsBlockSolid(x, y, z int32) (bool, bool)  { return false, false }

func newUnloadedTestBot(feet mgl32.Vec3) *bot.Bot {
	model := pathfinder.NewLocalWorldModel()
	model.SetChunkQuerier(unloadedQuerier{})

	return &bot.Bot{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		MovementState: "walk_to",
		WorldModel:    model,
		WorldCache:    world.NewWorldCache(0, cube.Range{}, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Pos:           feet,
		LastTickPos:   feet,
	}
}

// TestRecalculatePathSkipsUnloadedWorld is the freeze regression. A walk_to over
// a world that cannot describe its own floor must return without producing a
// route, and without burning a full iteration budget rediscovering that there
// is nothing there.
func TestRecalculatePathSkipsUnloadedWorld(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{0.5, 64, 0.5}
	b := newUnloadedTestBot(feet)
	// A real destination, so the only thing stopping the search is the gate.
	b.TargetPos = mgl32.Vec3{20.5, 64, 0.5}

	RecalculatePath(b)

	if len(b.CurrentPath) != 0 {
		t.Fatalf("path over an unloaded world = %d nodes, want none", len(b.CurrentPath))
	}
}

// TestRecalculatePathProceedsOnceTerrainArrives is the other half: the gate must
// not be a wall. Once the world can answer, the same walk_to has to produce a
// real route, or the fix would trade a freeze for a bot that never moves.
func TestRecalculatePathProceedsOnceTerrainArrives(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{0.5, 64, 0.5}
	b := newStuckTestBot(feet) // querier reports every cell loaded
	b.TargetPos = mgl32.Vec3{12.5, 64, 0.5}

	RecalculatePath(b)

	if len(b.CurrentPath) == 0 {
		t.Fatal("walk_to over loaded terrain produced no route, want a path")
	}
}

// TestRecalculatePathSkipsIdleBot covers the other half of the join-time damage
// path: the bot is hurt before anybody asked it to go anywhere, so there is no
// destination and the search must not run at all.
func TestRecalculatePathSkipsIdleBot(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{0.5, 64, 0.5}
	b := newStuckTestBot(feet)
	b.MovementState = "idle"
	b.TargetPos = mgl32.Vec3{} // the zero vector an idle bot still carries

	RecalculatePath(b)

	if len(b.CurrentPath) != 0 {
		t.Fatalf("idle bot produced a %d node route toward %v, want none", len(b.CurrentPath), b.TargetPos)
	}
}

// TestUnloadedWorldDoesNotBackOffRepaths pins the second half of the fix.
//
// A search that failed only because the terrain had not arrived is not a
// failure of the route, it is a failure of the clock. Counting it drives
// WalkToRepathFailures up, which doubles the retry interval each time up to
// walkToRepathMaxInterval — so the bot sits out a 30 second pause for every
// attempt made during the load and gives up long before the world is there.
func TestUnloadedWorldDoesNotBackOffRepaths(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{0.5, 64, 0.5}
	b := newUnloadedTestBot(feet)
	b.CurrentPath = nil
	b.PathIndex = 0
	b.TargetPos = mgl32.Vec3{12.5, 64, 0.5}
	b.TargetTolerance = 2.0
	b.LastPathRecalcTime = time.Now().Add(-time.Minute)

	tc := &TickContext{
		B:               b,
		CurrPos:         feet,
		MState:          "walk_to",
		TPos:            b.TargetPos,
		HasPath:         false,
		TargetTolerance: 2.0,
		Dist:            12.0,
	}

	tc.ensureWalkToHasPath()

	if b.WalkToRepathFailures != 0 {
		t.Errorf("unloaded-world failure counted as %d real failures, want 0: the retry interval backs off and the bot gives up before the terrain arrives",
			b.WalkToRepathFailures)
	}
}
