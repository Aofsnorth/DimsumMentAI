package gathering_test

import (
	"testing"

	"bedrock-ai/internal/bot/gathering"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type mineWorldCell struct {
	loaded bool
	solid  bool
}

type testMineWorld struct {
	cells map[protocol.BlockPos]mineWorldCell
}

func (w testMineWorld) IsSolid(x, y, z int32) bool {
	cell := w.cells[protocol.BlockPos{x, y, z}]
	return cell.loaded && cell.solid
}

func (w testMineWorld) IsLoaded(x, y, z int32) bool {
	return w.cells[protocol.BlockPos{x, y, z}].loaded
}

func TestPlanMineStepRejectsHiddenBlock(t *testing.T) {
	target := protocol.BlockPos{0, 0, 0}
	world := testMineWorld{cells: map[protocol.BlockPos]mineWorldCell{}}
	for x := int32(-1); x <= 1; x++ {
		for y := int32(-1); y <= 1; y++ {
			for z := int32(-1); z <= 1; z++ {
				world.cells[protocol.BlockPos{x, y, z}] = mineWorldCell{loaded: true, solid: true}
			}
		}
	}

	if step, ok := gathering.PlanMineStep(world, mgl32.Vec3{3, 0, 0}, target); ok {
		t.Fatalf("hidden block accepted: %+v", step)
	}
}

func TestPlanMineStepAcceptsExposedVisibleBlock(t *testing.T) {
	target := protocol.BlockPos{0, 0, 0}
	world := testMineWorld{cells: map[protocol.BlockPos]mineWorldCell{
		target:                     {loaded: true, solid: true},
		protocol.BlockPos{1, 0, 0}: {loaded: true},
		protocol.BlockPos{2, 0, 0}: {loaded: true},
		protocol.BlockPos{1, 1, 0}: {loaded: true},
		protocol.BlockPos{2, 1, 0}: {loaded: true},
	}}

	step, ok := gathering.PlanMineStep(world, mgl32.Vec3{2, 0, 0}, target)
	if !ok {
		t.Fatal("exposed visible block rejected")
	}
	if step.Position != target {
		t.Fatalf("Position = %v, want %v", step.Position, target)
	}
	if step.Face != 5 {
		t.Fatalf("Face = %d, want exposed east face 5", step.Face)
	}
	if step.Aim != (mgl32.Vec3{1, 0.5, 0.5}) {
		t.Fatalf("Aim = %v, want exposed face center", step.Aim)
	}
	if !step.CountsTowardTarget {
		t.Fatal("visible target candidate did not count toward mining target")
	}
}

func TestPlanMineStepRejectsUnknownExposure(t *testing.T) {
	target := protocol.BlockPos{0, 0, 0}
	world := testMineWorld{cells: map[protocol.BlockPos]mineWorldCell{
		target: {loaded: true, solid: true},
	}}

	if step, ok := gathering.PlanMineStep(world, mgl32.Vec3{2, 0, 0}, target); ok {
		t.Fatalf("unknown exposure accepted: %+v", step)
	}
}

func TestPlanMineStepRejectsUnknownSightCell(t *testing.T) {
	target := protocol.BlockPos{0, 0, 0}
	world := testMineWorld{cells: map[protocol.BlockPos]mineWorldCell{
		target:                     {loaded: true, solid: true},
		protocol.BlockPos{1, 0, 0}: {loaded: true},
	}}

	if step, ok := gathering.PlanMineStep(world, mgl32.Vec3{2, 0, 0}, target); ok {
		t.Fatalf("unknown sight-line cell accepted: %+v", step)
	}
}

func TestPlanMineStepRejectsUnknownTarget(t *testing.T) {
	world := testMineWorld{cells: map[protocol.BlockPos]mineWorldCell{}}

	if step, ok := gathering.PlanMineStep(world, mgl32.Vec3{2, 0, 0}, protocol.BlockPos{}); ok {
		t.Fatalf("unloaded target accepted: %+v", step)
	}
}
