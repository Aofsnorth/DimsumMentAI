package movement

import (
	"context"
	"math"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/debuglog"
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
	b.Mu.Lock()
	initPos := b.Pos
	b.Mu.Unlock()
	
	var lastPredictedY float32 = initPos.Y()
	prevPos := initPos

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.Mu.Lock()
			tick := b.ServerTick
			b.ServerTick++
			b.Mu.Unlock()

			tc := &TickContext{
				B:              b,
				Tick:           tick,
				LastPredictedY: lastPredictedY,
				PrevPos:        prevPos,
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

			// Per-tick body clearance: feet/head cells are non-solid for
			// collision only this tick. Do not use SetSolid(false) here —
			// that permanently erased real blocks from the world model.
			b.WorldModel.ClearBodyClearance()
			b.WorldModel.SetBodyClearance(tc.FeetX, tc.FeetY, tc.FeetZ)
			b.WorldModel.SetBodyClearance(tc.FeetX, tc.FeetY+1, tc.FeetZ)
			b.WorldModel.SetBodyClearance(tc.FeetX, tc.FeetY+2, tc.FeetZ)

			b.Mu.Lock()
			isGrounded := b.IsGrounded
			velY := b.VelY
			b.Mu.Unlock()
			isMidAir := !isGrounded || velY > 0.05 || velY < -0.05
			if !isMidAir && b.WorldCache != nil {
				if isSolid, loaded := b.WorldCache.IsBlockSolid(tc.FeetX, tc.FeetY-1, tc.FeetZ); loaded && isSolid {
					b.WorldModel.SetSolid(tc.FeetX, tc.FeetY-1, tc.FeetZ, true)
				}
			}

			tc.detectLadder()
			tc.updateDistanceToPlayer()
			tc.updateTargetPositionIfFollowing()
			tc.resolveNextTarget()
			venityIdle := tc.B.VenityCompat && tc.MState == "idle"
			if !venityIdle {
				tc.performActiveSteering()
				tc.runPhysicsAndCollisions()
			}
			tc.updateLookDirection()
			tc.calculateMovementSpeedAndPosition()
			tc.writePlayerAuthInputPacket()
			if tick > 0 && tick%200 == 0 {
				// #region agent log
				debuglog.Log("C", "movement/movement.go:SendInputLoop", "input loop alive", map[string]any{
					"tick": tick,
					"x":    tc.CurrPos.X(),
					"y":    tc.CurrPos.Y(),
					"z":    tc.CurrPos.Z(),
				})
				// #endregion
			}

			lastPredictedY = tc.LastPredictedY
			prevPos = tc.CurrPos
		}
	}
}
