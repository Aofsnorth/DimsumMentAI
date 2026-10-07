package action_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
)

type dummyBotForCombat struct {
	*bot.Bot
	actors map[uint64]*entity.Info
}

func (d *dummyBotForCombat) GetCoords() mgl32.Vec3                { return d.Pos }
func (d *dummyBotForCombat) GetEntities() map[uint64]*entity.Info { return d.actors }
func (d *dummyBotForCombat) FindPlayer(string) (uint64, mgl32.Vec3, bool) {
	return 0, mgl32.Vec3{}, false
}
func (d *dummyBotForCombat) StopMovement() {}

func TestCombatStopActions(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	setupBot := func() *bot.Bot {
		b := &bot.Bot{
			Logger:              logger,
			RecentBotMessages:   make(map[string]time.Time),
			RecentStatusReports: make(map[string]time.Time),
			Pos:                 mgl32.Vec3{0, 64, 0},
			Actors:              make(map[uint64]*entity.Info),
		}
		dummy := &dummyBotForCombat{Bot: b, actors: map[uint64]*entity.Info{
			99: {ID: 99, Type: "minecraft:zombie", Name: "zombie", Position: mgl32.Vec3{2, 64, 0}, Health: 20},
		}}
		cm := combat.NewCombatManager(dummy, logger)
		cm.EngageTarget(99)
		b.CombatMgr = cm
		return b
	}

	t.Run("stop action disengages combat", func(t *testing.T) {
		t.Parallel()
		b := setupBot()
		if !b.CombatMgr.InCombat() {
			t.Fatal("expected InCombat() = true before stop")
		}
		action.Execute(b, "stop", "", "player")
		if b.CombatMgr.InCombat() {
			t.Fatal("expected InCombat() = false after stop")
		}
	})

	t.Run("stay action disengages combat", func(t *testing.T) {
		t.Parallel()
		b := setupBot()
		if !b.CombatMgr.InCombat() {
			t.Fatal("expected InCombat() = true before stay")
		}
		action.Execute(b, "stay", "", "player")
		if b.CombatMgr.InCombat() {
			t.Fatal("expected InCombat() = false after stay")
		}
	})

	t.Run("disengage action disengages combat", func(t *testing.T) {
		t.Parallel()
		b := setupBot()
		if !b.CombatMgr.InCombat() {
			t.Fatal("expected InCombat() = true before disengage")
		}
		action.Execute(b, "disengage", "", "player")
		if b.CombatMgr.InCombat() {
			t.Fatal("expected InCombat() = false after disengage")
		}
	})

	t.Run("attack:stop disengages combat", func(t *testing.T) {
		t.Parallel()
		b := setupBot()
		if !b.CombatMgr.InCombat() {
			t.Fatal("expected InCombat() = true before attack:stop")
		}
		action.Execute(b, "attack", "stop", "player")
		if b.CombatMgr.InCombat() {
			t.Fatal("expected InCombat() = false after attack:stop")
		}
	})

	t.Run("attack:berhenti disengages combat", func(t *testing.T) {
		t.Parallel()
		b := setupBot()
		if !b.CombatMgr.InCombat() {
			t.Fatal("expected InCombat() = true before attack:berhenti")
		}
		action.Execute(b, "attack", "berhenti", "player")
		if b.CombatMgr.InCombat() {
			t.Fatal("expected InCombat() = false after attack:berhenti")
		}
	})
}
