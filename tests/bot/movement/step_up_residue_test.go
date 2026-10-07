// Regression for the live Geyser stall at a step_jump node.
//
// The log: bot resting at (79.11578, 73.14003, -54.5) with foot_solid=true and
// cache_solid=true, next path node (78,74,-55,step_jump), and the pair
// "Multiple stuck detections" / "bot wants to move and is not" repeating
// forever. The body sat 0.14 above the floor top (73.0) — sub-block residue
// from a mid-stride server echo — and with the 2cm grounding skin it read as
// airborne every tick. An airborne tick clears every jump request and makes
// the wall scan treat the step block as a wall, so the bot could neither hop
// onto the step nor walk through it: frozen, pressed against the ledge.
package movement_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

func newStepJumpResidueBot() *bot.Bot {
	world := pathfinder.NewLocalWorldModel()
	world.SetSolid(79, 72, -55, true) // floor the bot stands on, top face 73.0
	world.SetSolid(78, 73, -55, true) // the step the path climbs onto, top 74.0
	return &bot.Bot{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:       "TestBot",
		WorldModel: world,
		Pos:        mgl32.Vec3{79.11578, 73.14003, -54.5},
	}
}

// A body hovering just above the surface it stands on must read as grounded,
// or it can never jump again.
func TestResidueFloatAboveFloorReadsGrounded(t *testing.T) {
	t.Parallel()

	b := newStepJumpResidueBot()
	tc := &movement.TickContext{B: b, CurrPos: b.Pos, NextY: b.Pos.Y()}

	tc.UpdateGroundedState()
	if !tc.IsGrounded {
		t.Fatalf("feet at %.5f resting 0.14 above the floor top (73.0) were reported "+
			"airborne; that false airborne state clears every jump request and freezes "+
			"the bot at the step_jump node", tc.CurrPos.Y())
	}
}

// Physics must settle the hovering body onto the floor instead of letting it
// hover there forever.
func TestResidueFloatSettlesOntoTheFloor(t *testing.T) {
	t.Parallel()

	b := newStepJumpResidueBot()
	tc := &movement.TickContext{B: b, CurrPos: b.Pos, NextY: b.Pos.Y()}

	tc.RunPhysicsAndCollisionsForTest()

	if tc.NextY != 73.0 {
		t.Fatalf("physics left the body at %.5f, want snapped onto the 73.0 floor", tc.NextY)
	}
	if !tc.IsGrounded || !b.IsGrounded {
		t.Fatalf("settled body reported grounded=false (tick=%v bot=%v); the next tick would clear its jumps again", tc.IsGrounded, b.IsGrounded)
	}
}

// With the body grounded, a step_jump request must fire an upward impulse.
func TestStepJumpFiresFromSettledFloor(t *testing.T) {
	t.Parallel()

	b := newStepJumpResidueBot()
	tc := &movement.TickContext{
		B:          b,
		CurrPos:    mgl32.Vec3{79.11578, 73.0, -54.5},
		NextY:      73.0,
		IsGrounded: true,
		ShouldJump: true,
		VelY:       0.0,
	}

	tc.ApplyVerticalVelocity()
	if tc.VelY <= 0.0 {
		t.Fatalf("grounded step_jump request produced VelY=%v, want the 0.42 impulse; "+
			"a cleared request is the bot walking into the step forever", tc.VelY)
	}
}
