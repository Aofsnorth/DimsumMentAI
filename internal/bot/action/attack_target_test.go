package action

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

type openBlockQuerier struct {
	loaded func(x, y, z int32) bool
}

func (q openBlockQuerier) GetBlockRID(x, y, z int32) (uint32, bool) {
	return 0, q.loaded(x, y, z)
}

func (q openBlockQuerier) IsBlockAir(x, y, z int32) (bool, bool) {
	loaded := q.loaded(x, y, z)
	return loaded, loaded
}

func (q openBlockQuerier) IsBlockSolid(x, y, z int32) (bool, bool) {
	return false, q.loaded(x, y, z)
}

func newVisibilityBot(t *testing.T, origin mgl32.Vec3, actors map[uint64]*entity.Info, loaded func(x, y, z int32) bool) *bot.Bot {
	t.Helper()
	world := pathfinder.NewLocalWorldModel()
	world.SetChunkQuerier(openBlockQuerier{loaded: loaded})

	return &bot.Bot{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:          "TestBot",
		Pos:           origin,
		Actors:        actors,
		PlayerTracker: bot.NewPlayerTracker(),
		WorldModel:    world,
	}
}

func loadedStrip(minX, maxX, yMin, yMax int32) func(x, y, z int32) bool {
	return func(x, y, z int32) bool {
		return x >= minX && x <= maxX && y >= yMin && y <= yMax && z == 0
	}
}

func TestSelectAttackTarget_BarePicksNearestVisibleNonItemMob(t *testing.T) {
	t.Parallel()
	actors := map[uint64]*entity.Info{
		1: {ID: 1, Type: "minecraft:item", Name: "minecraft:beef", Position: mgl32.Vec3{1, 64, 0}, Health: 1},
		2: {ID: 2, Type: "minecraft:cow", Name: "minecraft:cow", Position: mgl32.Vec3{2, 64, 0}, Health: 10},
		3: {ID: 3, Type: "minecraft:pig", Name: "minecraft:pig", Position: mgl32.Vec3{5, 64, 0}, Health: 10},
	}
	b := newVisibilityBot(t, mgl32.Vec3{0, 64, 0}, actors, loadedStrip(0, 8, 64, 66))
	b.PlayerEntityIDs["Requester"] = 90
	b.PlayerUsernames[90] = "Requester"
	b.PlayerPositions[90] = mgl32.Vec3{1, 64, 0}

	target, ok := selectAttackTarget(b, "", "Requester")
	if !ok || target.ID != 2 {
		t.Fatalf("selectAttackTarget() = %#v, %v; want cow ID 2", target, ok)
	}
}

func TestSelectAttackTarget_NamedPicksNearestVisibleMatch(t *testing.T) {
	t.Parallel()
	actors := map[uint64]*entity.Info{
		1: {ID: 1, Type: "minecraft:cow", Name: "minecraft:cow", Position: mgl32.Vec3{6, 64, 0}, Health: 10},
		2: {ID: 2, Type: "minecraft:sheep", Name: "minecraft:sheep", Position: mgl32.Vec3{1, 64, 0}, Health: 10},
		3: {ID: 3, Type: "minecraft:cow", Name: "minecraft:cow", Position: mgl32.Vec3{3, 64, 0}, Health: 10},
	}
	b := newVisibilityBot(t, mgl32.Vec3{0, 64, 0}, actors, loadedStrip(0, 8, 64, 66))

	target, ok := selectAttackTarget(b, "cow", "user")
	if !ok || target.ID != 3 {
		t.Fatalf("selectAttackTarget(cow) = %#v, %v; want nearest cow ID 3", target, ok)
	}
}

func TestSelectAttackTarget_RejectsBlockedAndUnloadedLOS(t *testing.T) {
	t.Parallel()
	actors := map[uint64]*entity.Info{
		1: {ID: 1, Type: "minecraft:cow", Name: "minecraft:cow", Position: mgl32.Vec3{4, 64, 0}, Health: 10},
	}
	b := newVisibilityBot(t, mgl32.Vec3{0, 64, 0}, actors, loadedStrip(0, 8, 64, 66))

	if _, ok := selectAttackTarget(b, "cow", "user"); !ok {
		t.Fatal("selectAttackTarget() with open LOS = false, want true")
	}

	unloaded := newVisibilityBot(t, mgl32.Vec3{0, 64, 0}, actors, func(x, y, z int32) bool {
		return loadedStrip(0, 8, 64, 66)(x, y, z) && x != 2
	})
	if _, ok := selectAttackTarget(unloaded, "cow", "user"); ok {
		t.Fatal("selectAttackTarget() through unloaded cell = true, want false")
	}
}

func TestSelectAttackTarget_SkipsRequestingPlayer(t *testing.T) {
	t.Parallel()
	actors := map[uint64]*entity.Info{
		7: {ID: 7, Type: "minecraft:player", Name: "Requester", Position: mgl32.Vec3{1, 64, 0}, Health: 20},
		8: {ID: 8, Type: "minecraft:zombie", Name: "minecraft:zombie", Position: mgl32.Vec3{4, 64, 0}, Health: 20},
	}
	b := newVisibilityBot(t, mgl32.Vec3{0, 64, 0}, actors, loadedStrip(0, 8, 64, 66))
	b.PlayerEntityIDs["Requester"] = 7
	b.PlayerUsernames[7] = "Requester"
	b.PlayerPositions[7] = mgl32.Vec3{1, 64, 0}

	target, ok := selectAttackTarget(b, "", "Requester")
	if !ok || target.ID != 8 {
		t.Fatalf("selectAttackTarget() = %#v, %v; want zombie ID 8, not requester", target, ok)
	}
}

func TestHandleAttack_DoesNotEngagePlayerTarget(t *testing.T) {
	t.Parallel()
	actors := map[uint64]*entity.Info{
		7: {ID: 7, Type: "minecraft:player", Name: "Steve", Position: mgl32.Vec3{1, 64, 0}, Health: 20},
	}
	b := newVisibilityBot(t, mgl32.Vec3{0, 64, 0}, actors, loadedStrip(0, 8, 64, 66))
	b.PlayerEntityIDs["Steve"] = 7
	b.PlayerUsernames[7] = "Steve"
	b.PlayerPositions[7] = mgl32.Vec3{1, 64, 0}

	// Normal attack with a player name must not engage anyone; PVP routing is
	// the only player-targeting path and is tested in the combat package.
	if _, ok := selectAttackTarget(b, "steve", "user"); ok {
		t.Fatal("selectAttackTarget(steve) engaged a player; want mob-only selection")
	}
}
