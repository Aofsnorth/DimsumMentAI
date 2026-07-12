// Package movement implements tick-level bot movement, path following, and look
// direction. It is responsible for steering, physics, collision resolution, and
// the PlayerAuthInput heartbeat sent to the server.
package movement

import (
	"math"

	"bedrock-ai/internal/debuglog"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// interactNoise returns a tiny, smooth, non-repeating angular offset that
// simulates sub-degree mouse jitter on the interact (crosshair) channels.
// amp is the peak amplitude in degrees; phase offsets the waveform so yaw
// and pitch noise don't move in lockstep.
func interactNoise(tick uint64, amp, phase float32) float32 {
	t := float64(tick) + float64(phase)
	return amp * float32(math.Sin(t*0.087)+0.4*math.Sin(t*0.191+1.1))
}

func (tc *TickContext) writePlayerAuthInputPacket() {
	tc.prepareMoveVector()
	emoteJump, emoteSneak := tc.applyEmote()
	inputData := tc.buildInputData(emoteJump, emoteSneak)
	tc.sendPlayerAuthInput(inputData)
}

func (tc *TickContext) prepareMoveVector() {
	venityLookOnly := tc.venityLookOnly()

	tc.MoveDelta = tc.CurrPos.Sub(tc.PrevPos)
	if venityLookOnly {
		tc.MoveDelta = mgl32.Vec3{}
		tc.MoveVec = mgl32.Vec2{}
	}

	yawWorldRad := float64(tc.Yaw+90) * math.Pi / 180
	forwardX := float32(math.Cos(yawWorldRad))
	forwardZ := float32(math.Sin(yawWorldRad))

	yawDiff := angleDifference(tc.TargetYaw, tc.Yaw)
	absYawDiff := math.Abs(float64(yawDiff))

	if tc.HasHorizontalMove && tc.Dist > 0.01 {
		moveDirX := tc.Dx / tc.Dist
		moveDirZ := tc.Dz / tc.Dist
		moveForward := moveDirX*forwardX + moveDirZ*forwardZ
		moveStrafe := moveDirX*(-forwardZ) + moveDirZ*forwardX

		if tc.IsLadderActive {
			moveStrafe = 0.0
		}
		if absYawDiff > 10.0 {
			moveStrafe = 0.0
		}

		tc.MoveVec = mgl32.Vec2{moveStrafe, moveForward}
	}

	if tc.IsOnLadder && tc.ActivelyClimbing {
		if tc.VelY > 0 {
			tc.MoveVec = mgl32.Vec2{0, 1.0}
		} else if tc.VelY < 0 {
			tc.MoveVec = mgl32.Vec2{0, -1.0}
		}
	}
}

func (tc *TickContext) venityLookOnly() bool {
	return tc.B.VenityCompat && tc.MState == "idle" && !tc.HasHorizontalMove && !tc.ShouldJump
}

func (tc *TickContext) applyEmote() (emoteJump, emoteSneak bool) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()

	if tc.B.EmoteTicks <= 0 {
		return false, false
	}

	tc.B.EmoteTicks--
	isPathfindingState := tc.MState == "follow" || tc.MState == "walk_to"
	switch tc.B.EmoteState {
	case "jump":
		emoteJump = true
	case "sneak":
		emoteSneak = true
	case "spin":
		tc.handleEmoteSpin(isPathfindingState)
	case "wiggle":
		tc.handleEmoteWiggle(isPathfindingState)
	case "lookaround":
		tc.handleEmoteLookAround(isPathfindingState)
	case "nod":
		tc.handleEmoteNod(isPathfindingState)
	case "shake":
		tc.handleEmoteShake(isPathfindingState)
	}

	if tc.B.EmoteTicks == 0 {
		tc.B.EmoteState = ""
	}

	tc.B.Yaw = tc.Yaw
	tc.B.Pitch = tc.Pitch
	tc.B.HeadYaw = tc.HeadYaw
	return emoteJump, emoteSneak
}

func (tc *TickContext) handleEmoteSpin(isPathfindingState bool) {
	if isPathfindingState {
		return
	}
	tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw+18, 18)
}

func (tc *TickContext) handleEmoteWiggle(isPathfindingState bool) {
	if isPathfindingState {
		return
	}
	if tc.B.EmoteTicks%4 < 2 {
		tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw+15, 15)
	} else {
		tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw-15, 15)
	}
}

func (tc *TickContext) handleEmoteLookAround(isPathfindingState bool) {
	if isPathfindingState {
		return
	}
	if tc.B.EmoteTicks%5 == 0 {
		tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw+float32((tc.Tick%50)-25), 25)
		tc.Pitch = InterpolatePitch(tc.Pitch, tc.Pitch+float32((tc.Tick%30)-15), 15)
	}
}

func (tc *TickContext) handleEmoteNod(isPathfindingState bool) {
	if isPathfindingState {
		return
	}
	if tc.B.EmoteTicks%8 < 4 {
		tc.Pitch = InterpolatePitch(tc.Pitch, 30, 10)
	} else {
		tc.Pitch = InterpolatePitch(tc.Pitch, -10, 10)
	}
}

func (tc *TickContext) handleEmoteShake(isPathfindingState bool) {
	if isPathfindingState {
		return
	}
	if tc.B.EmoteTicks%8 < 4 {
		tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw+20, 20)
	} else {
		tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw-20, 20)
	}
}

func (tc *TickContext) buildInputData(emoteJump, emoteSneak bool) protocol.Bitset {
	inputData := protocol.NewBitset(packet.PlayerAuthInputBitsetSize)
	// BlockBreakingDelayEnabled is sent by a real Bedrock client on EVERY tick
	// (verified via MITM capture: 245/245 PlayerAuthInput packets, even while
	// standing perfectly still). Our bot never sent it, which is the single most
	// consistent difference between us and a genuine client — the likely trigger
	// for Venity's anticheat silently closing the socket ~30s after spawn. Set it
	// unconditionally, every tick, to match the real client baseline.
	inputData.Set(packet.InputFlagBlockBreakingDelayEnabled)
	if tc.IsGrounded {
		// VerticalCollision = standing on the floor; correct every grounded tick.
		inputData.Set(packet.InputFlagVerticalCollision)
	}
	if tc.shouldSetHorizontalCollision() {
		inputData.Set(packet.InputFlagHorizontalCollision)
	}
	if tc.ShouldJump || emoteJump {
		inputData.Set(packet.InputFlagJumping)
	}
	if tc.shouldSetSneak(emoteSneak) {
		inputData.Set(packet.InputFlagSneaking)
	}
	tc.applyMovementInputFlags(inputData)
	if tc.MoveVec.Y() > 0.5 && !tc.IsOnLadder {
		inputData.Set(packet.InputFlagSprinting)
	}
	return inputData
}

func (tc *TickContext) shouldSetHorizontalCollision() bool {
	if !tc.HasHorizontalMove {
		return false
	}
	horizDeltaSq := tc.MoveDelta.X()*tc.MoveDelta.X() + tc.MoveDelta.Z()*tc.MoveDelta.Z()
	return horizDeltaSq < 0.0004
}

func (tc *TickContext) shouldSetSneak(emoteSneak bool) bool {
	if emoteSneak {
		return true
	}
	if !tc.IsOnLadder {
		return false
	}
	return tc.VelY <= 0.0 && !tc.ActivelyClimbing || tc.VelY < 0
}

func (tc *TickContext) applyMovementInputFlags(inputData protocol.Bitset) {
	if tc.MoveVec.Y() > 0.1 {
		inputData.Set(packet.InputFlagUp)
	} else if tc.MoveVec.Y() < -0.1 {
		inputData.Set(packet.InputFlagDown)
	}
	if tc.MoveVec.X() > 0.1 {
		inputData.Set(packet.InputFlagRight)
	} else if tc.MoveVec.X() < -0.1 {
		inputData.Set(packet.InputFlagLeft)
	}
}

func (tc *TickContext) sendPlayerAuthInput(inputData protocol.Bitset) {
	pk := tc.buildPlayerAuthInputPacket(inputData)
	tc.logPlayerAuthInputCond()
	if err := tc.B.Conn.WritePacket(pk); err != nil {
		tc.B.Logger.Warn("SendInputLoop: connection closed or write failed", "error", err.Error())
		// #region agent log
		debuglog.Log("C", "movement/packet.go:writePlayerAuthInput", "PlayerAuthInput write failed", map[string]any{
			"error": err.Error(),
			"tick":  tc.Tick,
		})
		// #endregion
		return
	}
	tc.B.Mu.Lock()
	tc.B.LastSentInputYaw = tc.Yaw
	tc.B.LastSentInputPitch = tc.Pitch
	tc.B.Mu.Unlock()
}

func (tc *TickContext) buildPlayerAuthInputPacket(inputData protocol.Bitset) *packet.PlayerAuthInput {
	return &packet.PlayerAuthInput{
		Position: tc.CurrPos.Add(mgl32.Vec3{0, 1.62, 0}),
		Pitch:    tc.Pitch,
		Yaw:      tc.Yaw,
		// HeadYaw is decoupled from body Yaw so the head leads the torso
		// during turns — the way a real player's view arrives before their
		// body finishes rotating. This is the single biggest contributor to
		// natural-looking head motion on normal servers.
		HeadYaw: tc.HeadYaw,
		// InteractYaw/InteractPitch represent the crosshair / aim direction.
		// A real client derives these from the head angle with sub-degree
		// mouse jitter, so we add a faint continuous noise to avoid sending
		// a mathematically identical value every tick.
		InteractPitch:      tc.Pitch + interactNoise(tc.Tick, 0.06, 0.0),
		InteractYaw:        normalizeYaw(tc.HeadYaw + interactNoise(tc.Tick, 0.08, 1.3)),
		MoveVector:         tc.MoveVec,
		InputData:          inputData,
		InputMode:          packet.InputModeTouch,
		PlayMode:           packet.PlayModeNormal,
		InteractionModel:   packet.InteractionModelTouch,
		Tick:               tc.Tick,
		Delta:              tc.MoveDelta,
		AnalogueMoveVector: tc.MoveVec,
		RawMoveVector:      tc.MoveVec,
	}
}

func (tc *TickContext) logPlayerAuthInputCond() {
	if !tc.shouldLogInput() {
		return
	}
	tc.B.Mu.Lock()
	tickSyncedLog := tc.B.TickSynced
	rewind := tc.B.RewindMovement
	tc.B.Mu.Unlock()
	clientAck := false
	// #region agent log
	debuglog.Log("M", "movement/packet.go:writePlayerAuthInput", "PlayerAuthInput tick", map[string]any{
		"tick":           tc.Tick,
		"rewindMovement": rewind,
		"clientAck":      clientAck,
		"tickSynced":     tickSyncedLog,
		"runId":          "tick-fix-v5",
		"hasHMove":       tc.HasHorizontalMove,
		"mState":         tc.MState,
		"shouldJump":     tc.ShouldJump,
		"posX":           tc.CurrPos.X(),
		"posY":           tc.CurrPos.Y(),
		"posZ":           tc.CurrPos.Z(),
		"moveVecX":       tc.MoveVec.X(),
		"moveVecY":       tc.MoveVec.Y(),
		"deltaX":         tc.MoveDelta.X(),
		"deltaY":         tc.MoveDelta.Y(),
		"deltaZ":         tc.MoveDelta.Z(),
		"yaw":            tc.Yaw,
	})
	// #endregion
}

func (tc *TickContext) shouldLogInput() bool {
	return tc.Tick < 5 || tc.Tick%200 == 0 || tc.HasHorizontalMove || tc.ShouldJump
}
