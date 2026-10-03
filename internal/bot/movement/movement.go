// Package movement implements tick-level bot movement, path following, and look
// direction. It is responsible for steering, physics, collision resolution, and
// the PlayerAuthInput heartbeat sent to the server.
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
	B       *bot.Bot
	Tick    uint64
	CurrPos mgl32.Vec3
	MState  string
	TPlayer string
	TPos    mgl32.Vec3
	Yaw     float32
	Pitch   float32
	HeadYaw float32
	// LookDriftYaw/LookDriftPitch are a purely cosmetic offset applied when the
	// packet is written. Keeping them out of HeadYaw/Pitch means the eased gaze
	// can converge on a stable target instead of chasing its own drift.
	LookDriftYaw   float32
	LookDriftPitch float32
	// SmoothedLookYaw/SmoothedLookPitch carry the EMA of the raw stationary
	// look target across ticks. TickContext is rebuilt every tick, so
	// SendInputLoop passes these in and copies the updated values back out.
	SmoothedLookYaw   float32
	SmoothedLookPitch float32
	// FollowWalking is the follow walk/stop latch for this tick, computed in
	// UpdateShouldMoveState before steering and look run.
	FollowWalking       bool
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
	// DropOK is Jev's authority for one deliberate drop: set when the model
	// asks to leap a ledge, consumed by the first tick that uses it. It is a
	// latch on the tick rather than on the bot so it cannot outlive the decision
	// that granted it.
	DropOK bool
	// dropTaken keeps the spent authority from being re-read from the bot
	// mid-tick, so one decision produces exactly one leap.
	dropTaken bool
	// LedgeAheadOfBody is what the ground under the next step looks like. It is
	// read once per tick and handed to the prompt layer, so the model reasons
	// about the same measurement the body acts on rather than a second opinion.
	LedgeAheadOfBody    LedgeAhead
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
	TargetTolerance     float32 // arrival tolerance for walk_to (copied from bot)

	// Swim is the water controller for this tick, and SwimIntent is the plan it
	// produced. Both are nil when no controller was installed or the world
	// cannot answer, which is the ordinary case on dry land and must leave the
	// input flags exactly as they were before water movement existed.
	Swim        *SwimController
	SwimIntent  SwimIntent
	SwimPlanned bool

	// Gait carries the sprint/sneak/jump edges across ticks. It is a
	// pointer because an edge is only meaningful against the previous tick,
	// and the previous tick needs somewhere to live other than a TickContext
	// that is rebuilt 20 times a second. Nil is tolerated and means "no
	// transition history", which is the correct answer for a context assembled
	// by a test rather than by the movement loop.
	Gait *GaitState

	// Stride is the travel accumulator behind the walking head bob. Like Gait
	// it must outlive a single tick, because the bob's phase is derived from
	// distance and has to stay continuous through a stop.
	Stride *StridePhase
}

// notMovingReportInterval is how long the bot may want to move without moving
// before it says so. Three seconds is long enough that a bot pausing to clear a
// cell, break a block or finish a placement does not trip it.
const notMovingReportInterval = 3 * time.Second

// reportIfNotMoving logs when the bot has a reason to be walking and is not.
//
// Without this, a bot that stops moving produces no log line at all: steering
// runs every tick and has nothing to say when it is refusing to move, and the
// stuck counter logs at Debug. A session where the bot quietly stops halfway is
// then indistinguishable from one where it is busy thinking, because the only
// visible lines are the decision layer's — which keeps ticking, cheerful, while
// the body does nothing.
func reportIfNotMoving(b *bot.Bot, tc *TickContext, lastAt *time.Time, lastPos *mgl32.Vec3) {
	if tc.MState != "walk_to" {
		// Not trying to move: reset, so the next trip starts from a clean slate.
		*lastAt = time.Time{}
		return
	}

	now := time.Now()
	moved := math.Abs(float64(tc.CurrPos.X()-lastPos.X())) > 0.05 ||
		math.Abs(float64(tc.CurrPos.Z()-lastPos.Z())) > 0.05 ||
		math.Abs(float64(tc.CurrPos.Y()-lastPos.Y())) > 0.05
	if moved || lastAt.IsZero() {
		*lastAt = now
		*lastPos = tc.CurrPos
		return
	}
	if now.Sub(*lastAt) < notMovingReportInterval {
		return
	}
	*lastAt = now

	b.Mu.Lock()
	hasPath := len(b.CurrentPath) > 0
	pathIndex := b.PathIndex
	pathLen := len(b.CurrentPath)
	destination := b.TargetPos
	ticksStuck := b.TicksStuck
	consecutive := b.ConsecutiveStuckCount
	b.Mu.Unlock()

	b.Logger.Warn("bot wants to move and is not",
		"pos", tc.CurrPos,
		"destination", destination,
		"has_path", hasPath,
		"path_index", pathIndex,
		"path_len", pathLen,
		"distance", tc.Dist,
		"ticks_stuck", ticksStuck,
		"consecutive_stuck", consecutive,
		"grounded", tc.IsGrounded,
	)
}

// SendInputLoop handles the physical updates and steering of the bot
func SendInputLoop(ctx context.Context, b *bot.Bot, gd minecraft.GameData) {
	ticker := time.NewTicker(time.Second / 20) // 20 ticks/sec
	defer ticker.Stop()
	b.Mu.Lock()
	initPos := b.Pos
	initYaw := b.Yaw
	initPitch := b.Pitch
	b.Mu.Unlock()

	// One controller for the life of the connection, not one per tick: it owns
	// the submersion clock and the swimming edge state, and a fresh controller
	// every tick would read as a body that left the water and came back 20
	// times a second, re-sending StartSwimming forever and never letting the
	// breath clock run down.
	swim := NewSwimController(b, b.Logger)
	// Same lifetime reasoning as the swim controller: the sprint/sneak/jump
	// edges are comparisons against the previous tick, so they need a state
	// that outlives a single TickContext.
	gait := NewGaitState()
	stride := &StridePhase{}
	startStallWatchdog(b, ctx)

	var lastPredictedY float32 = initPos.Y()
	prevPos := initPos
	var lastProgressAt time.Time
	var lastProgressPos mgl32.Vec3
	smoothLookYaw, smoothLookPitch := initYaw, initPitch
	var connErr bool

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Stamped before the tick takes a single lock, so a tick parked
			// waiting for one still counts as alive until it gets past this.
			lastTickAt.Store(time.Now().UnixMilli())
			b.Mu.Lock()
			tick := b.ServerTick
			b.ServerTick++
			b.Mu.Unlock()

			tc := &TickContext{
				B:              b,
				Tick:           tick,
				LastPredictedY: lastPredictedY,
				PrevPos:        prevPos,
				Swim:           swim,
				Gait:           gait,
				Stride:         stride,
			}

			b.Mu.Lock()
			tc.CurrPos = b.Pos
			// Seeded here rather than left at the zero value. ApplyVerticalVelocity
			// derives the predicted height from it on every branch, and the struct
			// literal above cannot be relied on to remember a field that only
			// becomes meaningful once the current position is known. A branch that
			// forgets to assign it now costs one tick of stutter; before this it
			// cost a predicted position at Y=0 and a discarded path.
			tc.NextY = b.Pos.Y()
			tc.MState = b.MovementState
			tc.TPlayer = b.TargetPlayerName
			tc.TPos = b.TargetPos
			tc.Yaw = b.Yaw
			tc.Pitch = b.Pitch
			tc.HeadYaw = b.HeadYaw
			tc.VelY = b.VelY
			tc.TargetTolerance = b.TargetTolerance
			tc.SmoothedLookYaw = smoothLookYaw
			tc.SmoothedLookPitch = smoothLookPitch
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

			tc.planSwim()
			tc.takeRequestedJump()
			tc.applySprintHop()
			tc.detectLadder()
			tc.takeRequestedJump()
			tc.takeRequestedDrop()
			tc.updateTargetPositionIfFollowing()
			tc.resolveNextTarget()
			// A walk_to that lost its route has to re-plan here: the host clears
			// CurrentPath on a large position correction, and nothing else would
			// ever put one back.
			tc.EnsureWalkToHasPath()
			reportIfNotMoving(b, tc, &lastProgressAt, &lastProgressPos)
			venityIdle := tc.B.VenityCompat && tc.MState == "idle"
			if !venityIdle {
				tc.performActiveSteering()
				tc.runPhysicsAndCollisions()
			}
			tc.updateLookDirection()
			smoothLookYaw = tc.SmoothedLookYaw
			smoothLookPitch = tc.SmoothedLookPitch
			tc.calculateMovementSpeedAndPosition()
			if !tc.writePlayerAuthInputPacket() {
				connErr = true
			}
			// Persist computed orientation back to the bot so the next tick's
			// easing continues from where this tick left off. Without this the
			// eased Yaw/Pitch/HeadYaw were recomputed from the frozen spawn
			// angles every tick, so the body could never actually turn to face
			// its walk direction (it crawled sideways and computeMoveSpeed
			// kept throttling it because absYawDiff never shrank).
			b.Mu.Lock()
			b.Yaw = tc.Yaw
			b.Pitch = tc.Pitch
			b.HeadYaw = tc.HeadYaw
			b.Mu.Unlock()
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

			if connErr {
				return
			}
		}
	}
}
