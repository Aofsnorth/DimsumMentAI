package entity

import (
	"fmt"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

type visibilityWorld struct {
	solid  map[string]bool
	loaded map[string]bool
}

func (w visibilityWorld) IsSolid(x, y, z int32) bool {
	return w.solid[cellKey(x, y, z)]
}

func (w visibilityWorld) GetBlockName(x, y, z int32) (string, bool) {
	if !w.loaded[cellKey(x, y, z)] {
		return "", false
	}
	return "minecraft:air", true
}

func loadedVisibilityWorld(minX, maxX int32) visibilityWorld {
	w := visibilityWorld{solid: map[string]bool{}, loaded: map[string]bool{}}
	for x := minX; x <= maxX; x++ {
		for y := int32(64); y <= 66; y++ {
			w.loaded[cellKey(x, y, 0)] = true
		}
	}
	return w
}

func cellKey(x, y, z int32) string {
	return fmt.Sprintf("%d,%d,%d", x, y, z)
}

func TestVisibleMobsFiltersAndSortsGroundedActors(t *testing.T) {
	t.Parallel()
	w := loadedVisibilityWorld(0, 8)
	w.solid[cellKey(4, 65, 0)] = true
	actors := map[uint64]*Info{
		1: {ID: 1, Type: "minecraft:item", Name: "minecraft:beef", Position: mgl32.Vec3{1, 64, 0}, Health: 1},
		2: {ID: 2, Type: "minecraft:cow", Name: "minecraft:cow", Position: mgl32.Vec3{2, 64, 0}, Health: 10},
		3: {ID: 3, Type: "minecraft:pig", Name: "minecraft:pig", Position: mgl32.Vec3{5, 64, 0}, Health: 10},
		4: {ID: 4, Type: "minecraft:player", Name: "Requester", Position: mgl32.Vec3{1, 64, 0}, Health: 20},
		5: {ID: 5, Type: "minecraft:sheep", Name: "minecraft:sheep", Position: mgl32.Vec3{3, 64, 0}, Health: 10},
	}

	got := VisibleMobs(w, w, mgl32.Vec3{0, 64, 0}, actors, 16, nil)
	if len(got) != 2 {
		t.Fatalf("VisibleMobs() count = %d, want 2", len(got))
	}
	if got[0].ID != 2 || got[1].ID != 5 {
		t.Fatalf("VisibleMobs() IDs = [%d %d], want [2 5]", got[0].ID, got[1].ID)
	}
}

func TestNearestVisibleMobMatchesCanonicalMinecraftName(t *testing.T) {
	t.Parallel()
	w := loadedVisibilityWorld(0, 5)
	actors := map[uint64]*Info{
		1: {ID: 1, Type: "minecraft:pig", Name: "minecraft:pig", Position: mgl32.Vec3{1, 64, 0}, Health: 10},
		2: {ID: 2, Type: "minecraft:cow", Name: "minecraft:cow", Position: mgl32.Vec3{3, 64, 0}, Health: 10},
	}

	got, ok := NearestVisibleMob(w, w, mgl32.Vec3{0, 64, 0}, actors, 16, "COW", nil)
	if !ok || got.ID != 2 {
		t.Fatalf("NearestVisibleMob(cow) = %#v, %v; want ID 2", got, ok)
	}
}

func TestHasLineOfSightBlocksUnknownCells(t *testing.T) {
	t.Parallel()
	w := loadedVisibilityWorld(0, 3)
	delete(w.loaded, cellKey(1, 65, 0))

	if HasLineOfSight(w, w, mgl32.Vec3{0, 65, 0}, mgl32.Vec3{3, 65, 0}) {
		t.Fatal("HasLineOfSight() = true through unloaded cell")
	}
}

func TestNormalizeNameStripsMinecraftNamespace(t *testing.T) {
	t.Parallel()
	if got := NormalizeName(" MINECRAFT:Zombie "); got != "zombie" {
		t.Fatalf("NormalizeName() = %q, want zombie", got)
	}
}
