package combat_test

import (
	"context"
	"testing"

	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
)

// Guard mode is the standing order "kalo ada mob jahat, bunuh aja ya, jangan
// didiemin". Off — the default — a distant hostile only earns an alert; on, the
// scan hunts for the mob that most endangers the protected player and engages
// it, which is what these tests pin.
func addZombie(f *combatFakeBot, id uint64, x float32) {
	f.actors[id] = &entity.Info{ID: id, Type: "minecraft:zombie", Name: "minecraft:zombie", Position: mgl32.Vec3{x, 64, 0}, Health: 20}
}

func newGuardWorld() (*combatFakeBot, *combat.CombatManager, *combat.ThreatDetector) {
	f := newCombatFakeBot(mgl32.Vec3{0, 64, 0}, stripLoaded(-24, 24), noSolid)
	cm := combat.NewCombatManager(f, f.logger)
	td := combat.NewThreatDetector(f, cm, f.logger)
	return f, cm, td
}

// TestGuardIsOffByDefaultAndADistantHostileOnlyAlerts pins the default: without
// anybody asking, a zombie ten blocks away stays unengaged and the bot only
// raises the alert — the "didiemin" behaviour guard mode exists to replace.
func TestGuardIsOffByDefaultAndADistantHostileOnlyAlerts(t *testing.T) {
	t.Parallel()

	f, cm, td := newGuardWorld()
	addZombie(f, 5, 10)

	on, protector := td.GuardMode()
	if on || protector != "" {
		t.Fatalf("guard mode was born on: %v %q", on, protector)
	}

	td.Scan(context.Background())

	if cm.InCombat() {
		t.Fatal("a distant zombie was engaged with guard mode off")
	}
	if len(f.aiEvents) == 0 {
		t.Fatal("the distant zombie did not even raise an alert")
	}
}

// TestGuardModeEngagesTheMobNearTheProtectedPlayer is the headline behaviour:
// with guard on, the zombie drifting near Synterix is attacked even though
// another zombie stands right next to the bot — the scan protects the player,
// not itself.
func TestGuardModeEngagesTheMobNearTheProtectedPlayer(t *testing.T) {
	t.Parallel()

	f, cm, td := newGuardWorld()
	f.players["Synterix"] = struct {
		id  uint64
		pos mgl32.Vec3
	}{id: 77, pos: mgl32.Vec3{12, 64, 0}}
	addZombie(f, 5, 13) // 1 block from Synterix, 13 from the bot
	addZombie(f, 6, 2)  // 2 blocks from the bot

	td.SetGuardMode(true, "Synterix")
	td.Scan(context.Background())

	id, engaged := cm.TargetIDForTest()
	if !engaged {
		t.Fatal("guard mode did not engage the mob near the player")
	}
	if id != 5 {
		t.Fatalf("guard engaged entity %d, want 5 (the zombie beside the player, not the one beside the bot)", id)
	}
	if len(f.aiEvents) != 0 {
		t.Fatalf("an active fight still raised %d alerts, want none", len(f.aiEvents))
	}
}

// TestGuardWithoutThePlayerTrackedFallsBackToTheBot: the protector logged off
// and FindPlayer misses, so the anchor degrades to the bot itself rather than
// leaving the mobs unguarded.
func TestGuardWithoutThePlayerTrackedFallsBackToTheBot(t *testing.T) {
	t.Parallel()

	f, cm, td := newGuardWorld()
	addZombie(f, 5, 3)
	addZombie(f, 6, 9)

	td.SetGuardMode(true, "SotoLamongan")
	td.Scan(context.Background())

	id, engaged := cm.TargetIDForTest()
	if !engaged {
		t.Fatal("guard mode with an untracked protector engaged nothing")
	}
	if id != 5 {
		t.Fatalf("fallback engaged entity %d, want 5 (nearest to the bot)", id)
	}
}

// TestGuardOnlyEngagesInsideItsRadius: a zombie in sight but out of guard
// range is left alone — guard mode widens the reflex, it does not make the bot
// charge across the world.
func TestGuardOnlyEngagesInsideItsRadius(t *testing.T) {
	t.Parallel()

	f, cm, td := newGuardWorld()
	addZombie(f, 5, 14)

	td.SetGuardMode(true, "Synterix")
	td.Scan(context.Background())

	if _, engaged := cm.TargetIDForTest(); engaged {
		t.Fatal("a zombie 14 blocks away was engaged; guard radius is 12")
	}
}

// TestGuardDisableStopsTheEngagement: switching guard off returns to the
// off-mode behaviour — alert, no fight.
func TestGuardDisableStopsTheEngagement(t *testing.T) {
	t.Parallel()

	f, cm, td := newGuardWorld()
	addZombie(f, 5, 10)

	td.SetGuardMode(true, "Synterix")
	td.SetGuardMode(false, "")

	if on, protector := td.GuardMode(); on || protector != "" {
		t.Fatalf("guard state survived the disable: %v %q", on, protector)
	}
	td.Scan(context.Background())

	if cm.InCombat() {
		t.Fatal("guard-off still engaged the zombie")
	}
	if len(f.aiEvents) == 0 {
		t.Fatal("guard-off lost the plain alert behaviour")
	}
}
