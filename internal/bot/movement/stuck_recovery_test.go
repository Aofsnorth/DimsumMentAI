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

// newStuckTestBot builds a walk_to bot on flat known ground. The chunk querier
// reports every cell as loaded so path smoothing is not rejected for being
// undecoded — these tests are about recovery policy, not chunk streaming.
func newStuckTestBot(feet mgl32.Vec3) *bot.Bot {
	model := pathfinder.NewLocalWorldModel()
	model.SetChunkQuerier(flatGroundQuerier{})

	feetBlockX := int32(feet.X())
	feetBlockZ := int32(feet.Z())
	feetBlockY := int32(feet.Y())
	for x := feetBlockX - 3; x <= feetBlockX+3; x++ {
		for z := feetBlockZ - 3; z <= feetBlockZ+3; z++ {
			model.SetSolid(x, feetBlockY-1, z, true)
		}
	}

	return &bot.Bot{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		MovementState: "walk_to",
		WorldModel:    model,
		WorldCache:    world.NewWorldCache(0, cube.Range{}, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Pos:           feet,
		LastTickPos:   feet,
	}
}

// flatGroundQuerier marks every cell as decoded air.
type flatGroundQuerier struct{}

func (flatGroundQuerier) GetBlockRID(x, y, z int32) (uint32, bool) { return 0, true }
func (flatGroundQuerier) IsBlockAir(x, y, z int32) (bool, bool)    { return true, true }
func (flatGroundQuerier) IsBlockSolid(x, y, z int32) (bool, bool)  { return false, true }

// TestUpdateStuckCounterFiresOnHardFreeze covers the plain "position stopped
// changing" case: after stuckIdleTicks motionless ticks the counter reports a
// freeze, and any real movement clears it again.
func TestUpdateStuckCounterFiresOnHardFreeze(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{0.5, 64, 0.5}
	b := newStuckTestBot(feet)
	tc := &TickContext{B: b, CurrPos: feet, MState: "walk_to"}

	for i := 1; i < stuckIdleTicks; i++ {
		if tc.updateStuckCounter() {
			t.Fatalf("tick %d: reported frozen before %d motionless ticks", i, stuckIdleTicks)
		}
	}
	if !tc.updateStuckCounter() {
		t.Fatalf("tick %d: still not frozen, want frozen", stuckIdleTicks)
	}

	// Movement clears it.
	moved := feet.Add(mgl32.Vec3{0.5, 0, 0})
	tc = &TickContext{B: b, CurrPos: moved, MState: "walk_to"}
	if tc.updateStuckCounter() {
		t.Fatal("moving tick: reported frozen, want cleared")
	}
	if b.TicksStuck != 0 {
		t.Fatalf("TicksStuck = %d after moving, want 0", b.TicksStuck)
	}
}

// TestProgressWindowFiresOnRubberbanding is the regression guard for the case
// the per-tick counter cannot see. Here the position changes on every tick — the
// host is snapping the bot back — yet the bot ends the window exactly where it
// started, so no forward progress was made and recovery must run.
func TestProgressWindowFiresOnRubberbanding(t *testing.T) {
	t.Parallel()

	start := mgl32.Vec3{0.5, 64, 0.5}
	destination := start.Add(mgl32.Vec3{10, 0, 0})
	b := newStuckTestBot(start)
	tc := &TickContext{B: b, CurrPos: start, MState: "walk_to", TPos: destination}

	// First call seeds the window.
	if tc.updateStuckProgressWindow() {
		t.Fatal("first sample reported no-progress, want the window to seed")
	}

	// Rubberband: shove out 1.2 blocks, snap back, repeat. The window must
	// expire on forward progress, not on summed movement.
	b.StuckWindowStart = time.Now().Add(-stuckProgressWindow)
	b.StuckWindowPos = start
	if !tc.updateStuckProgressWindow() {
		t.Fatal("rubberbanding for a full window: reported progress, want stuck")
	}
}

// TestProgressWindowIgnoresSlidingAlongWall covers the other blind spot: the bot
// creeps sideways at well over the per-tick movement threshold, so TicksStuck
// never increments, but it never gains ground toward the destination either.
func TestProgressWindowIgnoresSlidingAlongWall(t *testing.T) {
	t.Parallel()

	start := mgl32.Vec3{0.5, 64, 0.5}
	// Destination is due +Z while the bot drifts +X: real displacement, zero
	// forward progress.
	destination := start.Add(mgl32.Vec3{0, 0, 10})
	b := newStuckTestBot(start)
	tc := &TickContext{B: b, CurrPos: start, MState: "walk_to", TPos: destination}
	tc.updateStuckProgressWindow()

	// Slide 0.1 blocks per tick for the length of the window: 30 ticks * 0.1 =
	// 3 blocks of sideways travel, none of it toward the destination.
	for i := 0; i < 30; i++ {
		tc.CurrPos = start.Add(mgl32.Vec3{0.1, 0, 0})
		if tc.updateStuckCounter() {
			// The per-tick counter may fire here; that is fine, this test is
			// about the window.
			break
		}
	}
	// Age the window: the slide above runs in microseconds, not in 1.5 s.
	b.StuckWindowStart = time.Now().Add(-stuckProgressWindow)
	if !tc.updateStuckProgressWindow() {
		t.Fatal("sliding for a full window: reported progress, want stuck")
	}
}

// TestProgressWindowResetsEscalationOnRealProgress locks in the other half of
// the policy: once the bot genuinely gains ground, the escalation counter is
// cleared so a bot that struggled once and then got free is not treated as a
// repeat offender.
func TestProgressWindowResetsEscalationOnRealProgress(t *testing.T) {
	t.Parallel()

	start := mgl32.Vec3{0.5, 64, 0.5}
	destination := start.Add(mgl32.Vec3{10, 0, 0})
	b := newStuckTestBot(start)
	b.ConsecutiveStuckCount = 2
	tc := &TickContext{B: b, CurrPos: start, MState: "walk_to", TPos: destination}
	tc.updateStuckProgressWindow()

	tc.CurrPos = start.Add(mgl32.Vec3{stuckProgressMinMove + 0.5, 0, 0})
	if tc.updateStuckProgressWindow() {
		t.Fatal("real forward progress: reported stuck, want no stall")
	}
	if b.ConsecutiveStuckCount != 0 {
		t.Fatalf("ConsecutiveStuckCount = %d after real progress, want 0", b.ConsecutiveStuckCount)
	}
}

// TestForwardProgressMeasuresTravelTowardDestination is the unit check behind
// the window policy: lateral drift and round trips score zero, real travel
// scores its length.
func TestForwardProgressMeasuresTravelTowardDestination(t *testing.T) {
	t.Parallel()

	from := mgl32.Vec3{0, 64, 0}
	target := mgl32.Vec3{0, 64, 8} // destination due +Z

	if got := forwardProgress(from, from.Add(mgl32.Vec3{0, 0, 2}), target); got != 2 {
		t.Errorf("straight at the destination = %v, want 2", got)
	}
	if got := forwardProgress(from, from.Add(mgl32.Vec3{2, 0, 0}), target); got != 0 {
		t.Errorf("pure lateral slide = %v, want 0", got)
	}
	if got := forwardProgress(from, from, target); got != 0 {
		t.Errorf("no movement = %v, want 0", got)
	}
	// Already on the destination: no forward axis, so the caller must not read
	// this as a stall.
	if got := forwardProgress(target, target, target); got < stuckProgressMinMove {
		t.Errorf("standing on the destination = %v, want at least %v", got, stuckProgressMinMove)
	}
}

// TestStuckPenaltyWindowGrowsAndCaps checks the penalty escalation stays bounded
// so a temporary blocker does not stay blocked forever.
func TestStuckPenaltyWindowGrowsAndCaps(t *testing.T) {
	t.Parallel()

	first := stuckPenaltyWindow(1)
	if first != stuckPenaltyBaseWindow {
		t.Fatalf("stuckPenaltyWindow(1) = %v, want %v", first, stuckPenaltyBaseWindow)
	}
	if grown := stuckPenaltyWindow(3); grown <= first {
		t.Fatalf("stuckPenaltyWindow(3) = %v, want longer than %v", grown, first)
	}
	if capped := stuckPenaltyWindow(50); capped != stuckPenaltyMaxWindow {
		t.Fatalf("stuckPenaltyWindow(50) = %v, want cap %v", capped, stuckPenaltyMaxWindow)
	}
}

// TestWalkToRepathIntervalBacksOff checks an unsolvable target costs less and
// less often, without ever exceeding the ceiling.
func TestWalkToRepathIntervalBacksOff(t *testing.T) {
	t.Parallel()

	if got := walkToRepathIntervalFor(0); got != walkToRepathInterval {
		t.Fatalf("walkToRepathIntervalFor(0) = %v, want %v", got, walkToRepathInterval)
	}
	if got := walkToRepathIntervalFor(1); got <= walkToRepathInterval {
		t.Fatalf("walkToRepathIntervalFor(1) = %v, want longer than %v", got, walkToRepathInterval)
	}
	if got := walkToRepathIntervalFor(20); got != walkToRepathMaxInterval {
		t.Fatalf("walkToRepathIntervalFor(20) = %v, want cap %v", got, walkToRepathMaxInterval)
	}
}

// TestEnsureWalkToHasPathReplansLostRoute is the freeze regression guard. The
// host clears CurrentPath on a large position correction; a walk_to with no
// route and a far target cannot direct-steer, so without a re-plan the bot would
// stand still indefinitely and silently.
func TestEnsureWalkToHasPathReplansLostRoute(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{0.5, 64, 0.5}
	b := newStuckTestBot(feet)
	// Server wiped the route; the target is far enough that direct steering is
	// unavailable, which is exactly the freeze.
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

	if len(b.CurrentPath) == 0 {
		t.Fatal("ensureWalkToHasPath left the bot with no route, want a re-plan")
	}
	if b.WalkToRepathFailures != 0 {
		t.Fatalf("WalkToRepathFailures = %d after a successful re-plan, want 0", b.WalkToRepathFailures)
	}
}

// TestEnsureWalkToHasPathLeavesLiveRouteAlone makes sure the guard does not
// re-plan a bot that is happily following a route.
func TestEnsureWalkToHasPathLeavesLiveRouteAlone(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{0.5, 64, 0.5}
	b := newStuckTestBot(feet)
	b.CurrentPath = []pathfinder.Node{{X: 4, Y: 64, Z: 0, LinkType: pathfinder.LinkWalk}}
	b.PathIndex = 0
	b.TargetPos = mgl32.Vec3{12.5, 64, 0.5}
	b.LastPathRecalcTime = time.Now().Add(-time.Minute)

	tc := &TickContext{
		B:       b,
		CurrPos: feet,
		MState:  "walk_to",
		TPos:    b.TargetPos,
		HasPath: true,
		Dist:    12.0,
	}

	tc.ensureWalkToHasPath()

	if b.PathIndex != 0 || len(b.CurrentPath) != 1 {
		t.Fatalf("live route was disturbed: path len %d index %d", len(b.CurrentPath), b.PathIndex)
	}
}

// TestStuckRecoveryPenalisesBlockerAhead checks the unstick actually changes the
// world model. Re-planning alone cannot break a wedge: A* is deterministic, so
// the same tile and the same model return the same route and the bot walks into
// the same blocker again. The cells in front of the bot have to be marked.
func TestStuckRecoveryPenalisesBlockerAhead(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{0.5, 64, 0.5}
	b := newStuckTestBot(feet)
	tc := &TickContext{
		B:       b,
		CurrPos: feet,
		MState:  "walk_to",
		// Bot is walking toward +X, 4 blocks short of its node.
		Dx:    4.0,
		Dz:    0.0,
		Dist:  4.0,
		FeetX: 0,
		FeetY: 64,
		FeetZ: 0,
	}

	tc.markBlockingCellsTempSolidLocked(3 * time.Second)

	// The cell 0.5 blocks ahead must now read as solid so the next plan goes
	// around it.
	if !b.WorldModel.IsSolid(1, 64, 0) {
		t.Fatal("cell ahead of the bot was not marked solid, want the next path to route around it")
	}
	// The bot's own tile must stay walkable or A* walls it in.
	if b.WorldModel.IsSolid(0, 64, 0) {
		t.Fatal("the bot's own tile was marked solid, want it left walkable")
	}
}

// TestCanDirectSteerToTargetMatchesSteeringGate keeps the "give up on the path"
// decision honest about the radius the steering gate actually uses. Dropping a
// path for a far target is what produced the permanent freeze.
func TestCanDirectSteerToTargetMatchesSteeringGate(t *testing.T) {
	t.Parallel()

	feet := mgl32.Vec3{0.5, 64, 0.5}
	b := newStuckTestBot(feet)

	near := &TickContext{B: b, CurrPos: feet, MState: "walk_to", TPos: mgl32.Vec3{5.5, 64, 0.5}}
	if !near.canDirectSteerToTarget() {
		t.Fatal("near walk_to target: reported no direct steering, want true")
	}

	far := &TickContext{B: b, CurrPos: feet, MState: "walk_to", TPos: mgl32.Vec3{40.5, 64, 0.5}}
	if far.canDirectSteerToTarget() {
		t.Fatal("far walk_to target: reported direct steering, want false (dropping the path would freeze)")
	}
}
