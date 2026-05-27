package gathering

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func TestPlanMineStepUsesVisibleRequestedBlock(t *testing.T) {
	world := newTestMineWorld()
	world.setSolid(1, 64, 0, true)

	step, ok := planMineStep(world, mgl32.Vec3{0.5, 64, 0.5}, protocol.BlockPos{1, 64, 0})
	if !ok {
		t.Fatal("planMineStep() did not find a visible step")
	}
	if step.Position != (protocol.BlockPos{1, 64, 0}) {
		t.Fatalf("step.Position = %v, want target block", step.Position)
	}
	if step.CountsTowardTarget != true {
		t.Fatal("visible requested block should count toward target")
	}
}

func TestPlanMineStepBreaksObstructionBeforeCoveredBlock(t *testing.T) {
	world := newTestMineWorld()
	world.setSolid(1, 64, 0, true)
	world.setSolid(2, 64, 0, true)

	step, ok := planMineStep(world, mgl32.Vec3{0.5, 64, 0.5}, protocol.BlockPos{2, 64, 0})
	if !ok {
		t.Fatal("planMineStep() did not find an access step")
	}
	if step.Position != (protocol.BlockPos{1, 64, 0}) {
		t.Fatalf("step.Position = %v, want front obstruction", step.Position)
	}
	if step.CountsTowardTarget {
		t.Fatal("obstruction should not count as collected target")
	}
}

func TestPlanMineStepRejectsFullyCoveredAdjacentBlock(t *testing.T) {
	world := newTestMineWorld()
	world.setSolid(1, 64, 0, true)
	world.setSolid(1, 65, 0, true)
	world.setSolid(1, 63, 0, true)
	world.setSolid(0, 64, 0, true)
	world.setSolid(2, 64, 0, true)
	world.setSolid(1, 64, -1, true)
	world.setSolid(1, 64, 1, true)

	if step, ok := planMineStep(world, mgl32.Vec3{0.5, 64, 0.5}, protocol.BlockPos{1, 64, 0}); ok {
		t.Fatalf("planMineStep() = %+v, want no reachable visible step", step)
	}
}

func TestInventoryCountMatchesRequestedItem(t *testing.T) {
	inv := map[uint32]protocol.ItemStack{
		0: {ItemType: protocol.ItemType{NetworkID: 1}, Count: 2},
		1: {ItemType: protocol.ItemType{NetworkID: 2}, Count: 4},
		2: {ItemType: protocol.ItemType{NetworkID: 3}, Count: 1},
	}
	names := map[int32]string{
		1: "minecraft:dirt",
		2: "minecraft:coarse_dirt",
		3: "minecraft:stone",
	}

	if got := inventoryCountMatching(inv, names, "dirt"); got != 6 {
		t.Fatalf("inventoryCountMatching() = %d, want 6", got)
	}
}

type testMineWorld struct {
	solid map[protocol.BlockPos]bool
}

func newTestMineWorld() *testMineWorld {
	return &testMineWorld{solid: make(map[protocol.BlockPos]bool)}
}

func (w *testMineWorld) setSolid(x, y, z int32, solid bool) {
	w.solid[protocol.BlockPos{x, y, z}] = solid
}

func (w *testMineWorld) IsSolid(x, y, z int32) bool {
	return w.solid[protocol.BlockPos{x, y, z}]
}

func (w *testMineWorld) SetSolid(x, y, z int32, solid bool) {
	w.setSolid(x, y, z, solid)
}

func (w *testMineWorld) IsHazard(x, y, z int32) bool {
	return false
}

func (w *testMineWorld) SetHazard(x, y, z int32, hazard bool) {}
