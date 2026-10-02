package combat_test

import (
	"bedrock-ai/internal/bot/affordance"
	"context"
	"math"
	"sync"
	"testing"

	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
)

// recordingFakeBot is the combat fake plus a record of movement and look
// commands, so tactics can be asserted on what the bot was told to do.
type recordingFakeBot struct {
	*combatFakeBot
	mu    sync.Mutex
	navs  []mgl32.Vec3
	looks []mgl32.Vec3
}

func (f *recordingFakeBot) NavigateTo(pos mgl32.Vec3) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.navs = append(f.navs, pos)
}

func (f *recordingFakeBot) LookAt(pos mgl32.Vec3) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.looks = append(f.looks, pos)
}

func newRecordingFakeBot(origin mgl32.Vec3) *recordingFakeBot {
	return &recordingFakeBot{
		combatFakeBot: newCombatFakeBot(origin, stripLoaded(-40, 40), noSolid),
	}
}

func addMob(f *combatFakeBot, id uint64, mob string, x float32) {
	f.actors[id] = &entity.Info{ID: id, Type: "minecraft:" + mob, Name: "minecraft:" + mob, Position: mgl32.Vec3{x, 64, 0}, Health: 20}
}

func TestMobMovePlan_PerMob(t *testing.T) {
	t.Parallel()

	creeper := combat.MobMovePlan("creeper", 2, affordance.Careful)
	if !creeper.Flee || creeper.SafeDistance != combat.CreeperSafeDistance {
		t.Fatalf("creeper at 2 = %+v, want flee to %v", creeper, combat.CreeperSafeDistance)
	}
	if got := combat.MobMovePlan("creeper", 5, affordance.Careful); got.Flee {
		t.Fatalf("creeper at 5 = %+v, want to stand and fight", got)
	}

	skel := combat.MobMovePlan("skeleton", 9, affordance.Careful)
	if !skel.Strafe || skel.BandMin != combat.SkeletonBandMin || skel.BandMax != combat.SkeletonBandMax {
		t.Fatalf("skeleton = %+v, want strafe in band [%v,%v]", skel, combat.SkeletonBandMin, combat.SkeletonBandMax)
	}
	if got := combat.MobMovePlan("stray", 9, affordance.Careful); !got.Strafe {
		t.Fatalf("stray = %+v, want the same strafe as a skeleton", got)
	}

	end := combat.MobMovePlan("enderman", 5, affordance.Careful)
	if end.Look != combat.LookFeet {
		t.Fatalf("enderman = %+v, want LookFeet", end)
	}

	if got := combat.MobMovePlan("zombie", 5, affordance.Careful); got.Flee || got.Strafe || got.Look != combat.LookCenter {
		t.Fatalf("zombie = %+v, want the plain melee default", got)
	}
}

func TestRetreatAndStrafeGeometry(t *testing.T) {
	t.Parallel()

	bot := mgl32.Vec3{0, 64, 0}
	threat := mgl32.Vec3{2, 64, 0}

	retreat := combat.RetreatPoint(bot, threat, 6)
	if retreat.X() != -4 || retreat.Z() != 0 {
		t.Fatalf("retreatPoint = %v, want (-4,64,0)", retreat)
	}
	if d := combat.HorizontalDistance(retreat, threat); math.Abs(float64(d-6)) > 1e-3 {
		t.Fatalf("retreat distance = %v, want 6", d)
	}

	// Standing exactly on the threat must still yield a point the safe
	// distance away rather than a division by zero.
	overlap := combat.RetreatPoint(threat, threat, 6)
	if d := combat.HorizontalDistance(overlap, threat); math.Abs(float64(d-6)) > 1e-3 {
		t.Fatalf("overlap retreat distance = %v, want 6", d)
	}

	strafe := combat.StrafePoint(bot, threat, 9, 1)
	if d := combat.HorizontalDistance(strafe, threat); math.Abs(float64(d-9)) > 1e-3 {
		t.Fatalf("strafe distance = %v, want 9", d)
	}
	// A quarter turn means the strafe point is sideways, not closer.
	if strafe.X() != threat.X() {
		t.Fatalf("strafe X = %v, want to hold the line at %v", strafe.X(), threat.X())
	}
}

func TestTick_CreeperTooClose_DisengagesAndFlees(t *testing.T) {
	t.Parallel()
	f := newRecordingFakeBot(mgl32.Vec3{0, 64, 0})
	addMob(f.combatFakeBot, 5, "creeper", 2) // inside the 3-block panic distance
	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)

	cm.Tick(context.Background())

	if cm.InCombat() {
		t.Fatal("InCombat() = true, want the bot to have disengaged from the creeper")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 1 {
		t.Fatalf("NavigateTo calls = %d, want one flee order", len(f.navs))
	}
	want := mgl32.Vec3{-4, 64, 0} // 6 blocks away on the far side
	if f.navs[0] != want {
		t.Fatalf("flee target = %v, want %v", f.navs[0], want)
	}
}

func TestTick_SkeletonInBand_StrafesNotCharges(t *testing.T) {
	t.Parallel()
	f := newRecordingFakeBot(mgl32.Vec3{0, 64, 0})
	addMob(f.combatFakeBot, 5, "skeleton", 9) // inside the [7,12] band
	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)

	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 1 {
		t.Fatalf("NavigateTo calls = %d, want one strafe order", len(f.navs))
	}
	target := mgl32.Vec3{9, 64, 0}
	if f.navs[0] == target {
		t.Fatal("bot charged straight at the skeleton instead of strafing")
	}
	if d := combat.HorizontalDistance(f.navs[0], target); math.Abs(float64(d-9)) > 1e-3 {
		t.Fatalf("strafe keeps distance %v, want 9", d)
	}
}

func TestTick_SkeletonInsideBand_BacksOff(t *testing.T) {
	t.Parallel()
	f := newRecordingFakeBot(mgl32.Vec3{0, 64, 0})
	addMob(f.combatFakeBot, 5, "skeleton", 2) // closer than the 7-block band floor
	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)

	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 1 {
		t.Fatalf("NavigateTo calls = %d, want one back-off order", len(f.navs))
	}
	target := mgl32.Vec3{2, 64, 0}
	if d := combat.HorizontalDistance(f.navs[0], target); math.Abs(float64(d-combat.SkeletonBandMax)) > 1e-3 {
		t.Fatalf("back-off distance = %v, want the band ceiling %v", d, combat.SkeletonBandMax)
	}
}

func TestTick_Enderman_AimsAtFeet(t *testing.T) {
	t.Parallel()
	f := newRecordingFakeBot(mgl32.Vec3{0, 64, 0})
	addMob(f.combatFakeBot, 5, "enderman", 2)
	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)

	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.looks) == 0 {
		t.Fatal("no LookAt issued on the enderman")
	}
	if got := f.looks[0].Y(); math.Abs(float64(got-64.2)) > 1e-3 {
		t.Fatalf("aim height = %v, want the feet at 64.2 (never the head)", got)
	}
}

// The rigidity complaint, stated as a test: the same creeper at the same
// distance used to produce the same answer forever, because the decision was a
// lookup rather than anybody's opinion.
//
// If these three plans ever become equal again, the disposition has stopped
// being a disposition and become a label.
func TestDispositionChangesTheCreeperAnswer(t *testing.T) {
	t.Parallel()

	careful := combat.MobMovePlan("creeper", 2.0, affordance.Careful)
	bold := combat.MobMovePlan("creeper", 2.0, affordance.Bold)
	reckless := combat.MobMovePlan("creeper", 2.0, affordance.Reckless)

	if !careful.Flee || !bold.Flee {
		t.Fatal("neither careful nor bold left a creeper at two blocks")
	}
	if reckless.Flee {
		t.Error("a reckless bot ran from a creeper; the whole point is that it does not")
	}
	if !reckless.HoldGround {
		t.Error("a reckless bot was not told to hold its ground")
	}
}

// TestTheCarefulBotReachesForABlock is the composed defence: put something
// between the body and the blast, then leave. It is the answer that cannot be
// produced by a switch on mob name, because it needs to know what is in hand.
func TestTheCarefulBotReachesForABlock(t *testing.T) {
	t.Parallel()

	plan := combat.MobMovePlan("creeper", 2.0, affordance.Careful)
	if !plan.BlockUp {
		t.Error("a careful bot inside the blast radius did not ask for a block between itself and the creeper")
	}
	if !plan.Flee {
		t.Error("blocking up without leaving is not a defence; both are wanted")
	}
}

// TestTheRecklessBotDoesNotWasteABlock keeps the two answers from collapsing
// into one. A bot that walls itself off and then stands there has neither the
// caution nor the joke.
func TestTheRecklessBotDoesNotWasteABlock(t *testing.T) {
	t.Parallel()

	reckless := combat.MobMovePlan("creeper", 2.0, affordance.Reckless)
	if reckless.BlockUp {
		t.Error("a reckless bot put up a wall; it is standing there on purpose, not hiding")
	}
}

// TestTheAppetiteIsOnlyConsultedForTheMobThatCares pins the scope. Skeleton and
// enderman tactics are geometry, not dispositions — a skeleton arrow does not
// become less dangerous because the bot is in a good mood, so widening the dial
// to every mob would be inventing a personality the world does not support.
func TestTheAppetiteIsOnlyConsultedForTheMobThatCares(t *testing.T) {
	t.Parallel()

	careful := combat.MobMovePlan("skeleton", 9.0, affordance.Careful)
	reckless := combat.MobMovePlan("skeleton", 9.0, affordance.Reckless)

	if careful != reckless {
		t.Errorf("the skeleton tactic changed with the disposition: %+v vs %+v", careful, reckless)
	}
}
