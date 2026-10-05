package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestGroundedMoveDeltaHasNegativeYForGeyserGroundDetection(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{IsGrounded: true}
	tc := &movement.TickContext{
		B:          b,
		CurrPos:    mgl32.Vec3{10, 64, 10},
		PrevPos:    mgl32.Vec3{10, 64, 10}, // stationary on flat ground
		IsGrounded: true,
	}

	tc.PrepareMoveVectorForTest()

	if tc.MoveDelta.Y() >= 0 {
		t.Fatalf("grounded MoveDelta.Y() = %v, want < 0 (-0.0784) for Geyser ground check", tc.MoveDelta.Y())
	}
	if tc.MoveDelta.Y() != -0.0784 {
		t.Fatalf("grounded MoveDelta.Y() = %v, want -0.0784", tc.MoveDelta.Y())
	}
}

func TestAirborneMoveDeltaPreservesPhysicsVelocity(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{IsGrounded: false}
	tc := &movement.TickContext{
		B:          b,
		CurrPos:    mgl32.Vec3{10, 64.42, 10},
		PrevPos:    mgl32.Vec3{10, 64.0, 10},
		VelY:       0.42,
		ShouldJump: true,
		IsGrounded: false,
	}

	tc.PrepareMoveVectorForTest()

	if tc.MoveDelta.Y() <= 0 {
		t.Fatalf("jumping MoveDelta.Y() = %v, want positive upward delta", tc.MoveDelta.Y())
	}
}

func TestTeleportAckSetsInputFlagHandledTeleport(t *testing.T) {
	t.Parallel()

	tcTeleport := &movement.TickContext{
		Gait:        movement.NewGaitState(),
		TeleportAck: true,
		IsGrounded:  true,
	}
	flagsWithTeleport := tcTeleport.BuildInputDataForTest(false, false)
	if !flagsWithTeleport.Load(packet.InputFlagHandledTeleport) {
		t.Fatal("InputFlagHandledTeleport must be set when TeleportAck is true to confirm server teleport")
	}

	tcNormal := &movement.TickContext{
		Gait:        movement.NewGaitState(),
		TeleportAck: false,
		IsGrounded:  true,
	}
	flagsNormal := tcNormal.BuildInputDataForTest(false, false)
	if flagsNormal.Load(packet.InputFlagHandledTeleport) {
		t.Fatal("InputFlagHandledTeleport must NOT be set on normal non-teleport ticks")
	}
}

func TestGaitFlagsSetDownButtonsAlongsideStateFlags(t *testing.T) {
	t.Parallel()

	tc := &movement.TickContext{
		Gait:       movement.NewGaitState(),
		ShouldJump: true,
		IsGrounded: true,
	}
	flags := tc.BuildInputDataForTest(false, true)

	if !flags.Load(packet.InputFlagJumping) || !flags.Load(packet.InputFlagJumpDown) {
		t.Fatal("jumping must set both InputFlagJumping and InputFlagJumpDown for Geyser input packet")
	}
	if !flags.Load(packet.InputFlagSneaking) || !flags.Load(packet.InputFlagSneakDown) {
		t.Fatal("sneaking must set both InputFlagSneaking and InputFlagSneakDown for Geyser input packet")
	}
}

func TestBotTeleportAckLifecycle(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{}
	targetPos := mgl32.Vec3{87, 74, -54}

	b.SetTeleportAck(targetPos)

	ackPos, ok := b.TakeTeleportAck()
	if !ok {
		t.Fatal("TakeTeleportAck should return true after SetTeleportAck")
	}
	if ackPos != targetPos {
		t.Fatalf("TakeTeleportAck pos = %v, want %v", ackPos, targetPos)
	}

	// Second take must be empty (single-shot consumption)
	_, secondOk := b.TakeTeleportAck()
	if secondOk {
		t.Fatal("TakeTeleportAck should return false on second call")
	}
}

func TestMoveVectorSetsMovementInputFlags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		moveVec   mgl32.Vec2
		wantFlags []int
		unwanted  []int
	}{
		{
			name:      "forward movement sets InputFlagUp",
			moveVec:   mgl32.Vec2{0, 1.0},
			wantFlags: []int{packet.InputFlagUp},
			unwanted:  []int{packet.InputFlagDown, packet.InputFlagLeft, packet.InputFlagRight},
		},
		{
			name:      "backward movement sets InputFlagDown",
			moveVec:   mgl32.Vec2{0, -1.0},
			wantFlags: []int{packet.InputFlagDown},
			unwanted:  []int{packet.InputFlagUp, packet.InputFlagLeft, packet.InputFlagRight},
		},
		{
			name:      "strafe left sets InputFlagLeft",
			moveVec:   mgl32.Vec2{-1.0, 0},
			wantFlags: []int{packet.InputFlagLeft},
			unwanted:  []int{packet.InputFlagRight, packet.InputFlagUp, packet.InputFlagDown},
		},
		{
			name:      "strafe right sets InputFlagRight",
			moveVec:   mgl32.Vec2{1.0, 0},
			wantFlags: []int{packet.InputFlagRight},
			unwanted:  []int{packet.InputFlagLeft, packet.InputFlagUp, packet.InputFlagDown},
		},
		{
			name:      "diagonal forward-right sets Up, Right, and UpRight",
			moveVec:   mgl32.Vec2{0.7, 0.7},
			wantFlags: []int{packet.InputFlagUp, packet.InputFlagRight, packet.InputFlagUpRight},
			unwanted:  []int{packet.InputFlagDown, packet.InputFlagLeft},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := &movement.TickContext{
				B:       &bot.Bot{},
				Gait:    movement.NewGaitState(),
				MoveVec: tc.moveVec,
			}
			flags := ctx.BuildInputDataForTest(false, false)
			for _, flag := range tc.wantFlags {
				if !flags.Load(flag) {
					t.Fatalf("MoveVec %v did not set flag %d", tc.moveVec, flag)
				}
			}
			for _, flag := range tc.unwanted {
				if flags.Load(flag) {
					t.Fatalf("MoveVec %v unexpectedly set flag %d", tc.moveVec, flag)
				}
			}
		})
	}
}

func TestDescentLinkFromYWhenPathIndexZero(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Pos: mgl32.Vec3{87.0, 74.0, -54.0},
		CurrentPath: []pathfinder.Node{
			{X: 86, Y: 73, Z: -54},
		},
		PathIndex: 0,
	}

	tc := &movement.TickContext{
		B:       b,
		HasPath: true,
		CurrPos: b.Pos,
	}

	descentFromY, linkDescends := tc.DescentLinkFromYForTest()
	if !linkDescends {
		t.Fatal("descentLinkFromY returned linkDescends=false for PathIndex=0 descent node")
	}
	if descentFromY != 74 {
		t.Fatalf("descentLinkFromY returned descentFromY=%d, want 74", descentFromY)
	}
}
