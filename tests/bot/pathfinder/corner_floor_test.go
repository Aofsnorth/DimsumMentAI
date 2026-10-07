package pathfinder_test

import (
	"testing"

	"bedrock-ai/internal/bot/pathfinder"
)

func TestDiagonalMovementRequiresAdjacentFloorSupport(t *testing.T) {
	t.Parallel()

	start := pathfinder.Node{X: 0, Y: 1, Z: 0}
	target := pathfinder.Node{X: 1, Y: 1, Z: 1}

	// Case 1: L-shaped floor where (1, 0, 0) has floor, but (0, 0, 1) does NOT.
	// Diagonal movement from (0, 1, 0) to (1, 1, 1) should be disallowed so the bot
	// routes cardinally via (1, 1, 0) instead of clipping the corner gap.
	floors1 := [][3]int32{
		{0, 0, 0}, // start floor
		{1, 0, 0}, // adjacent 1 has floor
		// {0, 0, 1} is missing (hole/chasm)
		{1, 0, 1}, // target floor
	}
	model1 := worldWithFloors(start, target, floors1)

	neighbors1 := model1.GetNeighbors(start)
	for _, n := range neighbors1 {
		if n.X == 1 && n.Z == 1 && n.LinkType == pathfinder.LinkWalk {
			t.Errorf("diagonal walk neighbor %+v should NOT be generated when adjacent cell has no floor", n)
		}
	}

	// Case 2: 2x2 solid square where both (1, 0, 0) and (0, 0, 1) have floor.
	// Diagonal movement is safe here.
	floors2 := [][3]int32{
		{0, 0, 0},
		{1, 0, 0},
		{0, 0, 1},
		{1, 0, 1},
	}
	model2 := worldWithFloors(start, target, floors2)

	neighbors2 := model2.GetNeighbors(start)
	hasDiagonal := false
	for _, n := range neighbors2 {
		if n.X == 1 && n.Z == 1 && n.LinkType == pathfinder.LinkWalk {
			hasDiagonal = true
			break
		}
	}
	if !hasDiagonal {
		t.Errorf("diagonal walk neighbor should be generated when both adjacent cells have floors")
	}
}

func TestSmoothingRejectsDiagonalAcrossMissingFloor(t *testing.T) {
	t.Parallel()

	from := pathfinder.Node{X: 0, Y: 1, Z: 0, LinkType: pathfinder.LinkWalk}
	to := pathfinder.Node{X: 1, Y: 1, Z: 1, LinkType: pathfinder.LinkWalk}

	// Floor exists only under 'from' and 'to'. Corners (1, 0, 0) and (0, 0, 1) are holes.
	floors := [][3]int32{
		{0, 0, 0},
		{1, 0, 1},
	}
	model := worldWithFloors(from, to, floors)

	if pathfinder.CanWalkDirectly(from, to, model) {
		t.Errorf("CanWalkDirectly should reject diagonal smoothing across missing corner floors")
	}
}

func TestStepJumpOutFromStartNodeWithoutSolidFloor(t *testing.T) {
	t.Parallel()

	// Bot is in a 1-block pit at (0, 1, 0).
	// Under its feet at (0, 0, 0) is a non-solid block (e.g. hanging sign).
	// Adjacent block at (1, 1, 0) is solid ground with air above it at (1, 2, 0).
	start := pathfinder.Node{X: 0, Y: 1, Z: 0}
	target := pathfinder.Node{X: 1, Y: 2, Z: 0}

	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(start, target)
	// (1, 1, 0) is solid (ledge block). Top is at Y=2.
	model.SetSolid(1, 1, 0, true)

	neighbors := model.GetNeighbors(start)
	hasStepJump := false
	for _, n := range neighbors {
		if n.X == 1 && n.Y == 2 && n.Z == 0 && n.LinkType == pathfinder.LinkStepJump {
			hasStepJump = true
			break
		}
	}
	if !hasStepJump {
		t.Errorf("expected step jump to (1, 2, 0) from start node in pit, got neighbors: %+v", neighbors)
	}
}
