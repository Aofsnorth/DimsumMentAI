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
	// PlayerAuthInput is the client heartbeat: a real Bedrock client sends it
	// EVERY tick (20/s), even when standing perfectly still. Previously we
	// skipped the send entirely when idle and not turning (the venityLookOnly
	// throttle), which broke the heartbeat and let Venity time the session out
	// ("Network timed out"). We now always send; venityLookOnly only controls
	// whether positional delta is suppressed below.
	venityLookOnly := tc.B.VenityCompat && tc.MState == "idle" && !tc.HasHorizontalMove && !tc.ShouldJump

	tc.MoveDelta = tc.CurrPos.Sub(tc.PrevPos)
	if venityLookOnly {
		tc.MoveDelta = mgl32.Vec3{}
		tc.MoveVec = mgl32.Vec2{}
	}

	yawWorldRad := float64(tc.Yaw+90) * math.Pi / 180
	forwardX := float32(math.Cos(yawWorldRad))
	forwardZ := float32(math.Sin(yawWorldRad))

	yawDiff := tc.TargetYaw - tc.Yaw
	for yawDiff < -180 {
		yawDiff += 360
	}
	for yawDiff > 180 {
		yawDiff -= 360
	}
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
	// HorizontalCollision must ONLY be set when we are genuinely blocked. A real
	// client never reports a side collision while freely moving horizontally.
	// Setting it unconditionally (the old behaviour) is harmless while idle but
	// becomes a self-contradiction the moment we walk — "I'm wall-stuck" while
	// posX/posZ change ~0.28/tick — which Venity's movement anticheat reads as a
	// hack and silently closes the socket. Only flag it when we WANT to move but
	// our actual horizontal delta is ~0 (i.e. actually pinned against geometry).
	horizDeltaSq := tc.MoveDelta.X()*tc.MoveDelta.X() + tc.MoveDelta.Z()*tc.MoveDelta.Z()
	if tc.HasHorizontalMove && horizDeltaSq < 0.0004 {
		inputData.Set(packet.InputFlagHorizontalCollision)
	}
	if tc.ShouldJump {
		inputData.Set(packet.InputFlagJumping)
	}
	if tc.IsOnLadder && tc.VelY <= 0.0 && !tc.ActivelyClimbing {
		inputData.Set(packet.InputFlagSneaking)
	}
	if tc.IsOnLadder && tc.VelY < 0 {
		inputData.Set(packet.InputFlagSneaking)
	}
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
	if tc.MoveVec.Y() > 0.5 && !tc.IsOnLadder {
		inputData.Set(packet.InputFlagSprinting)
	}

	tc.B.Mu.Lock()
	// ClientAckServerData tells the server we processed its correction.
	// The original bot unconditionally sent ClientAckServerData when RewindMovement was true.
	// But according to the protocol, ClientAckServerData MUST ONLY be set if a
	// CorrectPlayerMovePrediction was received. Since Venity never sends them, we
	// should never set this flag. Setting it on every tick causes a kick after 30s.
	clientAck := false
	tc.B.Mu.Unlock()
	tc.B.Mu.Lock()
	if tc.B.EmoteTicks > 0 {
		tc.B.EmoteTicks--
		isPathfindingState := tc.MState == "follow" || tc.MState == "walk_to"
		switch tc.B.EmoteState {
		case "jump":
			inputData.Set(packet.InputFlagJumping)
		case "sneak":
			inputData.Set(packet.InputFlagSneaking)
		case "spin":
			if !isPathfindingState {
				tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw+18, 18)
			}
		case "wiggle":
			if !isPathfindingState {
				if tc.B.EmoteTicks%4 < 2 {
					tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw+15, 15)
				} else {
					tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw-15, 15)
				}
			}
		case "lookaround":
			if !isPathfindingState {
				if tc.B.EmoteTicks%5 == 0 {
					tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw+float32((tc.Tick%50)-25), 25)
					tc.Pitch = InterpolatePitch(tc.Pitch, tc.Pitch+float32((tc.Tick%30)-15), 15)
				}
			}
		case "nod":
			if !isPathfindingState {
				if tc.B.EmoteTicks%8 < 4 {
					tc.Pitch = InterpolatePitch(tc.Pitch, 30, 10)
				} else {
					tc.Pitch = InterpolatePitch(tc.Pitch, -10, 10)
				}
			}
		case "shake":
			if !isPathfindingState {
				if tc.B.EmoteTicks%8 < 4 {
					tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw+20, 20)
				} else {
					tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw-20, 20)
				}
			}
		}
		if tc.B.EmoteTicks == 0 {
			tc.B.EmoteState = ""
		}
	}
	tc.B.Yaw = tc.Yaw
	tc.B.Pitch = tc.Pitch
	tc.B.HeadYaw = tc.HeadYaw
	tc.B.Mu.Unlock()

	pk := &packet.PlayerAuthInput{
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
	// Log every send while moving (so the final ticks before a Venity kick are
	// captured), plus the usual startup/heartbeat sampling when idle.
	if tc.Tick < 5 || tc.Tick%200 == 0 || tc.HasHorizontalMove || tc.ShouldJump {
		tc.B.Mu.Lock()
		tickSyncedLog := tc.B.TickSynced
		tc.B.Mu.Unlock()
		// #region agent log
		debuglog.Log("M", "movement/packet.go:writePlayerAuthInput", "PlayerAuthInput tick", map[string]any{
			"tick":           tc.Tick,
			"rewindMovement": tc.B.RewindMovement,
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
