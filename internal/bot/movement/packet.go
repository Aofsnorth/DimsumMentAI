// Package movement implements tick-level bot movement, path following, and look
// direction. It is responsible for steering, physics, collision resolution, and
// the PlayerAuthInput heartbeat sent to the server.
package movement

import (
	"bedrock-ai/internal/bot"
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

	// In vanilla Minecraft Bedrock, gravity applies every tick. When standing or
	// walking on the ground, the client's downward impulse against the floor gives
	// Delta.Y a negative value (~ -0.0784). Geyser calculates player ground status as:
	//   isOnGround = VERTICAL_COLLISION && lastTickEndVelocity.getY() < 0
	// Without a negative Delta.Y, Geyser evaluates isOnGround=false and forwards
	// onGround=false to Java, triggering server-side anticheat rubberband / desync.
	if groundedForPacket(tc) && !tc.ShouldJump && tc.VelY <= 0 {
		if tc.MoveDelta.Y() >= 0 {
			tc.MoveDelta = mgl32.Vec3{tc.MoveDelta.X(), -0.0784, tc.MoveDelta.Z()}
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
	// Only the input-flag emotes live here. The look emotes (spin, wiggle,
	// lookaround, nod, shake) are stepped by applyLookEmote in
	// updateLookDirection — applying them here too rotated the body twice per
	// tick, and before the fix the packet-time mutation was the ONLY one,
	// which the eased look erased before it could ever be seen.
	switch tc.B.EmoteState {
	case "jump":
		emoteJump = true
	case "sneak":
		emoteSneak = true
	case "twerk":
		emoteSneak = (tc.B.EmoteTicks/3)%2 == 0
	}

	if tc.B.EmoteTicks == 0 {
		tc.B.EmoteState = ""
		tc.B.EmoteJumpSpent = false
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
		// Cast to int before subtracting. Tick is a uint64, so Tick%50-25
		// underwound to roughly 1.8e19 for every tick whose remainder was
		// below 25, and InterpolateAngle clamped that to its maximum step. The
		// sweep was meant to travel from -24 to +25 and back; it only ever
		// turned one way, and stopped reversing the moment it should have.
		tc.Yaw = InterpolateAngle(tc.Yaw, tc.Yaw+float32(int(tc.Tick%50)-25), 25)
		tc.Pitch = InterpolatePitch(tc.Pitch, tc.Pitch+float32(int(tc.Tick%30)-15), 15)
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
	// The bot's move vector is camera-space by construction: prepareMoveVector
	// projects the world-space travel direction onto the basis built from the
	// look angle, so x is strafe and y is forward relative to where the body is
	// facing, exactly like a WASD client.
	//
	// InputFlagCameraRelativeMovementEnabled (bit 63) would declare that
	// explicitly. DO NOT set it unconditionally. Bit 63 postdates this bot's
	// interoperability target: on a server whose protocol predates it the flag
	// reads back as an out-of-range enum and the server answers with a
	// PacketViolationWarning ("enum value is deprecated ... readNoHeader failed!
	// packetId: 144") and drops the connection roughly 170ms after spawn --
	// before the world is usable. That is exactly what happened when this was
	// set, and it cost a full disconnect/reconnect loop on every attempt.
	//
	// Bit 57 (BlockBreakingDelayEnabled) above is unaffected and still goes out
	// every tick: older servers understand it, this one does too. The
	// difference is the whole lesson -- flag availability is a protocol-version
	// question, not a correctness one.
	//
	// If you ever need the declaration, gate it behind config keyed to a server
	// you have actually verified accepts it. Do not re-add it as "obviously
	// right", because it is not.
	if tc.TeleportAck {
		inputData.Set(packet.InputFlagHandledTeleport)
	}
	if groundedForPacket(tc) {
		// VerticalCollision = standing on the floor; correct every grounded tick.
		inputData.Set(packet.InputFlagVerticalCollision)
	}
	if tc.shouldSetHorizontalCollision() {
		inputData.Set(packet.InputFlagHorizontalCollision)
	}
	// The wire only ever claims a jump the physics actually integrated:
	// ShouldJump is owned by the pre-physics buyers (requested, sprint hop,
	// emote), the airborne physics branch clears it, and buildInputData runs
	// last. A flag high with no impulse behind it is the truncated hop the
	// packet-time emote purchase used to produce: the server bought a jump the
	// bot's own claims denied.
	if tc.ShouldJump {
		inputData.Set(packet.InputFlagJumping)
		inputData.Set(packet.InputFlagJumpDown)
	}
	if tc.shouldSetSneak(emoteSneak) {
		inputData.Set(packet.InputFlagSneaking)
		inputData.Set(packet.InputFlagSneakDown)
	}
	tc.applyMovementInputFlags(&inputData)
	tc.applySwimInputFlags(&inputData)
	tc.applySprintFlag(&inputData)
	// Last, and deliberately after every writer: the edges are derived from the
	// assembled level flags so they can never contradict what is on the wire.
	tc.Gait.ApplyGaitEdges(&inputData)
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
	if tc.B != nil {
		if sprint, _, latched := tc.B.SprintHint(); latched {
			if sprint {
				inputData.Set(packet.InputFlagSprinting)
				inputData.Set(packet.InputFlagSprintDown)
			}
			return
		}
	}
	inputData.Set(packet.InputFlagSprinting)
	inputData.Set(packet.InputFlagSprintDown)
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

// jumpableNow reports whether the body may be given a jump this tick.
//
// IsGrounded is the primary signal, but this server reports a body standing
// perfectly still as airborne — grounded=false with vel_y exactly zero, tick
// after tick, which is why "lompat" produced a reply and no hop. A body that is
// not moving vertically is not falling, so giving it a jump cannot produce the
// Jumping+VerticalCollision pair the server reads as an unsupported jump: that
// pair needs real downward motion to be true. Guarding on it is what kept a
// grounded-in-practice bot from ever leaving the floor.
//
// velY is the tick's own copy of the vertical velocity — seeded from the bot's
// previous-tick velocity when the context is built — and NOT the bot field:
// the bot field is only a stale mirror of it, and reading that mirror instead
// is how a body rising at 0.42 blocks/tick with an all-zero bot mirror looked
// jumpable.
func jumpableNow(b *bot.Bot, velY float32) bool {
	if b == nil {
		return false
	}
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.IsGrounded || velY == 0
}

// groundedForPacket answers the question the wire asks: did the body have the
// ground under it last tick. The live TickContext field is written by the
// physics phase that runs later in this same tick, so a waved emote flag can
// momentarily claim the body is grounded while the previous tick already had
// it airborne; the packet would then send Jumping + VerticalCollision on a
// falling body. The server reads that combination as an unsupported jump,
// patches the prediction gap on its next correction, and the client renders a
// second hop on the landing. This is the "lompat di udara naik terus lagi".
func groundedForPacket(tc *TickContext) bool {
	if tc == nil || tc.B == nil {
		return false
	}
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	return tc.B.IsGrounded
}

// buyEmoteJump turns an active "jump" emote into this tick's jump request.
//
// This runs BEFORE the physics phase, in the same branch that integrates it,
// because where the hop is bought decides whether the jump is a real one. The
// previous version bought the hop at packet time — after runPhysicsAndCollisions
// had already integrated the tick — so InputFlagJumping went out while the
// position claims on the very same packet never left the floor. The server
// bought the jump and arced the body up; the bot's own claims stayed at ground
// level; and every tick the two views pulled the body apart. The player watched
// the bot rise a fraction of a block and fall back: "bisa lompat tapi pendek
// bgt, ngak normal".
//
// The latch bounds the emote to one hop. An emote wave is ~80 ticks of active
// state, and without the latch every grounded tick inside the window re-buys
// the impulse — a held jump key on the wire, which is the "lompat di udara naik
// terus lagi" the one-hop latch exists to prevent. A body that is airborne when
// the emote starts stays latched but unspent: the gate refuses here without
// spending, the same way takeRequestedJump leaves its request latched for the
// landing tick to honour.
func (tc *TickContext) buyEmoteJump() {
	if tc == nil || tc.B == nil {
		return
	}
	tc.B.Mu.Lock()
	active := tc.B.EmoteState == "jump" && tc.B.EmoteTicks > 0
	// Same gate as takeRequestedJump: the previous tick's grounded answer, or a
	// body hanging at vertical rest, which is how this server reports standing.
	mayJump := active && !tc.B.EmoteJumpSpent && (tc.B.IsGrounded || tc.VelY == 0)
	if mayJump {
		tc.B.EmoteJumpSpent = true
	}
	tc.B.Mu.Unlock()
	if !mayJump {
		return
	}
	tc.ShouldJump = true
	tc.JumpReason = "emote"
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
	// left the ground and the block was never placed. The previous tick's
	// grounded answer is the freshest truth available at this point in the
	// tick, and a body hanging at vertical rest — which is how this server
	// reports a standing one — is jumpable too; tc.VelY still carries the
	// previous tick's velocity at this point, which is exactly why the gate
	// reads the tick's copy and not the bot's stale mirror.
	if !jumpableNow(tc.B, tc.VelY) {
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
	// The shield hold is the combat crouch: with a shield in the off hand the
	// server only counts blocks from a sneaking body, so the hold ORs straight
	// over everything else, the way a player holding the crouch key would.
	if tc.B != nil && tc.B.SneakHeldState() {
		return true
	}
	if !tc.IsOnLadder {
		return false
	}
	return tc.VelY <= 0.0 && !tc.ActivelyClimbing || tc.VelY < 0
}

func (tc *TickContext) applyMovementInputFlags(inputData *protocol.InputFlags) {
	up := tc.MoveVec.Y() > 0.1
	down := tc.MoveVec.Y() < -0.1
	left := tc.MoveVec.X() < -0.1
	right := tc.MoveVec.X() > 0.1

	if up && left {
		setInputFlag(inputData, packet.InputFlagUpLeft)
	} else if up && right {
		setInputFlag(inputData, packet.InputFlagUpRight)
	} else if down && left {
		setInputFlag(inputData, packet.InputFlagDownLeft)
	} else if down && right {
		setInputFlag(inputData, packet.InputFlagDownRight)
	}

	if up {
		setInputFlag(inputData, packet.InputFlagUp)
	} else if down {
		setInputFlag(inputData, packet.InputFlagDown)
	}
	if left {
		setInputFlag(inputData, packet.InputFlagLeft)
	} else if right {
		setInputFlag(inputData, packet.InputFlagRight)
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
	// Both pitch fields are clamped at the last possible moment, here, rather
	// than only where the gaze target was computed. Every writer upstream --
	// the eased pitch, the walking gaze offsets, the look emote sweeps, the
	// organic drift -- is a separate route to the same packet, and a value that
	// has been eased toward straight down for a while arrives already rounded
	// to the boundary. Clamping once, at the wire, is the only place that cannot
	// be forgotten by a later writer.
	pitch := ClampPitch(tc.Pitch)
	interactPitch := ClampPitch(tc.Pitch + tc.LookDriftPitch)

	pk := &packet.PlayerAuthInput{
		Position: tc.CurrPos.Add(mgl32.Vec3{0, 1.62, 0}),
		Pitch:    pitch,
		Yaw:      tc.Yaw,
		// HeadYaw is decoupled from body Yaw so the head leads the torso
		// during turns — the way a real player's view arrives before their
		// body finishes rotating. This is the single biggest contributor to
		// natural-looking head motion on normal servers.
		HeadYaw: tc.HeadYaw,
		// InteractYaw/InteractPitch represent the crosshair / aim direction and
		// carry the cosmetic drift. The drift lives here rather than in
		// HeadYaw/Pitch so it can never feed back into the eased gaze.
		InteractPitch:      interactPitch,
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
