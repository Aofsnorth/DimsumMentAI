package movement

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/pathfinder"
	"github.com/go-gl/mathgl/mgl32"
)

func TestPerformActiveSteeringDoesNotJumpOnLevelTopSegment(t *testing.T) {
	b := movementTestBot([]pathfinder.Node{
		{X: 0, Y: 64, Z: 0},
		{X: 1, Y: 65, Z: 0},
		{X: 2, Y: 65, Z: 0},
	}, 2)

	tc := &TickContext{
		B:                   b,
		CurrPos:             mgl32.Vec3{1.5, 64.8, 0.5},
		MState:              "walk_to",
		TPos:                mgl32.Vec3{2.5, 65, 0.5},
		Yaw:                 -90,
		HasPath:             true,
		NextTarget:          mgl32.Vec3{2.5, 65, 0.5},
		Dx:                  1,
		Dz:                  0,
		Dist:                1,
		AllowDirectSteering: true,
	}

	tc.performActiveSteering()

	if tc.ShouldJump {
		t.Fatalf("expected no jump on level top segment, got jump: %s", tc.JumpReason)
	}
}

func TestPerformActiveSteeringJumpsOnAscendingSegment(t *testing.T) {
	b := movementTestBot([]pathfinder.Node{
		{X: 0, Y: 64, Z: 0},
		{X: 1, Y: 65, Z: 0},
		{X: 2, Y: 65, Z: 0},
	}, 1)

	tc := &TickContext{
		B:                   b,
		CurrPos:             mgl32.Vec3{0.5, 64, 0.5},
		MState:              "walk_to",
		TPos:                mgl32.Vec3{2.5, 65, 0.5},
		Yaw:                 -90,
		HasPath:             true,
		NextTarget:          mgl32.Vec3{1.5, 65, 0.5},
		Dx:                  1,
		Dz:                  0,
		Dist:                1,
		AllowDirectSteering: true,
	}

	tc.performActiveSteering()

	if !tc.ShouldJump {
		t.Fatal("expected jump on ascending path segment")
	}
}

func movementTestBot(path []pathfinder.Node, pathIndex int) *bot.Bot {
	return &bot.Bot{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		MovementState: "walk_to",
		TargetPos:     mgl32.Vec3{2.5, 65, 0.5},
		WorldModel:    pathfinder.NewLocalWorldModel(),
		CurrentPath:   path,
		PathIndex:     pathIndex,
		LastTickPos:   mgl32.Vec3{-10, 64, -10},
		Yaw:           -90,
	}
}
