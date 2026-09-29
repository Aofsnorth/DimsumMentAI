package combat

import (
	"context"
	"math"
	"sync"
	"testing"

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

	creeper := mobMovePlan("creeper", 2)
	if !creeper.flee || creeper.safeDistance != creeperSafeDistance {
		t.Fatalf("creeper at 2 = %+v, want flee to %v", creeper, creeperSafeDistance)
	}
	if got := mobMovePlan("creeper", 5); got.flee {
		t.Fatalf("creeper at 5 = %+v, want to stand and fight", got)
	}

	skel := mobMovePlan("skeleton", 9)
	if !skel.strafe || skel.bandMin != skeletonBandMin || skel.bandMax != skeletonBandMax {
		t.Fatalf("skeleton = %+v, want strafe in band [%v,%v]", skel, skeletonBandMin, skeletonBandMax)
	}
	if got := mobMovePlan("stray", 9); !got.strafe {
		t.Fatalf("stray = %+v, want the same strafe as a skeleton", got)
	}

	end := mobMovePlan("enderman", 5)
	if end.look != LookFeet {
		t.Fatalf("enderman = %+v, want LookFeet", end)
	}

	if got := mobMovePlan("zombie", 5); got.flee || got.strafe || got.look != LookCenter {
		t.Fatalf("zombie = %+v, want the plain melee default", got)
	}
}

func TestRetreatAndStrafeGeometry(t *testing.T) {
	t.Parallel()

	bot := mgl32.Vec3{0, 64, 0}
	threat := mgl32.Vec3{2, 64, 0}

	retreat := retreatPoint(bot, threat, 6)
	if retreat.X() != -4 || retreat.Z() != 0 {
		t.Fatalf("retreatPoint = %v, want (-4,64,0)", retreat)
	}
	if d := horizontalDistance(retreat, threat); math.Abs(float64(d-6)) > 1e-3 {
		t.Fatalf("retreat distance = %v, want 6", d)
	}

	// Standing exactly on the threat must still yield a point the safe
	// distance away rather than a division by zero.
	overlap := retreatPoint(threat, threat, 6)
	if d := horizontalDistance(overlap, threat); math.Abs(float64(d-6)) > 1e-3 {
		t.Fatalf("overlap retreat distance = %v, want 6", d)
	}

	strafe := strafePoint(bot, threat, 9, 1)
	if d := horizontalDistance(strafe, threat); math.Abs(float64(d-9)) > 1e-3 {
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
	cm := NewCombatManager(f, f.logger)
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
	cm := NewCombatManager(f, f.logger)
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
	if d := horizontalDistance(f.navs[0], target); math.Abs(float64(d-9)) > 1e-3 {
		t.Fatalf("strafe keeps distance %v, want 9", d)
	}
}

func TestTick_SkeletonInsideBand_BacksOff(t *testing.T) {
	t.Parallel()
	f := newRecordingFakeBot(mgl32.Vec3{0, 64, 0})
	addMob(f.combatFakeBot, 5, "skeleton", 2) // closer than the 7-block band floor
	cm := NewCombatManager(f, f.logger)
	cm.EngageTarget(5)

	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 1 {
		t.Fatalf("NavigateTo calls = %d, want one back-off order", len(f.navs))
	}
	target := mgl32.Vec3{2, 64, 0}
	if d := horizontalDistance(f.navs[0], target); math.Abs(float64(d-skeletonBandMax)) > 1e-3 {
		t.Fatalf("back-off distance = %v, want the band ceiling %v", d, skeletonBandMax)
	}
}

func TestTick_Enderman_AimsAtFeet(t *testing.T) {
	t.Parallel()
	f := newRecordingFakeBot(mgl32.Vec3{0, 64, 0})
	addMob(f.combatFakeBot, 5, "enderman", 2)
	cm := NewCombatManager(f, f.logger)
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
