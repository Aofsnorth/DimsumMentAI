package movement

import (
	"context"
	"math"
	"time"

	"bedrock-ai/internal/bot"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft"
)

type TickContext struct {
	B                   *bot.Bot
	Tick                uint64
	CurrPos             mgl32.Vec3
	MState              string
	TPlayer             string
	TPos                mgl32.Vec3
	Yaw                 float32
	Pitch               float32
	VelY                float32
	FeetX, FeetY, FeetZ int32
	IsLadderActive      bool
	LadderWallYaw       float32
	DistToPlayer        float32
	HasPath             bool
	NextTarget          mgl32.Vec3
	Dx, Dz, Dist        float32
	ShouldJump          bool
	JumpReason          string
	PrevPos             mgl32.Vec3
	MoveVec             mgl32.Vec2
	MoveDelta           mgl32.Vec3
	AllowDirectSteering bool
	ShouldMove          bool
	PlayerHeightDiff    float32
	HasHorizontalMove   bool
	IsOnLadder          bool
	IsGrounded          bool
	IsDescending        bool
	NextY               float32
	TargetYaw           float32
	TargetPitch         float32
	ActivelyClimbing    bool
	LastPredictedY      float32
	IsParkourJump       bool
}

// SendInputLoop handles the physical updates and steering of the bot
func SendInputLoop(ctx context.Context, b *bot.Bot, gd minecraft.GameData) {
	ticker := time.NewTicker(time.Second / 20) // 20 ticks/sec
	defer ticker.Stop()
	tick := uint64(0)

	b.Mu.Lock()
	initYaw := b.Yaw
	initPitch := b.Pitch
	initPos := b.Pos
	b.Mu.Unlock()

	_ = initYaw
	_ = initPitch

	var lastPredictedY float32 = initPos.Y()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tc := &TickContext{
				B:              b,
				Tick:           tick,
				LastPredictedY: lastPredictedY,
			}

			b.Mu.Lock()
			tc.CurrPos = b.Pos
			tc.MState = b.MovementState
			tc.TPlayer = b.TargetPlayerName
			tc.TPos = b.TargetPos
			tc.Yaw = b.Yaw
			tc.Pitch = b.Pitch
			tc.VelY = b.VelY
			b.Mu.Unlock()

			tc.FeetX = int32(math.Floor(float64(tc.CurrPos.X())))
			tc.FeetY = int32(math.Floor(float64(tc.CurrPos.Y())))
			tc.FeetZ = int32(math.Floor(float64(tc.CurrPos.Z())))

			// === AUTO-LEARN PASSABLE BLOCKS ===
			// Only mark the block below feet as solid when grounded.
			// During a jump the bot passes through air blocks; marking
			// them solid would create phantom ground and break gravity.
			b.Mu.Lock()
			isGrounded := b.IsGrounded
			velY := b.VelY
			b.Mu.Unlock()

			isMidAir := !isGrounded || velY > 0.05 || velY < -0.05

			if !isMidAir {
				// Grounded: safe to learn floor below feet as solid.
				// But cross-check against real chunk data first so we
				// don't override an air block the chunk knows about.
				shouldMarkSolid := true
				if b.WorldCache != nil {
					isSolid, loaded := b.WorldCache.IsBlockSolid(tc.FeetX, tc.FeetY-1, tc.FeetZ)
					if loaded && !isSolid {
						shouldMarkSolid = false
					}
				}
				if shouldMarkSolid {
					b.WorldModel.SetSolid(tc.FeetX, tc.FeetY-1, tc.FeetZ, true)
				}
			}
			// The blocks at feet, head and above are always passable
			// (the bot is standing in them, so they can't be solid).
			b.WorldModel.SetSolid(tc.FeetX, tc.FeetY, tc.FeetZ, false)
			b.WorldModel.SetSolid(tc.FeetX, tc.FeetY+1, tc.FeetZ, false)
			b.WorldModel.SetSolid(tc.FeetX, tc.FeetY+2, tc.FeetZ, false)

			tc.detectLadder()
			tc.updateDistanceToPlayer()
			tc.updateTargetPositionIfFollowing()
			tc.resolveNextTarget()
			tc.performActiveSteering()
			tc.runPhysicsAndCollisions()
			tc.updateLookDirection()
			tc.calculateMovementSpeedAndPosition()
			tc.writePlayerAuthInputPacket()

			lastPredictedY = tc.LastPredictedY
			tick++
		}
	}
}
