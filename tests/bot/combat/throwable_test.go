package combat_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/combat"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Crossbow and trident: the two weapons that are thrown rather than swung, and
// the only two whose state outlives the tick that started it.
//
// Nothing in this file needs a bot, a body or a connection. The decisions are
// over plain values precisely so that the part that matters — does it fire
// before it is loaded, does it throw at something out of reach, does it wait
// for a trident that has not come back yet — can be asked directly.

// throwNow is the reference instant every timing test measures from.
var throwNow = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

// armedCrossbow is a crossbow that has finished loading: the only state a
// crossbow may fire from.
func armedCrossbow() combat.CrossbowState {
	return combat.CrossbowState{Phase: combat.CrossbowLoaded}
}

// loadedCrossbow builds a crossbow part-way through a fresh load, `elapsed`
// after the load began.
func loadingCrossbow(elapsed time.Duration) combat.CrossbowState {
	return combat.CrossbowState{
		Phase:      combat.CrossbowLoading,
		PhaseStart: throwNow.Add(-elapsed),
	}
}

func TestPlanCrossbow_Decisions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		s    combat.CrossbowSituation
		now  time.Time
		want combat.CrossbowAction
	}{
		{
			name: "an unloaded crossbow with bolts begins the load",
			s:    combat.CrossbowSituation{HasBolts: true, TargetDistance: 12, LineOfSight: true},
			now:  throwNow,
			want: combat.CrossbowStartLoad,
		},
		{
			name: "a loaded crossbow fires on the spot",
			s:    combat.CrossbowSituation{TargetDistance: 12, LineOfSight: true, State: armedCrossbow()},
			now:  throwNow,
			want: combat.CrossbowFire,
		},
		{
			name: "a loaded crossbow fires without bolts in the pack",
			// The bolt is already in the weapon. Gating the shot on the pack
			// would strand a bot holding a charged crossbow with an empty quiver,
			// which is the most valuable shot it will ever take.
			s:    combat.CrossbowSituation{TargetDistance: 12, LineOfSight: true, HasBolts: false, State: armedCrossbow()},
			now:  throwNow,
			want: combat.CrossbowFire,
		},
		{
			name: "a crossbow with no bolts never starts a load",
			s:    combat.CrossbowSituation{HasBolts: false, TargetDistance: 12, LineOfSight: true},
			now:  throwNow,
			want: combat.CrossbowIdle,
		},
		{
			name: "a crossbow mid-load waits out the load time",
			s: combat.CrossbowSituation{HasBolts: true, TargetDistance: 12, LineOfSight: true,
				State: loadingCrossbow(combat.CrossbowLoadTime - time.Millisecond)},
			now:  throwNow,
			want: combat.CrossbowIdle,
		},
		{
			name: "a crossbow past the load time is still not fired by the planner",
			// Completing the load is a state change the caller makes, not a
			// packet. The planner must not turn elapsed time into a shot by
			// itself, or the bolt goes out before the transition is recorded.
			s: combat.CrossbowSituation{HasBolts: true, TargetDistance: 12, LineOfSight: true,
				State: loadingCrossbow(combat.CrossbowLoadTime + time.Second)},
			now:  throwNow,
			want: combat.CrossbowIdle,
		},
		{
			name: "a crossbow reloads rather than firing twice in a row",
			s: combat.CrossbowSituation{HasBolts: true, TargetDistance: 12, LineOfSight: true,
				State: combat.CrossbowState{LastShot: throwNow.Add(-10 * time.Millisecond)}},
			now:  throwNow,
			want: combat.CrossbowIdle,
		},
		{
			name: "a crossbow reloads once the reload interval has passed",
			s: combat.CrossbowSituation{HasBolts: true, TargetDistance: 12, LineOfSight: true,
				State: combat.CrossbowState{LastShot: throwNow.Add(-combat.CrossbowReloadInterval - time.Millisecond)}},
			now:  throwNow,
			want: combat.CrossbowStartLoad,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := combat.PlanCrossbow(tc.s, tc.now); got != tc.want {
				t.Fatalf("PlanCrossbow = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPlanCrossbow_ReachGating is the "do not throw at something unreachable"
// requirement. A crossbow shot at a blank wall costs a bolt and a reload.
func TestPlanCrossbow_ReachGating(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		distance float32
		los      bool
		state    combat.CrossbowState
		want     combat.CrossbowAction
	}{
		{"point blank is below the minimum band and is not shot at", 0.5, true, armedCrossbow(), combat.CrossbowIdle},
		{"inside the band with a live bolt", 10, true, armedCrossbow(), combat.CrossbowFire},
		{"just inside the minimum band", combat.CrossbowMinRange, true, armedCrossbow(), combat.CrossbowFire},
		{"inside the band and a step closer still throws", combat.CrossbowMinRange + 0.1, true, armedCrossbow(), combat.CrossbowFire},
		{"just past the maximum band", combat.CrossbowMaxRange + 0.5, true, armedCrossbow(), combat.CrossbowIdle},
		{"exactly at the maximum band", combat.CrossbowMaxRange, true, armedCrossbow(), combat.CrossbowFire},
		{"a target behind a wall is not shot at", 10, false, armedCrossbow(), combat.CrossbowIdle},
		{"the band is ignored when there is no line of sight", 10, false, armedCrossbow(), combat.CrossbowIdle},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := combat.PlanCrossbow(combat.CrossbowSituation{
				HasBolts: true, TargetDistance: tc.distance, LineOfSight: tc.los, State: tc.state,
			}, throwNow)
			if got != tc.want {
				t.Fatalf("PlanCrossbow = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCrossbowState_LoadThenFire walks the whole machine the way the tick loop
// would, and asserts the invariant the task turns on: a shot is never planned
// before the load has been completed and recorded.
func TestCrossbowState_LoadThenFire(t *testing.T) {
	t.Parallel()

	const tick = 50 * time.Millisecond
	s := combat.CrossbowState{}
	situation := combat.CrossbowSituation{HasBolts: true, TargetDistance: 14, LineOfSight: true}
	now := throwNow

	var order []combat.CrossbowAction
	var phases []combat.CrossbowPhase
	loadAt := -1
	for i := 0; i < 200; i++ {
		now = now.Add(tick)
		// The caller completes the load once the hold has run out, which is a
		// state change rather than a packet.
		//
		// The mark has to happen BEFORE the state is handed to the planner. The
		// original order copied s into the situation first and marked it after,
		// so every plan was made against a state that had not yet recorded the
		// completed load — and the test then correctly reported the machine firing
		// "from phase empty" while the machine itself was behaving properly.
		if s.LoadDue(now) {
			s.MarkLoaded(now)
		}
		situation.State = s

		action := combat.PlanCrossbow(situation, now)
		order = append(order, action)
		// The phase the planner saw when it chose this action. It has to be
		// captured here, at the moment of the decision — reconstructing it after
		// the fact from the action list is what the previous version of this check
		// did, and it started at Empty and only ever returned to Empty, so it
		// could not have passed even against a perfect implementation.
		phases = append(phases, s.Phase)

		if action == combat.CrossbowStartLoad {
			s.RecordLoad(2, now)
			continue
		}
		if action == combat.CrossbowFire {
			s.RecordFire(now)
		}
	}

	if len(order) < 3 {
		t.Fatalf("expected a load and a fire, got %v", order)
	}
	if order[0] != combat.CrossbowStartLoad {
		t.Fatalf("first action = %v, want %v", order[0], combat.CrossbowStartLoad)
	}

	// Every shot must be preceded by a load that had time to complete, and the
	// crossbow must go back to waiting afterwards. A machine that went straight
	// from one fire to the next would be shooting as fast as the tick loop runs.
	//
	// The check is deliberately about elapsed time and the state machine's own
	// knowledge, not about the literal action before the fire. A fire is preceded
	// by a long run of "wait" actions, because that is what waiting out a load
	// looks like; demanding that the immediately preceding action be the load verb
	// would fail a correct implementation and would pass one that fired without
	// ever loading. What has to hold is that a load happened since the last fire,
	// and that the hold ran its course.
	sawFire := false
	lastFire := -1
	for i, action := range order {
		switch action {
		case combat.CrossbowStartLoad:
			loadAt = i
		case combat.CrossbowFire:
			if lastFire >= 0 && i-lastFire < 2 {
				t.Fatalf("tick %d: fired again immediately after firing (actions %v)", i, order)
			}
			if loadAt < 0 || loadAt < lastFire {
				t.Fatalf("tick %d: fired with no load since the previous shot (load at %d, last fire at %d, actions %v)",
					i, loadAt, lastFire, order)
			}
			loadAt = -1
			lastFire = i
			sawFire = true
		}
	}
	if !sawFire {
		t.Fatalf("the crossbow never fired (actions %v)", order)
	}

	// No shot may be planned while the crossbow is empty or mid-load. The phase
	// recorded at plan time is the evidence; the action list alone cannot say,
	// because a load completing is a state change and not an action.
	for i, action := range order {
		if action != combat.CrossbowFire {
			continue
		}
		if phases[i] != combat.CrossbowLoaded {
			t.Fatalf("tick %d: fired from phase %v, want %v (actions %v)", i, phases[i], combat.CrossbowLoaded, order)
		}
	}
}

// TestCrossbow_NeverFiresBeforeLoaded sweeps the whole load window and asserts
// the invariant directly rather than through one happy path.
func TestCrossbow_NeverFiresBeforeLoaded(t *testing.T) {
	t.Parallel()

	for elapsed := time.Duration(0); elapsed < 3*time.Second; elapsed += 10 * time.Millisecond {
		now := throwNow.Add(elapsed)
		situation := combat.CrossbowSituation{
			HasBolts: true, TargetDistance: 14, LineOfSight: true,
			State: loadingCrossbow(elapsed),
		}
		if got := combat.PlanCrossbow(situation, now); got == combat.CrossbowFire {
			t.Fatalf("elapsed %v: crossbow fired while still loading", elapsed)
		}
		if got := combat.PlanCrossbow(combat.CrossbowSituation{
			HasBolts: true, TargetDistance: 14, LineOfSight: true, State: combat.CrossbowState{},
		}, now); got == combat.CrossbowFire {
			t.Fatalf("elapsed %v: crossbow fired from empty", elapsed)
		}
	}
}

func TestCrossbowState_LoadDue(t *testing.T) {
	t.Parallel()

	s := loadingCrossbow(0)
	if s.LoadDue(throwNow) {
		t.Fatal("a load that has just begun is not due")
	}
	if s.LoadDue(throwNow.Add(combat.CrossbowLoadTime - time.Millisecond)) {
		t.Fatal("a load one millisecond short of the time is not due")
	}
	if !s.LoadDue(throwNow.Add(combat.CrossbowLoadTime)) {
		t.Fatal("a load past the load time is due")
	}

	// A loaded crossbow is not waiting on anything.
	if armedCrossbow().LoadDue(throwNow.Add(time.Hour)) {
		t.Fatal("a loaded crossbow should never report a load as due")
	}
}

func TestCrossbowState_RecordFireEmptiesTheWeapon(t *testing.T) {
	t.Parallel()

	s := armedCrossbow()
	s.RecordFire(throwNow)

	if s.Phase != combat.CrossbowEmpty {
		t.Fatalf("phase after firing = %v, want %v", s.Phase, combat.CrossbowEmpty)
	}
	if !s.LastShot.Equal(throwNow) {
		t.Fatalf("LastShot = %v, want %v", s.LastShot, throwNow)
	}
	// A spent crossbow must not report a load as due: it has to start a new
	// one through PlanCrossbow, not have the transition happen underneath it.
	if s.LoadDue(throwNow.Add(time.Hour)) {
		t.Fatal("an empty crossbow must not report a load as due")
	}
}

func TestCrossbowState_RecordLoadKeepsTheSlot(t *testing.T) {
	t.Parallel()

	s := combat.CrossbowState{}
	s.RecordLoad(5, throwNow)

	if s.Slot != 5 {
		t.Fatalf("Slot = %d, want 5", s.Slot)
	}
	if s.Phase != combat.CrossbowLoading {
		t.Fatalf("Phase = %v, want %v", s.Phase, combat.CrossbowLoading)
	}
	if !s.PhaseStart.Equal(throwNow) {
		t.Fatalf("PhaseStart = %v, want %v", s.PhaseStart, throwNow)
	}
}

func TestCrossbowLoadTimeIsTheOneTheShotStateMachineUses(t *testing.T) {
	t.Parallel()

	// shot.go already pays this wait for a crossbow. The two must not drift
	// apart, or a bolt would be released at one point in the animation and
	// considered loaded at another.
	if combat.CrossbowLoadTime <= 0 {
		t.Fatal("the crossbow load time must be a real wait")
	}
	if combat.CrossbowReloadInterval < combat.CrossbowLoadTime {
		t.Fatalf("reload interval %v is shorter than the load time %v",
			combat.CrossbowReloadInterval, combat.CrossbowLoadTime)
	}
}

func TestCrossbowAimPoint_LiftsTheAimByDistance(t *testing.T) {
	t.Parallel()

	from := mgl32.Vec3{0, 64, 0}
	near := combat.CrossbowAimPoint(from, mgl32.Vec3{2, 64, 0})
	far := combat.CrossbowAimPoint(from, mgl32.Vec3{20, 64, 0})

	if near.Y() <= 65.2 {
		t.Fatalf("near aim height = %v, want at least the 65.2 body centre", near.Y())
	}
	if far.Y() <= near.Y() {
		t.Fatalf("far aim height = %v, want above the near aim height %v", far.Y(), near.Y())
	}
}

func TestCrossbowAction_String(t *testing.T) {
	t.Parallel()

	seen := map[combat.CrossbowAction]string{
		combat.CrossbowIdle:      "wait",
		combat.CrossbowStartLoad: "load",
		combat.CrossbowFire:      "fire",
	}
	for action, want := range seen {
		if got := action.String(); got != want {
			t.Fatalf("CrossbowAction(%d).String() = %q, want %q", int(action), got, want)
		}
	}
	if got := combat.CrossbowAction(99).String(); got == "" {
		t.Fatal("an unknown crossbow action must still name itself for a log line")
	}
}

func TestCrossbowPhase_String(t *testing.T) {
	t.Parallel()

	seen := map[combat.CrossbowPhase]string{
		combat.CrossbowEmpty:   "empty",
		combat.CrossbowLoading: "loading",
		combat.CrossbowLoaded:  "loaded",
	}
	for phase, want := range seen {
		if got := phase.String(); got != want {
			t.Fatalf("CrossbowPhase(%d).String() = %q, want %q", int(phase), got, want)
		}
	}
	if got := combat.CrossbowPhase(99).String(); got == "" {
		t.Fatal("an unknown crossbow phase must still name itself for a log line")
	}
}

// ===================== TRIDENT =====================

// flyingTrident is a trident that has been thrown and is not back yet.
func flyingTrident(thrown time.Duration) combat.TridentState {
	return combat.TridentState{Phase: combat.TridentInFlight, ThrownAt: throwNow.Add(-thrown)}
}

func TestPlanTrident_Decisions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		s    combat.TridentSituation
		now  time.Time
		want combat.TridentAction
	}{
		{
			name: "a held trident at range is thrown",
			s:    combat.TridentSituation{HasTrident: true, TargetDistance: 10, LineOfSight: true},
			now:  throwNow,
			want: combat.TridentThrow,
		},
		{
			name: "a trident in flight is never thrown again",
			// The bot is holding air. A second throw here is a wasted tick and
			// the reason a trident user ends up with nothing in hand.
			s:    combat.TridentSituation{HasTrident: false, TargetDistance: 10, LineOfSight: true, State: flyingTrident(time.Second)},
			now:  throwNow,
			want: combat.TridentWait,
		},
		{
			name: "a returning trident is still waited for",
			s: combat.TridentSituation{TargetDistance: 10, LineOfSight: true,
				State: combat.TridentState{Phase: combat.TridentReturning, ThrownAt: throwNow.Add(-2 * time.Second)}},
			now:  throwNow,
			want: combat.TridentWait,
		},
		{
			name: "a trident that never came back is given up on",
			s:    combat.TridentSituation{TargetDistance: 10, LineOfSight: true, State: flyingTrident(combat.TridentReturnTimeout + time.Second)},
			now:  throwNow,
			want: combat.TridentRecover,
		},
		{
			name: "a trident back in the slot is thrown again",
			// The recorded phase still says returning, but the bot is holding
			// one. The live inventory reading wins over the bookkeeping, or a
			// trident that has landed would leave the bot waiting on a weapon
			// it is already holding.
			s: combat.TridentSituation{HasTrident: true, TargetDistance: 10, LineOfSight: true,
				State: combat.TridentState{Phase: combat.TridentReturning, ThrownAt: throwNow.Add(-2 * time.Second)}},
			now:  throwNow,
			want: combat.TridentThrow,
		},
		{
			name: "a bot with no trident at all does nothing",
			s:    combat.TridentSituation{HasTrident: false, TargetDistance: 10, LineOfSight: true},
			now:  throwNow,
			want: combat.TridentIdle,
		},
		{
			name: "the throw is paced",
			s: combat.TridentSituation{HasTrident: true, TargetDistance: 10, LineOfSight: true,
				State: combat.TridentState{LastThrow: throwNow.Add(-10 * time.Millisecond)}},
			now:  throwNow,
			want: combat.TridentWait,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := combat.PlanTrident(tc.s, tc.now); got != tc.want {
				t.Fatalf("PlanTrident = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlanTrident_ReachGating(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		distance float32
		los      bool
		want     combat.TridentAction
	}{
		{"just inside the minimum band", combat.TridentMinRange, true, combat.TridentThrow},
		{"mid range throws", 10, true, combat.TridentThrow},
		{"below the minimum band is not thrown at", 0.5, true, combat.TridentIdle},
		{"past the maximum band", combat.TridentMaxRange + 0.5, true, combat.TridentIdle},
		{"exactly at the maximum band", combat.TridentMaxRange, true, combat.TridentThrow},
		{"a target behind cover is not thrown at", 10, false, combat.TridentIdle},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := combat.PlanTrident(combat.TridentSituation{
				HasTrident: true, TargetDistance: tc.distance, LineOfSight: tc.los,
			}, throwNow)
			if got != tc.want {
				t.Fatalf("PlanTrident = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTridentLoyalty_Decisions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		s    combat.TridentSituation
		now  time.Time
		want combat.TridentAction
	}{
		{
			name: "held and in hand throws again",
			s: combat.TridentSituation{HasTrident: true, TargetDistance: 9, LineOfSight: true,
				State: combat.TridentState{Phase: combat.TridentReturning, ThrownAt: throwNow.Add(-5 * time.Second)}},
			now:  throwNow,
			want: combat.TridentThrow,
		},
		{
			name: "returning but not yet in hand waits",
			s: combat.TridentSituation{HasTrident: false, TargetDistance: 9, LineOfSight: true,
				State: combat.TridentState{Phase: combat.TridentReturning, ThrownAt: throwNow.Add(-5 * time.Second)}},
			now:  throwNow,
			want: combat.TridentWait,
		},
		{
			name: "in flight and not in hand waits",
			s: combat.TridentSituation{HasTrident: false, TargetDistance: 9, LineOfSight: true,
				State: flyingTrident(2 * time.Second)},
			now:  throwNow,
			want: combat.TridentWait,
		},
		{
			name: "a lost trident with nothing in the hand is given up on",
			s: combat.TridentSituation{HasTrident: false, TargetDistance: 9, LineOfSight: true,
				State: combat.TridentState{Phase: combat.TridentLost, ThrownAt: throwNow.Add(-time.Hour)}},
			now:  throwNow,
			want: combat.TridentRecover,
		},
		{
			name: "a held trident that is simply in the slot throws",
			s:    combat.TridentSituation{HasTrident: true, TargetDistance: 9, LineOfSight: true},
			now:  throwNow,
			want: combat.TridentThrow,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := combat.PlanTrident(tc.s, tc.now); got != tc.want {
				t.Fatalf("PlanTrident = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTridentState_ThrowAndReturn walks the throw, the wait and the recovery.
func TestTridentState_ThrowAndReturn(t *testing.T) {
	t.Parallel()

	const tick = 50 * time.Millisecond
	s := combat.TridentState{}
	now := throwNow

	// The throw is recorded and the weapon leaves the hand.
	s.RecordThrow(3, now)
	if s.Phase != combat.TridentInFlight {
		t.Fatalf("phase after throw = %v, want %v", s.Phase, combat.TridentInFlight)
	}
	if s.Slot != 3 {
		t.Fatalf("Slot = %d, want 3", s.Slot)
	}
	if s.HeldInSlot {
		t.Fatal("a trident that was just thrown is not in the slot")
	}

	// Mid flight the bot must be told to wait.
	if got := combat.PlanTrident(combat.TridentSituation{
		HasTrident: false, TargetDistance: 9, LineOfSight: true, State: s,
	}, now); got != combat.TridentWait {
		t.Fatalf("mid flight = %v, want %v", got, combat.TridentWait)
	}

	// The server flags the trident as flying home. That is an anticipation:
	// it does not put the item back in the slot.
	now = now.Add(2 * time.Second)
	if s.UpdateLoyalty(combat.TridentFlight{Returning: true, Observed: true}, now) {
		t.Fatal("a returning trident has not been recovered yet")
	}
	if s.Phase != combat.TridentReturning {
		t.Fatalf("phase after the return flag = %v, want %v", s.Phase, combat.TridentReturning)
	}

	// It actually lands back in the hotbar. That is the confirmation, and it
	// is the caller's inventory reading that says so — nothing in this package
	// looks at the hotbar.
	now = now.Add(time.Second)
	s.HeldInSlot = true
	if !s.UpdateLoyalty(combat.TridentFlight{}, now) {
		t.Fatal("a trident back in its slot must be reported as recovered")
	}
	if s.Phase != combat.TridentHeld {
		t.Fatalf("phase after recovery = %v, want %v", s.Phase, combat.TridentHeld)
	}

	// And it can be thrown again.
	if got := combat.PlanTrident(combat.TridentSituation{
		HasTrident: true, TargetDistance: 9, LineOfSight: true, State: s,
	}, now); got != combat.TridentThrow {
		t.Fatalf("after recovery = %v, want %v", got, combat.TridentThrow)
	}
}

// TestTridentState_ReturnTimeout gives up on a trident that never comes back,
// which is what a throw into a wall or across a chunk border looks like.
func TestTridentState_ReturnTimeout(t *testing.T) {
	t.Parallel()

	s := flyingTrident(combat.TridentReturnTimeout)
	if s.UpdateLoyalty(combat.TridentFlight{}, throwNow) {
		t.Fatal("a trident inside the return window has not been lost")
	}
	if s.Phase != combat.TridentInFlight {
		t.Fatalf("phase = %v, want %v", s.Phase, combat.TridentInFlight)
	}

	// Past the window the trident is given up on. UpdateLoyalty reports
	// *recovery*, and a trident that is lost has not been recovered — the phase
	// is the thing that says it is gone.
	s = flyingTrident(combat.TridentReturnTimeout + time.Millisecond)
	if s.UpdateLoyalty(combat.TridentFlight{}, throwNow) {
		t.Fatal("a lost trident has not been recovered")
	}
	if s.Phase != combat.TridentLost {
		t.Fatalf("phase = %v, want %v", s.Phase, combat.TridentLost)
	}
}

func TestTridentState_UpdateLoyaltyNeedsAnObservation(t *testing.T) {
	t.Parallel()

	// With no reader wired, nothing is known: the trident stays in flight and
	// is not silently promoted to returning or lost.
	s := flyingTrident(time.Second)
	if s.UpdateLoyalty(combat.TridentFlight{Observed: false}, throwNow) {
		t.Fatal("an unobserved flight must not report a recovery")
	}
	if s.Phase != combat.TridentInFlight {
		t.Fatalf("phase = %v, want %v", s.Phase, combat.TridentInFlight)
	}
}

func TestTridentReturnTimeoutIsLongerThanAThrowInterval(t *testing.T) {
	t.Parallel()

	if combat.TridentReturnTimeout <= combat.TridentThrowInterval {
		t.Fatalf("return timeout %v must outlast the throw interval %v",
			combat.TridentReturnTimeout, combat.TridentThrowInterval)
	}
}

func TestTridentAimPoint_LiftsTheAimByDistance(t *testing.T) {
	t.Parallel()

	from := mgl32.Vec3{0, 64, 0}
	near := combat.TridentAimPoint(from, mgl32.Vec3{3, 64, 0})
	far := combat.TridentAimPoint(from, mgl32.Vec3{18, 64, 0})

	if near.Y() <= 65.2 {
		t.Fatalf("near aim height = %v, want at least the 65.2 body centre", near.Y())
	}
	if far.Y() <= near.Y() {
		t.Fatalf("far aim height = %v, want above the near aim height %v", far.Y(), near.Y())
	}
}

func TestTridentAction_String(t *testing.T) {
	t.Parallel()

	seen := map[combat.TridentAction]string{
		combat.TridentIdle:    "idle",
		combat.TridentThrow:   "throw",
		combat.TridentWait:    "wait for it to come back",
		combat.TridentRecover: "recover",
	}
	for action, want := range seen {
		if got := action.String(); got != want {
			t.Fatalf("TridentAction(%d).String() = %q, want %q", int(action), got, want)
		}
	}
	if got := combat.TridentAction(99).String(); got == "" {
		t.Fatal("an unknown trident action must still name itself for a log line")
	}
}

func TestTridentPhase_String(t *testing.T) {
	t.Parallel()

	seen := map[combat.TridentPhase]string{
		combat.TridentHeld:      "in hand",
		combat.TridentInFlight:  "in flight",
		combat.TridentReturning: "returning",
		combat.TridentLost:      "lost",
	}
	for phase, want := range seen {
		if got := phase.String(); got != want {
			t.Fatalf("TridentPhase(%d).String() = %q, want %q", int(phase), got, want)
		}
	}
	if got := combat.TridentPhase(99).String(); got == "" {
		t.Fatal("an unknown trident phase must still name itself for a log line")
	}
}

// ===================== THE FLAG THE SERVER SETS =====================

// TestEntityFlag_ReadsTheTridentReturnFlag pins the encoding of
// protocol.EntityDataFlagReturnTrident as it actually arrives on the wire: a
// bit in the int64 at protocol.EntityDataKeyFlags, not a standalone key. The
// value 53 was printed from the module, not taken from memory.
func TestEntityFlag_ReadsTheTridentReturnFlag(t *testing.T) {
	t.Parallel()

	const returnTrident = uint32(protocol.EntityDataFlagReturnTrident)

	if returnTrident != 53 {
		t.Fatalf("EntityDataFlagReturnTrident = %d, want 53", returnTrident)
	}

	meta := protocol.EntityMetadata{
		uint32(protocol.EntityDataKeyFlags): int64(1) << returnTrident,
	}
	if !combat.EntityFlagSet(meta, returnTrident) {
		t.Fatal("the trident return flag was not read out of the flags bitfield")
	}

	if combat.EntityFlagSet(meta, uint32(protocol.EntityDataFlagOnFire)) {
		t.Fatal("a flag that was never set must not read as set")
	}
	if combat.EntityFlagSet(protocol.EntityMetadata{}, returnTrident) {
		t.Fatal("empty metadata has no flags set")
	}
}

// TestEntityFlagSet_ToleratesTheNumericShapesHostsUse covers the encodings a
// SetActorData can plausibly carry for a bitfield, because a reader that only
// handles int64 reports "no flags" on a host that never sends one.
//
// There is a real limit here and the test states it rather than papering over
// it: the trident return flag is bit 53, which does not fit in 32 bits at all.
// A host that delivers the flags field as an int32 or narrower cannot express
// it, and no reader can recover a bit that was never on the wire. So the wide
// types are the ones that carry the flag, and the narrow ones are checked for
// the reading they can actually support — a low flag — rather than for a
// truncated high one.
func TestEntityFlagSet_ToleratesTheNumericShapesHostsUse(t *testing.T) {
	t.Parallel()

	const bit = uint32(protocol.EntityDataFlagReturnTrident)
	want := int64(1) << bit

	// Bit 53 is the first bit that does not fit in a uint32, so any flags field
	// narrower than 64 bits is structurally unable to carry this flag.
	if bit < 32 {
		t.Fatalf("EntityDataFlagReturnTrident = %d, which would fit in 32 bits; this test's premise is wrong", bit)
	}

	for name, value := range map[string]any{
		"int64":   want,
		"uint64":  uint64(want),
		"float64": float64(want),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			meta := protocol.EntityMetadata{uint32(protocol.EntityDataKeyFlags): value}
			if !combat.EntityFlagSet(meta, bit) {
				t.Fatalf("a %s flags value was not read", name)
			}
		})
	}

	// A float cannot hold bit 53 exactly, so it is only asked about a low flag.
	// This is a host re-encoding the field, not the module's own encoding.
	t.Run("float32 reads a low flag", func(t *testing.T) {
		t.Parallel()
		const low = uint32(protocol.EntityDataFlagOnFire)
		meta := protocol.EntityMetadata{
			uint32(protocol.EntityDataKeyFlags): float32(uint32(1) << low),
		}
		if !combat.EntityFlagSet(meta, low) {
			t.Fatal("a float32 flags value was not read")
		}
	})

	// The narrow types are read as numbers; they simply cannot be asked about
	// a bit that does not fit in them.
	for name, value := range map[string]any{
		"int32":  int32(1),
		"uint32": uint32(1),
		"int16":  int16(1),
		"uint16": uint16(1),
		"int8":   int8(1),
		"uint8":  uint8(1),
	} {
		t.Run(name+" reads a low flag", func(t *testing.T) {
			t.Parallel()
			meta := protocol.EntityMetadata{uint32(protocol.EntityDataKeyFlags): value}
			if !combat.EntityFlagSet(meta, uint32(protocol.EntityDataFlagOnFire)) {
				t.Fatalf("a %s flags value was not read", name)
			}
		})
	}
}
