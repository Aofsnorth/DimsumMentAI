// Package movement implements tick-level bot movement, path following, and look
// direction. It is responsible for steering, physics, collision resolution, and
// the PlayerAuthInput heartbeat sent to the server.
package movement

import (
	"bedrock-ai/internal/debuglog"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (tc *TickContext) writePlayerAuthInputPacket() bool {
	tc.prepareMoveVector()
	emoteJump, emoteSneak := tc.applyEmote()
	inputData := tc.buildInputData(emoteJump, emoteSneak)
	itemInteractionData := tc.TakeItemInteractionData(&inputData)
	itemStackRequest := tc.TakeItemStackRequest(&inputData)
	blockActions := tc.TakeBlockActions(&inputData)
	return tc.sendPlayerAuthInput(inputData, itemInteractionData, itemStackRequest, blockActions)
}

func (tc *TickContext) TakeItemInteractionData(inputData *protocol.InputFlags) *protocol.UseItemTransactionData {
	data, ok := tc.B.TakeItemInteractionData()
	if !ok {
		return nil
	}
	inputData.Set(packet.InputFlagPerformItemInteraction)
	return &data
}

func (tc *TickContext) TakeItemStackRequest(inputData *protocol.InputFlags) *protocol.ItemStackRequest {
	request, ok := tc.B.TakeItemStackRequest()
	if !ok {
		return nil
	}
	inputData.Set(packet.InputFlagPerformItemStackRequest)
	return &request
}

// TakeBlockActions drains the block actions queued for this tick — break
// routing (see internal/bot/breaking.go) plus the per-tick ContinueDestroy —
// and flags the PlayerAuthInput as carrying them, the way a stock client sends
// block breaking on servers that negotiated server-authoritative breaking.
func (tc *TickContext) TakeBlockActions(inputData *protocol.InputFlags) []protocol.PlayerBlockAction {
	actions := tc.B.TakeBlockTickActions()
	if len(actions) > 0 {
		inputData.Set(packet.InputFlagPerformBlockActions)
	}
	return actions
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

	yawDiff := AngleDifference(tc.TargetYaw, tc.Yaw)
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

	// Orientation is persisted once per tick in SendInputLoop after the look
	// updates. Doing it here too would be redundant; historically this
	// emote-only writeback was the ONLY persistence, which froze the bot's
	// body at its spawn yaw whenever no emote was active.
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

func (tc *TickContext) buildInputData(emoteJump, emoteSneak bool) protocol.InputFlags {
	inputData := protocol.NewInputFlags(packet.InputFlagCount)
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
	tc.applySwimInputFlags(&inputData)
	tc.applySprintFlag(&inputData)
	return inputData
}

// applySprintFlag decides whether this tick runs.
//
// A latched Jev hint wins while it is set: run means the flag goes out
// whenever the body is moving forward, walk means it stays off even on a long
// straight stretch. With no hint the old rule stands — sprint on a committed
// forward stride, off the ladder — so every tick that never heard of Jev
// behaves exactly as before.
func (tc *TickContext) applySprintFlag(inputData *protocol.InputFlags) {
	if tc.IsOnLadder || tc.MoveVec.Y() <= 0.5 {
		return
	}
	if sprint, _, latched := tc.B.SprintHint(); latched {
		if sprint {
			inputData.Set(packet.InputFlagSprinting)
		}
		return
	}
	inputData.Set(packet.InputFlagSprinting)
}

// applySprintHop turns a latched sprint-jump into this tick's jump, alongside
// the steering solution's own ShouldJump. It runs in the same place as the
// requested-jump latch and under the same contract: only off the ground's
// truth from the previous tick's physics, never mid-air, so one latch is one
// hop and never a fly.
func (tc *TickContext) applySprintHop() {
	if tc.ShouldJump || tc.IsOnLadder || tc.SwimPlanned {
		return
	}
	_, hop, latched := tc.B.SprintHint()
	if !latched || !hop {
		return
	}
	if tc.MoveVec.Y() <= 0.5 {
		return
	}
	tc.B.Mu.Lock()
	grounded := tc.B.IsGrounded
	tc.B.Mu.Unlock()
	if !grounded {
		return
	}
	tc.ShouldJump = true
	tc.JumpReason = "sprint_hop"
}

// planSwim samples the water and stores this tick's plan on the context.
//
// It runs before steering and physics, not inside buildInputData, for two
// reasons. The vertical physics needs the swim drive to replace gravity, and
// that happens earlier in the tick than the input packet is built; and a plan
// taken at packet time would read the world a second time per tick and could
// disagree with the physics that already moved the body.
//
// A world that cannot answer leaves SwimPlanned false, and every consumer
// treats that as "no water opinion" rather than "dry".
func (tc *TickContext) planSwim() {
	if tc.Swim == nil {
		return
	}
	intent, ok := tc.Swim.Plan()
	if !ok {
		return
	}
	tc.SwimIntent = intent
	tc.SwimPlanned = true
}

// takeRequestedJump turns a latched jump request into this tick's jump.
//
// This is the only place a jump can be produced. The steering solution owns
// ShouldJump, and it is recomputed from scratch every tick, so anything that set
// the flag from outside would be overwritten before the packet went out — which
// is why the scaffolder's "jump" used to be the emote and the body stayed on the
// floor.
//
// The request is consumed whether or not it produces a jump. A body already in
// the air cannot jump again, and re-arming the latch for the next grounded tick
// would turn one request into a hop the bot never asked for.
func (tc *TickContext) takeRequestedJump() {
	if tc.B == nil || !tc.B.JumpRequested() {
		return
	}
	// tc.IsGrounded is unusable here: TickContext is rebuilt every tick and the
	// grounded flag is only filled in by UpdateGroundedState, which runs in the
	// physics phase AFTER this. Reading it here always saw false, so every
	// scaffold jump request was consumed and silently dropped — the body never
	// left the ground and the block was never placed. b.IsGrounded is the flag
	// the previous tick's physics synced, which is the freshest truth available
	// at this point in the tick.
	tc.B.Mu.Lock()
	grounded := tc.B.IsGrounded
	tc.B.Mu.Unlock()
	if !grounded {
		// Leave the request latched so a one-tick grounding gap does not eat it;
		// it expires by TTL if nobody can honour it.
		return
	}
	tc.B.ConsumeJumpRequest()
	tc.ShouldJump = true
	tc.JumpReason = "requested"
}

// applySwimInputFlags merges the already-planned swim intent into the tick's
// flag set.
//
// It is the step that makes the water code live rather than unit-tested. The
// controller samples the world, advances the breath clock, and decides whether
// the body should be rising, sinking or crossing; without this the swim, dive
// and surface flags were computed and then dropped on the floor, because
// buildInputData is the only thing that reaches the server.
func (tc *TickContext) applySwimInputFlags(flags *protocol.InputFlags) {
	if !tc.SwimPlanned {
		return
	}
	// ApplyInput records the swimming state for the next tick, so the
	// StartSwimming edge is sent once on the way in and StopSwimming once on the
	// way out.
	tc.Swim.ApplyInput(flags, tc.SwimIntent)
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

func (tc *TickContext) applyMovementInputFlags(inputData protocol.InputFlags) {
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

func (tc *TickContext) sendPlayerAuthInput(inputData protocol.InputFlags, itemInteractionData *protocol.UseItemTransactionData, itemStackRequest *protocol.ItemStackRequest, blockActions []protocol.PlayerBlockAction) bool {
	pk := tc.BuildPlayerAuthInputPacket(inputData, itemInteractionData, itemStackRequest, blockActions)
	tc.logPlayerAuthInputCond()
	if err := tc.B.Conn.WritePacket(pk); err != nil {
		tc.B.Logger.Warn("SendInputLoop: connection closed or write failed", "error", err.Error())
		// #region agent log
		debuglog.Log("C", "movement/packet.go:writePlayerAuthInput", "PlayerAuthInput write failed", map[string]any{
			"error": err.Error(),
			"tick":  tc.Tick,
		})
		// #endregion
		return false
	}
	tc.B.Mu.Lock()
	tc.B.LastSentInputYaw = tc.Yaw
	tc.B.LastSentInputPitch = tc.Pitch
	tc.B.Mu.Unlock()
	return true
}

func (tc *TickContext) BuildPlayerAuthInputPacket(inputData protocol.InputFlags, itemInteractionData *protocol.UseItemTransactionData, itemStackRequest *protocol.ItemStackRequest, blockActions []protocol.PlayerBlockAction) *packet.PlayerAuthInput {
	pk := &packet.PlayerAuthInput{
		Position: tc.CurrPos.Add(mgl32.Vec3{0, 1.62, 0}),
		Pitch:    tc.Pitch,
		Yaw:      tc.Yaw,
		// HeadYaw is decoupled from body Yaw so the head leads the torso
		// during turns — the way a real player's view arrives before their
		// body finishes rotating. This is the single biggest contributor to
		// natural-looking head motion on normal servers.
		HeadYaw: tc.HeadYaw,
		// InteractYaw/InteractPitch represent the crosshair / aim direction and
		// carry the cosmetic drift. The drift lives here rather than in
		// HeadYaw/Pitch so it can never feed back into the eased gaze.
		InteractPitch:      tc.Pitch + tc.LookDriftPitch,
		InteractYaw:        normalizeYaw(tc.HeadYaw + tc.LookDriftYaw),
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
	if itemInteractionData != nil {
		pk.ItemInteractionData = protocol.Option(*itemInteractionData)
	}
	if itemStackRequest != nil {
		pk.ItemStackRequest = protocol.Option(*itemStackRequest)
	}
	if len(blockActions) > 0 {
		pk.BlockActions = protocol.Option(blockActions)
	}
	return pk
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
		"pitch":          tc.Pitch,
		"headYaw":        tc.HeadYaw,
		"targetYaw":      tc.TargetYaw,
		"targetPitch":    tc.TargetPitch,
		"driftPitch":     tc.LookDriftPitch,
		"driftYaw":       tc.LookDriftYaw,
		"interactPitch":  tc.Pitch + tc.LookDriftPitch,
		"interactYaw":    normalizeYaw(tc.HeadYaw + tc.LookDriftYaw),
		"smoothPitch":    tc.SmoothedLookPitch,
		"smoothYaw":      tc.SmoothedLookYaw,
		"isGrounded":     tc.IsGrounded,
		"velY":           tc.VelY,
		"anchorY":        tc.serverAnchorY(),
		"anchorDelta":    tc.serverAnchorDelta(),
		"trackingLook":   tc.isTrackingLookTarget(),
	})
	// #endregion
}

func (tc *TickContext) shouldLogInput() bool {
	return tc.Tick < 5 || tc.Tick%200 == 0 || tc.HasHorizontalMove || tc.ShouldJump ||
		(tc.isTrackingLookTarget() && tc.Tick%10 == 0)
}

// isTrackingLookTarget reports whether a look-at/follow target is currently
// active. Used to raise the log sample rate while tracking, where the
// interesting signal (pitch chatter) happens far faster than the default
// once-per-200-ticks sampling can see.
func (tc *TickContext) isTrackingLookTarget() bool {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	return tc.B.LookTargetName != "" && time.Now().Before(tc.B.LookTargetUntil)
}
