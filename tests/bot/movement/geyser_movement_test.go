package movement_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/bot/world"

	"github.com/df-mc/dragonfly/server/block/cube"
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

func TestTurnPathAdvanceDistAllowsCornerProgress(t *testing.T) {
	t.Parallel()

	// Path with turn at index 1: (76,73,-54) -> (75,73,-55) -> (75,73,-56)
	b := &bot.Bot{
		Pos: mgl32.Vec3{75.8, 73.0, -55.2},
		CurrentPath: []pathfinder.Node{
			{X: 76, Y: 73, Z: -54},
			{X: 75, Y: 73, Z: -55},
			{X: 75, Y: 73, Z: -56},
		},
		PathIndex:     1,
		MovementState: "walk_to",
	}

	// Distance to node 1 center (75.5, 73.0, -55.5) from (75.8, 73.0, -55.2) is ~0.42
	dx := float32(75.5 - 75.8)
	dz := float32(-55.5 - (-55.2))
	dist := float32(0.424)

	tc := &movement.TickContext{
		B:          b,
		HasPath:    true,
		CurrPos:    b.Pos,
		NextTarget: mgl32.Vec3{75.5, 73.0, -55.5},
		Dx:         dx,
		Dz:         dz,
		Dist:       dist,
		MState:     "walk_to",
	}

	tc.AdvancePathOrArriveForTest()

	b.Mu.Lock()
	idx := b.PathIndex
	b.Mu.Unlock()

	if idx != 2 {
		t.Fatalf("expected PathIndex to advance to 2 around corner, stayed at %d", idx)
	}
}

func TestHasGroundSupportAtRejectsNonSolidFoliageAndAir(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		WorldModel: pathfinder.NewLocalWorldModel(),
		WorldCache: world.NewWorldCache(0, cube.Range{-64, 319}, nil),
	}
	b.WorldCache.SetGeyserCompat(true)
	b.WorldCache.SetUseBlockNetworkIDHashes(false)
	b.WorldModel.SetChunkQuerier(b.WorldCache)

	tc := &movement.TickContext{B: b}

	// 1. Non-solid foliage (short_grass, RID 16851 in Geyser Bedrock 1.21).
	// Under the old bug (!isAir fallback), short_grass was treated as solid ground.
	b.WorldCache.SetBlockRID(10, 73, 10, 16851)
	if tc.HasGroundSupportAtForTest(10.5, 10.5, 74) {
		t.Fatal("short_grass at supportY must NOT provide ground support")
	}

	// 2. Air (RID 17025 in the plain embedded palette; no custom blocks here).
	b.WorldCache.SetBlockRID(10, 73, 10, 17025)
	if tc.HasGroundSupportAtForTest(10.5, 10.5, 74) {
		t.Fatal("air at supportY must NOT provide ground support")
	}

	// 3. Solid floor (RID 20236 in Geyser Bedrock 1.21).
	b.WorldCache.SetBlockRID(10, 73, 10, 20236)
	if !tc.HasGroundSupportAtForTest(10.5, 10.5, 74) {
		t.Fatal("solid block at supportY MUST provide ground support")
	}
}

func TestDescentTriggeredWhenSteppingOverNonSolidFoliage(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		WorldModel: pathfinder.NewLocalWorldModel(),
		WorldCache: world.NewWorldCache(0, cube.Range{-64, 319}, nil),
		Pos:        mgl32.Vec3{10.5, 74.0, 10.5},
		CurrentPath: []pathfinder.Node{
			{X: 10, Y: 73, Z: 11},
		},
		PathIndex:     0,
		MovementState: "walk_to",
	}
	b.WorldCache.SetGeyserCompat(true)
	b.WorldCache.SetUseBlockNetworkIDHashes(false)
	b.WorldModel.SetChunkQuerier(b.WorldCache)

	// Foliage (short_grass) at support level Y=73.
	b.WorldCache.SetBlockRID(10, 73, 10, 16851)
	b.WorldCache.SetBlockRID(10, 73, 11, 16851)

	tc := &movement.TickContext{
		B:                 b,
		HasPath:           true,
		HasHorizontalMove: true,
		CurrPos:           b.Pos,
		NextTarget:        mgl32.Vec3{10.5, 73.0, 11.5},
		Dx:                0,
		Dz:                1.0,
		Dist:              1.0,
		NextY:             74.0,
		MState:            "walk_to",
	}

	// Moving forward over non-solid foliage must trigger controlled descent.
	tc.ComputeTargetPositionForTest(0.2, false, false)
	if tc.NextY >= 74.0 {
		t.Fatalf("tc.NextY = %v, want < 74.0 (descent must lower Y when stepping over non-solid foliage)", tc.NextY)
	}

	// If the support level at Y=73 were instead solid ground, descent must NOT trigger.
	b.WorldCache.SetBlockRID(10, 73, 10, 20236)
	b.WorldCache.SetBlockRID(10, 73, 11, 20236)

	tcSolid := &movement.TickContext{
		B:                 b,
		HasPath:           true,
		HasHorizontalMove: true,
		CurrPos:           b.Pos,
		NextTarget:        mgl32.Vec3{10.5, 73.0, 11.5},
		Dx:                0,
		Dz:                1.0,
		Dist:              1.0,
		NextY:             74.0,
		MState:            "walk_to",
	}

	tcSolid.ComputeTargetPositionForTest(0.2, false, false)
	if tcSolid.NextY != 74.0 {
		t.Fatalf("tcSolid.NextY = %v, want 74.0", tcSolid.NextY)
	}
}

func TestMultiTickStepDownDoesNotSnapBackUp(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		WorldModel: pathfinder.NewLocalWorldModel(),
		WorldCache: world.NewWorldCache(0, cube.Range{-64, 319}, nil),
		Pos:        mgl32.Vec3{10.5, 74.0, 10.95},
		CurrentPath: []pathfinder.Node{
			{X: 10, Y: 74, Z: 10},
			{X: 10, Y: 73, Z: 11},
		},
		PathIndex:     1,
		MovementState: "walk_to",
	}
	b.WorldCache.SetGeyserCompat(true)
	b.WorldCache.SetUseBlockNetworkIDHashes(false)
	b.WorldModel.SetChunkQuerier(b.WorldCache)

	// Step at Y=73 (floor top at 74.0)
	b.WorldCache.SetBlockRID(10, 73, 10, 20236)
	// Lower ground at Y=72 (floor top at 73.0)
	b.WorldCache.SetBlockRID(10, 72, 11, 20236)

	// Tick 1: Bot takes step forward over the edge into cell 11 (targetZ = 11.15)
	tc1 := &movement.TickContext{
		B:                 b,
		HasPath:           true,
		HasHorizontalMove: true,
		CurrPos:           b.Pos,
		NextTarget:        mgl32.Vec3{10.5, 73.0, 11.5},
		Dx:                0,
		Dz:                11.5 - 10.95,
		Dist:              11.5 - 10.95,
		NextY:             74.0,
		MState:            "walk_to",
	}

	targetX, targetZ := tc1.ComputeTargetPositionForTest(0.2, false, false)
	t.Logf("Tick 1 target pos: (%v, %v, %v), NextY: %v", targetX, tc1.NextY, targetZ, tc1.NextY)
	if tc1.NextY >= 74.0 {
		t.Fatalf("Tick 1 NextY = %v, want < 74.0", tc1.NextY)
	}

	// Tick 2: Physics runs with position from Tick 1
	b.Pos = mgl32.Vec3{targetX, tc1.NextY, targetZ}
	tc2 := &movement.TickContext{
		B:                 b,
		HasPath:           true,
		HasHorizontalMove: true,
		CurrPos:           b.Pos,
		NextY:             b.Pos.Y(),
		NextTarget:        mgl32.Vec3{10.5, 73.0, 11.5},
		Dx:                0,
		Dz:                11.5 - targetZ,
		Dist:              11.5 - targetZ,
		MState:            "walk_to",
	}

	tc2.RunPhysicsAndCollisionsForTest()
	t.Logf("Tick 2 after physics: CurrPos.Y = %v, NextY = %v, isGrounded = %v, isDescending = %v",
		tc2.CurrPos.Y(), tc2.NextY, tc2.IsGrounded, tc2.IsDescending)

	if tc2.NextY > tc1.NextY {
		t.Fatalf("Tick 2 physics snapped NextY up from %v to %v (bot floated back up!)", tc1.NextY, tc2.NextY)
	}
}

func TestDescentNodeDoesNotAdvancePathUntilLanded(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Pos:    mgl32.Vec3{10.5, 74.0, 11.2},
		CurrentPath: []pathfinder.Node{
			{X: 10, Y: 74, Z: 10},
			{X: 10, Y: 73, Z: 11},
		},
		PathIndex:     1,
		MovementState: "walk_to",
	}

	// While still floating at Y=74.0 and close horizontally (< 0.55m):
	tcFloating := &movement.TickContext{
		B:          b,
		HasPath:    true,
		CurrPos:    b.Pos,
		NextTarget: mgl32.Vec3{10.5, 73.0, 11.5},
		Dx:         0,
		Dz:         0.3,
		Dist:       0.3,
		MState:     "walk_to",
	}

	tcFloating.AdvancePathForTest(2.5)

	b.Mu.Lock()
	idx := b.PathIndex
	pathLen := len(b.CurrentPath)
	b.Mu.Unlock()

	if idx != 1 || pathLen != 2 {
		t.Fatalf("path advanced prematurely while floating at Y=74! PathIndex=%d, pathLen=%d", idx, pathLen)
	}

	// Once the bot descends to Y=73.0 (landed on lower block):
	b.Pos = mgl32.Vec3{10.5, 73.0, 11.4}
	tcLanded := &movement.TickContext{
		B:          b,
		HasPath:    true,
		CurrPos:    b.Pos,
		NextTarget: mgl32.Vec3{10.5, 73.0, 11.5},
		Dx:         0,
		Dz:         0.1,
		Dist:       0.1,
		MState:     "walk_to",
	}

	tcLanded.AdvancePathForTest(2.5)

	b.Mu.Lock()
	idxLanded := b.PathIndex
	remainingPath := b.CurrentPath
	b.Mu.Unlock()

	if idxLanded != 0 || remainingPath != nil {
		t.Fatalf("expected path to complete after landing on Y=73, got PathIndex=%d, path=%v", idxLanded, remainingPath)
	}
}
