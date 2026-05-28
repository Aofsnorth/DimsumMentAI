package movement

import (
	"math"

	"bedrock-ai/internal/debuglog"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (tc *TickContext) writePlayerAuthInputPacket() {
	venityLookOnly := tc.B.VenityCompat && tc.MState == "idle" && !tc.HasHorizontalMove && !tc.ShouldJump
	if venityLookOnly {
		tc.B.Mu.Lock()
		dyaw := math.Abs(float64(tc.Yaw - tc.B.LastSentInputYaw))
		dpitch := math.Abs(float64(tc.Pitch - tc.B.LastSentInputPitch))
		tc.B.Mu.Unlock()
		if dyaw < 1.0 && dpitch < 1.0 {
			return
		}
	}

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
	if tc.IsGrounded {
		inputData.Set(packet.InputFlagVerticalCollision)
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
	// For Venity: TickSynced is set to true after the post-chunk-load handshake
	// (sendVenityLoadedHandshake), so clientAck activates once the handshake completes.
	clientAck := tc.B.RewindMovement && tc.B.TickSynced
	tc.B.Mu.Unlock()
	if clientAck {
		inputData.Set(packet.InputFlagClientAckServerData)
	}
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
	tc.B.Mu.Unlock()

	pk := &packet.PlayerAuthInput{
		Position:           tc.CurrPos.Add(mgl32.Vec3{0, 1.62, 0}),
		Pitch:              tc.Pitch,
		Yaw:                tc.Yaw,
		HeadYaw:            tc.Yaw,
		MoveVector:         tc.MoveVec,
		InputData:          inputData,
		InputMode:          packet.InputModeTouch,
		PlayMode:           packet.PlayModeNormal,
		InteractionModel:   packet.InteractionModelTouch,
		InteractPitch:      tc.Pitch,
		InteractYaw:        tc.Yaw,
		Tick:               tc.Tick,
		Delta:              tc.MoveDelta,
		AnalogueMoveVector: tc.MoveVec,
		RawMoveVector:      tc.MoveVec,
	}
	if tc.Tick < 5 || tc.Tick%200 == 0 {
		tc.B.Mu.Lock()
		tickSyncedLog := tc.B.TickSynced
		tc.B.Mu.Unlock()
		// #region agent log
		debuglog.Log("M", "movement/packet.go:writePlayerAuthInput", "PlayerAuthInput tick", map[string]any{
			"tick":           tc.Tick,
			"rewindMovement": tc.B.RewindMovement,
			"clientAck":      clientAck,
			"tickSynced":     tickSyncedLog,
			"runId":          "tick-fix-v4",
			"venityLookOnly": venityLookOnly,
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
