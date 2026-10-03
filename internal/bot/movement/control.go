// Package movement implements tick-level bot movement, path following, and look
// direction. It is responsible for steering, physics, collision resolution, and
// the PlayerAuthInput heartbeat sent to the server.
package movement

import (
	"math"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/rand"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	walkingGazeYawPhaseFrequency   float32 = 0.030
	walkingGazePitchPhaseFrequency float32 = 0.041
	walkingGazePhaseBlendFrequency float32 = 0.013
	walkingGazeYawPhaseDrift       float32 = 0.35
	walkingGazePitchPhaseDrift     float32 = 1.20
	walkingGazePhaseBlendBias      float32 = 0.50
	WalkingGazeYawAmplitude        float32 = 10.0
	walkingGazePitchAmplitude      float32 = 6.0
	WalkingGazeDownwardBias        float32 = 2.0
	WalkingGazeMaxYawOffset        float32 = 12.0
	WalkingGazeMaxPitchOffset      float32 = 8.0
)

func (tc *TickContext) updateLookDirection() {
	tc.updateActivelyClimbing()
	WantsToMove := tc.resolveTargetLook()
	tc.applyEasedLookDirection(WantsToMove)
}

func (tc *TickContext) updateActivelyClimbing() {
	tc.ActivelyClimbing = false
	if !tc.IsOnLadder || !tc.HasPath {
		return
	}
	tc.B.Mu.Lock()
	if tc.B.PathIndex < len(tc.B.CurrentPath) {
		nn := tc.B.CurrentPath[tc.B.PathIndex]
		if nn.Y != int32(math.Floor(float64(tc.CurrPos.Y()))) && (tc.B.WorldModel.IsLadder(nn.X, nn.Y, nn.Z) || tc.B.WorldModel.IsLadder(nn.X, nn.Y-1, nn.Z)) {
			tc.ActivelyClimbing = true
		}
	}
	tc.B.Mu.Unlock()
}

func (tc *TickContext) resolveTargetLook() bool {
	tc.TargetYaw = tc.Yaw
	tc.TargetPitch = tc.Pitch

	WantsToMove := tc.WantsToMove()
	if WantsToMove {
		tc.ApplyMoveLookTarget()
		return WantsToMove
	}

	lookTargetActive := tc.applyTrackedLookTarget()
	tc.applyIdleOrFollowLook(lookTargetActive)
	return WantsToMove
}

// Follow walk/stop hysteresis thresholds. A single 2.0-block threshold on both
// the engage and release side made the walk state flap when the target hovered
// right at it, alternating the look target between the walking pose and the
// tracked pose — which read as the head bouncing while following.
const (
	followWalkDist       = 2.2
	followStopDist       = 1.8
	followWalkHeightDiff = 1.7
	followStopHeightDiff = 1.3
)

func (tc *TickContext) WantsToMove() bool {
	if tc.MState == "walk_to" {
		return true
	}
	if tc.MState != "follow" {
		return false
	}
	return tc.FollowWalking
}

func (tc *TickContext) ApplyMoveLookTarget() {
	if tc.Dist > 0.1 && tc.HasHorizontalMove {
		yawRad := math.Atan2(float64(tc.Dz), float64(tc.Dx))
		targetYaw := float32(yawRad*180/math.Pi) - 90
		tc.TargetYaw = targetYaw
		tc.TargetPitch = 0
		// The BODY still turns hard toward the walk direction so
		// computeMoveSpeed doesn't throttle us to 10% while EaseAngle is still
		// catching up — movement speed and the strafe/forward split both read
		// tc.Yaw, not tc.HeadYaw.
		//
		// The HEAD deliberately does NOT snap here. Snapping it to the movement
		// direction every tick threw away the head-trunk separation on which the
		// natural look depends: the ease in ApplyEasedLook could then never let
		// the head arrive before the body, because its starting point was reset
		// to the body angle each tick. Head aim now comes from the route
		// look-ahead in WalkingGazeAngles, which leads the body into turns.
		tc.Yaw = EaseAngle(tc.Yaw, targetYaw, 4.0, 40.0, 0.7)
	} else {
		tc.TargetYaw = tc.Yaw
		tc.TargetPitch = tc.Pitch
	}

	if tc.ActivelyClimbing && tc.LadderWallYaw != -999 {
		tc.TargetYaw = tc.LadderWallYaw
	}
}

func (tc *TickContext) applyIdleOrFollowLook(lookTargetActive bool) {
	if lookTargetActive {
		return
	}
	if tc.MState == "follow" {
		tc.ApplyFollowLookTarget()
		return
	}
	tc.applyIdleLook()
}

func (tc *TickContext) ApplyFollowLookTarget() {
	tc.B.Mu.Lock()
	playerPos := tc.B.TargetPos
	tc.B.Mu.Unlock()

	// Aim at the head, not the feet. Adding the eye height to both sides would
	// cancel out and leave the bot staring at the target's shins.
	eye := tc.CurrPos.Y() + bot.PlayerEyeHeight
	dxP := playerPos.X() - tc.CurrPos.X()
	dzP := playerPos.Z() - tc.CurrPos.Z()
	dyP := (playerPos.Y() + bot.PlayerEyeHeight) - eye

	distP := float32(math.Sqrt(float64(dxP*dxP + dzP*dzP)))
	if distP > 0.1 {
		yawRad := math.Atan2(float64(dzP), float64(dxP))
		tc.TargetYaw = float32(yawRad*180/math.Pi) - 90
		pitchRad := math.Atan2(float64(dyP), float64(distP))
		tc.TargetPitch = float32(-pitchRad * 180 / math.Pi)
		// Clamp to a comfortable range so the camera never gets stuck looking
		// straight up/down (which causes the eased pitch to stall at the ±90
		// boundary and feel unnatural). No deadzone here: the stationary
		// smoothing stage applies a soft level band instead, and a hard cutoff
		// at this boundary used to chatter whenever the target's raw pitch sat
		// right on it.
		tc.TargetPitch = clampFloat32(tc.TargetPitch, -25, 25)
	} else {
		tc.TargetYaw = tc.Yaw
		tc.TargetPitch = tc.Pitch
	}
}

func (tc *TickContext) applyEasedLookDirection(WantsToMove bool) {
	yawDiff := AngleDifference(tc.TargetYaw, tc.Yaw)
	absYawDiff := math.Abs(float64(yawDiff))
	yawSpeed := tc.selectYawSpeed(absYawDiff)
	pitchSpeed := float32(28.0)
	if WantsToMove {
		// Walking owns its target (route direction plus gaze scan). Keep the
		// smoothed state in sync so the first stationary tick after stopping
		// does not glide in from a stale angle.
		tc.SmoothedLookYaw, tc.SmoothedLookPitch = tc.TargetYaw, tc.TargetPitch
	} else {
		tc.SmoothStationaryLookTarget()
		pitchSpeed = 12.0
	}
	tc.ApplyEasedLook(WantsToMove, yawSpeed, pitchSpeed)
}

func (tc *TickContext) selectYawSpeed(absYawDiff float64) float32 {
	if tc.IsLadderActive {
		return 80.0
	}
	if absYawDiff > 30.0 {
		return 65.0
	}
	return 40.0
}

// ApplyEasedLook performs ease-out yaw/pitch interpolation toward the
// resolved target, tuned to read as a human head movement rather than a
// constant-rate servo.
//
// Natural-motion model:
//  1. The HEAD eases toward the target first (lead) — a real player turns
//     their eyes/head before their torso follows. During ordinary walking, a
//     low-frequency bounded offset lets the gaze scan around the route.
//  2. The BODY (tc.Yaw) follows the movement target while walking so gaze
//     offsets never alter steering. Outside walking it follows the head.
//  3. A continuous organic drift (sum of incommensurate sines) is added to
//     the head yaw and pitch every tick. This replaces the old deterministic
//     tick-parity jitter and mimics breathing, micro-saccades and postural
//     sway — the gaze is never mathematically frozen.
//  4. Smooth speed variation: the ease factor and step caps are multiplied
//     by a smooth low-frequency multiplier (periods of 3–8 seconds) so the
//     angular velocity gradually speeds up and slows down — never constant,
//     but never vibrating. Per-tick random jitter would create 20Hz
//     vibration; this instead matches how a human head's turn rate
//     fluctuates over the course of a single head movement.
func (tc *TickContext) ApplyEasedLook(WantsToMove bool, yawMax, pitchMax float32) {
	// --- Head (leads) -------------------------------------------------------
	// Head eases toward the target faster than the body so it arrives first.
	headEase := float32(0.32)
	headMin := float32(0.40)
	headCap := float32(22.0)
	if tc.IsLadderActive {
		headCap = 28.0
	}
	if WantsToMove {
		headEase = 0.42
		headMin = 0.70
	}
	if yawMax > headCap {
		yawMax = headCap
	}

	pitchEase := float32(0.22)
	pitchMin := float32(0.30)
	pitchCap := float32(14.0)
	if pitchMax > pitchCap {
		pitchMax = pitchCap
	}

	// Smooth speed multipliers: vary gradually over 3–8 second periods using
	// low-frequency sines with different phases per channel. Amplitude 0.22
	// means speed ranges from 0.78× to 1.22× baseline — enough to feel alive
	// but not erratic. Each channel (head yaw, head pitch, body) uses a
	// different phase so they don't pulse in unison.
	speedAmp := float32(0.22)
	headYawSpd := SmoothSpeedMultiplier(tc.Tick, speedAmp, 0.0)
	headPitchSpd := SmoothSpeedMultiplier(tc.Tick, speedAmp, 2.1)

	// Smoothly ease head toward target (with smooth speed variation).
	walkingScanEnabled := WantsToMove && tc.HasHorizontalMove && !tc.IsLadderActive
	gazeYaw, gazePitch := tc.TargetYaw, tc.TargetPitch
	if walkingScanEnabled {
		gazeYaw, gazePitch = tc.WalkingScanBase()
	}
	headTargetYaw, headTargetPitch := WalkingHeadTarget(
		tc.Tick,
		gazeYaw,
		gazePitch,
		walkingScanEnabled,
	)
	tc.HeadYaw = EaseAngle(tc.HeadYaw, headTargetYaw, headMin*headYawSpd, yawMax*headYawSpd, headEase*headYawSpd)
	tc.Pitch = EasePitch(tc.Pitch, headTargetPitch, pitchMin*headPitchSpd, pitchMax*headPitchSpd, pitchEase*headPitchSpd)

	// --- Organic drift ------------------------------------------------------
	// Continuous, non-repeating micro-motion via incommensurate sine
	// frequencies, kept in LookDriftYaw/LookDriftPitch instead of being added
	// onto HeadYaw/Pitch.
	//
	// Writing the drift into the eased state made it a feedback loop: the drift
	// pushed the head off target, the ease pulled it back at only 0.22 per tick,
	// and the leftover error showed up as a continuous up-down oscillation. The
	// drift is now applied as a visual offset on the outgoing packet only, so it
	// can never perturb the state the easing converges toward.
	ampYaw := float32(0.22)
	ampPitch := float32(0.28)
	if WantsToMove {
		ampYaw = 0.08
		ampPitch = 0.10
	}
	tc.LookDriftYaw, tc.LookDriftPitch = OrganicLookDrift(tc.Tick, ampYaw, ampPitch)

	// Walking head bob, phase-locked to travel. Added to the drift offsets and
	// not to the eased state, for the same reason: folding it in here would
	// make the bob push the head off target, the ease pull it back at 0.22 per
	// tick, and the leftover error read as a permanent tremor.
	tc.Stride.Advance(tc.MoveDelta.X(), tc.MoveDelta.Z())
	if bobPitch, bobYaw, walking := tc.Stride.Bob(!tc.IsGrounded); walking {
		tc.LookDriftPitch += bobPitch
		tc.LookDriftYaw += bobYaw
	}
	if walkingScanEnabled {
		tc.HeadYaw, tc.Pitch = boundWalkingGaze(gazeYaw, gazePitch, tc.HeadYaw, tc.Pitch)
	}

	// --- Body (lags) -------------------------------------------------------
	// Walking body yaw follows the route target rather than the scanned head
	// target, keeping gaze independent from movement direction and pathing.
	bodyEase := float32(0.20)
	bodyMin := float32(0.25)
	bodyCap := float32(16.0)
	if tc.IsLadderActive {
		bodyCap = 22.0
	}
	if WantsToMove {
		bodyEase = 0.30
		bodyMin = 0.50
	}
	bodyTargetYaw := tc.HeadYaw
	if WantsToMove {
		bodyTargetYaw = tc.TargetYaw
	}
	bodySpd := SmoothSpeedMultiplier(tc.Tick, speedAmp, 4.3)
	tc.Yaw = EaseAngle(tc.Yaw, bodyTargetYaw, bodyMin*bodySpd, bodyCap*bodySpd, bodyEase*bodySpd)
}

// WalkingScanBase is the angle the walking gaze scan is layered on top of.
//
// While there is a route to follow, that is the look-ahead aim: a point a few
// metres down the path, so the head is already turned toward the next corner and
// the vertical angle follows the terrain. Without a route it is the movement
// direction itself, which keeps the head-tracked follow and idle poses — and the
// steering reference the movement code reads — completely untouched.
func (tc *TickContext) WalkingScanBase() (float32, float32) {
	if yaw, pitch, ok := tc.WalkingGazeAngles(); ok {
		return yaw, pitch
	}
	return tc.TargetYaw, tc.TargetPitch
}

func WalkingHeadTarget(tick uint64, targetYaw, targetPitch float32, enabled bool) (float32, float32) {
	if !enabled {
		return targetYaw, targetPitch
	}

	yawOffset, pitchOffset := walkingGazeOffsets(tick)
	return normalizeYaw(targetYaw + yawOffset), ClampPitch(targetPitch + pitchOffset)
}

func walkingGazeOffsets(tick uint64) (float32, float32) {
	phase := float32(tick)
	yawDrift, pitchDrift := OrganicLookDrift(tick, walkingGazeYawPhaseDrift, walkingGazePitchPhaseDrift)
	blendDrift, _ := OrganicLookDrift(tick, walkingGazePhaseBlendBias, walkingGazePhaseBlendFrequency*100)

	yawPhase := phase*walkingGazeYawPhaseFrequency + yawDrift
	pitchPhase := phase*walkingGazePitchPhaseFrequency + pitchDrift
	scanBlend := clampFloat32(walkingGazePhaseBlendBias+blendDrift, 0, 1)

	return WalkingGazeYawAmplitude * float32(math.Sin(float64(yawPhase))),
		WalkingGazeDownwardBias + walkingGazePitchAmplitude*scanBlend*float32(math.Sin(float64(pitchPhase)))
}

func boundWalkingGaze(targetYaw, targetPitch, headYaw, pitch float32) (float32, float32) {
	yawOffset := clampFloat32(AngleDifference(headYaw, targetYaw), -WalkingGazeMaxYawOffset, WalkingGazeMaxYawOffset)
	minPitch := ClampPitch(targetPitch - WalkingGazeMaxPitchOffset)
	maxPitch := ClampPitch(targetPitch + WalkingGazeMaxPitchOffset)
	return normalizeYaw(targetYaw + yawOffset), clampFloat32(pitch, minPitch, maxPitch)
}

func (tc *TickContext) applyTrackedLookTarget() bool {
	tc.B.Mu.Lock()
	name := tc.B.LookTargetName
	until := tc.B.LookTargetUntil
	tc.B.Mu.Unlock()
	if name == "" || time.Now().After(until) {
		if name != "" {
			tc.B.Mu.Lock()
			tc.B.LookTargetName = ""
			tc.B.LookTargetUntil = time.Time{}
			tc.B.Mu.Unlock()
		}
		return false
	}
	if _, pos, ok := tc.B.FindPlayer(name); ok {
		tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 1.62, 0}))
		return true
	}
	return false
}

func (tc *TickContext) applyIdleLook() {
	now := time.Now()
	tc.B.Mu.Lock()
	nextChange := tc.B.NextIdleLookChange
	targetYaw := tc.B.IdleLookTargetYaw
	targetPitch := tc.B.IdleLookTargetPitch
	targetType := tc.B.IdleLookTargetType
	targetID := tc.B.IdleLookTargetID
	targetPos := tc.B.IdleLookTargetPos
	tc.B.Mu.Unlock()

	if !nextChange.IsZero() && now.Before(nextChange) {
		// Mid-fixation: a tracked target keeps being followed, because a person
		// watches what they decided to look at rather than staring at where it
		// used to be.
		if targetType == "player" {
			if pos, ok := tc.playerPositionByID(targetID); ok {
				tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 1.62, 0}))
				tc.applyMicroSaccade()
				return
			}
		} else if targetType == "actor" {
			if pos, ok := tc.actorPositionByID(targetID); ok {
				tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 0.9, 0}))
				tc.applyMicroSaccade()
				return
			}
		} else if targetType == "block" {
			tc.setLookTarget(targetPos)
			tc.applyMicroSaccade()
			return
		} else {
			tc.TargetYaw = targetYaw
			tc.TargetPitch = GazePitchFloor(targetPitch)
			tc.applyMicroSaccade()
			return
		}
	}

	// Start of a new fixation. A tracked target still wins when there is one:
	// a person who notices another player looks at them, and looking at
	// nothing else while someone walks past is the giveaway.
	//
	// Jev's gaze goes first, but only as a preference rather than an order. A
	// model that asks for a player and gets a wall because nobody was there looks
	// broken in a way that no amount of extra realism elsewhere repairs, so a
	// request with nothing behind it falls through to the same cascade the bot
	// would have run anyway.
	if tc.applyPreferredGaze(now) {
		return
	}
	if roll := rand.Intn(100); roll < 45 {
		if id, pos, ok := tc.nearestIdlePlayer(14); ok {
			tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 1.62, 0}))
			tc.storeIdleLook("player", id, mgl32.Vec3{}, now.Add(idleLookHold(float32(rand.Float64()))))
			return
		}
	}
	if roll := rand.Intn(100); roll < 75 {
		if id, pos, ok := tc.nearestIdleActor(12); ok {
			tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 0.9, 0}))
			tc.storeIdleLook("actor", id, mgl32.Vec3{}, now.Add(idleLookHold(float32(rand.Float64()))))
			return
		}
	}
	if roll := rand.Intn(100); roll < 88 {
		if pos, ok := tc.randomIdleBlock(8); ok {
			tc.setLookTarget(pos)
			tc.storeIdleLook("block", 0, pos, now.Add(idleLookHold(float32(rand.Float64()))))
			return
		}
	}

	// No target worth looking at: let the saccade model take the head. This is
	// the path a bot takes when it is genuinely on its own, which is exactly
	// when a human's idle eye movement is most visible.
	sample := NextGazeSample(targetYaw, targetPitch)
	tc.TargetYaw = normalizeYaw(targetYaw + sample.DYaw)
	tc.TargetPitch = GazePitchFloor(targetPitch + sample.DPitch)
	tc.storeIdleLook("wander", 0, mgl32.Vec3{}, now.Add(time.Duration(sample.Sec*float32(time.Second))))
}

// applyPreferredGaze honours Jev's latched attention for the next fixation, and
// reports whether it took effect.
//
// Every branch here does exactly what the untargeted cascade below would have
// done for the same kind of thing — same target choice, same hold duration, same
// micro-saccade. The model picks the subject, never the motion. Where the
// subject is not there, the answer is false and the caller carries on, so the
// bot's own rules still decide.
func (tc *TickContext) applyPreferredGaze(now time.Time) bool {
	switch tc.B.GazePreference() {
	case "person":
		if id, pos, ok := tc.nearestIdlePlayer(14); ok {
			tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 1.62, 0}))
			tc.storeIdleLook("player", id, mgl32.Vec3{}, now.Add(idleLookHold(float32(rand.Float64()))))
			tc.applyMicroSaccade()
			return true
		}
	case "mob":
		if id, pos, ok := tc.nearestIdleActor(12); ok {
			tc.setNaturalLookTarget(pos.Add(mgl32.Vec3{0, 0.9, 0}))
			tc.storeIdleLook("actor", id, mgl32.Vec3{}, now.Add(idleLookHold(float32(rand.Float64()))))
			tc.applyMicroSaccade()
			return true
		}
	case "block":
		if pos, ok := tc.randomIdleBlock(8); ok {
			tc.setLookTarget(pos)
			tc.storeIdleLook("block", 0, pos, now.Add(idleLookHold(float32(rand.Float64()))))
			tc.applyMicroSaccade()
			return true
		}
	case "around":
		// Nothing in particular. This is a real choice, not a fallback: a person
		// watching empty air is not staring blankly, it is scanning, and it is
		// the most visible idle eye movement there is.
		targetYaw, targetPitch := tc.currentIdleGaze()
		sample := NextGazeSample(targetYaw, targetPitch)
		tc.TargetYaw = normalizeYaw(targetYaw + sample.DYaw)
		tc.TargetPitch = GazePitchFloor(targetPitch + sample.DPitch)
		tc.storeIdleLook("wander", 0, mgl32.Vec3{}, now.Add(time.Duration(sample.Sec*float32(time.Second))))
		return true
	}
	return false
}

// currentIdleGaze reads where the head is resting, which is what a scan continues
// from. Reading it here rather than passing it down keeps the two fix-up paths
// from being able to disagree about the starting point.
func (tc *TickContext) currentIdleGaze() (float32, float32) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	return tc.B.IdleLookTargetYaw, tc.B.IdleLookTargetPitch
}

// applyMicroSaccade adds a small corrective offset to the current target. It
// is what keeps a held gaze alive: without it the head holds a mathematically
// exact angle for the whole fixation, which is the one thing a camera does and
// a person does not.
func (tc *TickContext) applyMicroSaccade() {
	dYaw, dPitch := MicroSaccade()
	if dYaw == 0 && dPitch == 0 {
		return
	}
	tc.TargetYaw = normalizeYaw(tc.TargetYaw + dYaw)
	tc.TargetPitch = GazePitchFloor(tc.TargetPitch + dPitch)
}

// idleLookHold converts a sampled fixation into a duration. Tracking a real
// target (a person, a mob) holds attention longer than scanning empty air, so
// the same skewed draw is used but scaled up a little.
func idleLookHold(t float32) time.Duration {
	sec := SampleFixation() * 1.4
	if t < 0.2 {
		// The occasional long look at a person who is actually interesting.
		sec += 1.2
	}
	return time.Duration(sec * float32(time.Second))
}

func (tc *TickContext) setLookTarget(pos mgl32.Vec3) {
	dx := pos.X() - tc.CurrPos.X()
	dy := pos.Y() - (tc.CurrPos.Y() + 1.62)
	dz := pos.Z() - tc.CurrPos.Z()
	distH := math.Sqrt(float64(dx*dx + dz*dz))
	if distH < 0.001 {
		distH = 0.001
	}
	tc.TargetYaw = normalizeYaw(float32(math.Atan2(float64(dz), float64(dx))*180/math.Pi) - 90)
	tc.TargetPitch = float32(-math.Atan2(float64(dy), distH) * 180 / math.Pi)
}

func (tc *TickContext) setNaturalLookTarget(pos mgl32.Vec3) {
	tc.TargetYaw, tc.TargetPitch = NaturalLookAngles(tc.CurrPos, pos, tc.Yaw)
}

// lookTargetSmoothing is the per-tick EMA factor for the stationary look
// target. At 20 ticks/s, 0.10 gives a ~0.5 s time constant: deliberate glides
// still feel immediate, while a raw target alternating between two quantized
// positions collapses to a flat line before the eased head ever reacts.
const lookTargetSmoothing = 0.10

const (
	// lookSaccadeYaw/Pitch are the raw-target jumps that snap the EMA instead
	// of slewing through it, so a fresh idle look or a look-at command still
	// turns the head promptly.
	lookSaccadeYaw   = 45.0
	lookSaccadePitch = 30.0

	// LookLevelBand is the half-width of the soft level band applied after the
	// EMA. Inside it the pitch is attenuated quadratically: continuous at the
	// band edge, ~0 at level. A hard cut here used to chatter whenever a
	// small height difference put the raw pitch right on the threshold.
	LookLevelBand = 1.5
)

// SmoothStationaryLookTarget low-pass filters the stationary look target and
// applies a soft level band. It replaces the old hard deadzone plus
// freeze-to-current damping, which had two defects that showed up as a visible
// head tremor while tracking a nearby player:
//
//  1. The hard deadzone snapped the target between 0 and the raw angle
//     whenever the raw pitch hovered at the threshold (a few centimetres of
//     height difference between bot and player, plus position quantization).
//  2. Freezing the target to the current angle for small differences left the
//     head permanently short of its true angle by up to ~1.4 degrees, which
//     read as aiming slightly above the player's head.
//
// Pinned static looks (drop aiming, forced angles) bypass the smoothing so
// action code that waits on a specific angle still converges quickly.
func (tc *TickContext) SmoothStationaryLookTarget() {
	if tc.pinnedStaticLook() {
		tc.SmoothedLookYaw, tc.SmoothedLookPitch = tc.TargetYaw, tc.TargetPitch
		return
	}

	rawYaw, rawPitch := tc.TargetYaw, tc.TargetPitch

	if math.Abs(float64(AngleDifference(rawYaw, tc.SmoothedLookYaw))) > lookSaccadeYaw {
		tc.SmoothedLookYaw = rawYaw
	} else {
		tc.SmoothedLookYaw = normalizeYaw(tc.SmoothedLookYaw + lookTargetSmoothing*AngleDifference(rawYaw, tc.SmoothedLookYaw))
	}

	if math.Abs(float64(rawPitch-tc.SmoothedLookPitch)) > lookSaccadePitch {
		tc.SmoothedLookPitch = rawPitch
	} else {
		tc.SmoothedLookPitch += lookTargetSmoothing * (rawPitch - tc.SmoothedLookPitch)
	}

	tc.TargetYaw = tc.SmoothedLookYaw
	tc.TargetPitch = SoftenLevelBand(tc.SmoothedLookPitch)
}

// pinnedStaticLook reports whether an action pinned the look angles and the
// eased head must reach exactly that angle without extra lag.
func (tc *TickContext) pinnedStaticLook() bool {
	tc.B.Mu.Lock()
	pinned := tc.B.IdleLookTargetType == "static"
	tc.B.Mu.Unlock()
	return pinned
}

// SoftenLevelBand attenuates pitch quadratically inside ±LookLevelBand and
// passes it through unchanged outside. Unlike a hard zero, it is continuous,
// so an input hovering at the band edge cannot make the output chatter.
func SoftenLevelBand(pitch float32) float32 {
	mag := absFloat32(pitch)
	if mag >= LookLevelBand {
		return pitch
	}
	scale := (mag / LookLevelBand) * (mag / LookLevelBand)
	return pitch * scale
}

func NaturalLookAngles(originFeet, target mgl32.Vec3, currentYaw float32) (float32, float32) {
	dx := target.X() - originFeet.X()
	dy := target.Y() - (originFeet.Y() + 1.62)
	dz := target.Z() - originFeet.Z()
	distH := math.Sqrt(float64(dx*dx + dz*dz))

	yaw := currentYaw
	if distH >= 0.35 {
		yaw = normalizeYaw(float32(math.Atan2(float64(dz), float64(dx))*180/math.Pi) - 90)
	}

	pitchDist := distH
	if pitchDist < 0.35 {
		pitchDist = 0.35
	}
	pitch := float32(-math.Atan2(float64(dy), pitchDist) * 180 / math.Pi)
	if distH < 1.0 {
		pitch = clampFloat32(pitch, -30, 30)
	} else {
		// Asymmetric clamp: real players idle-look slightly downward more
		// often than upward (ground, feet, blocks nearby). Capping the up
		// angle keeps the head from appearing stuck skyward.
		pitch = clampFloat32(pitch, -25, 25)
	}
	// No hard deadzone: the raw geometry is honest, and the stationary
	// smoothing stage (SmoothStationaryLookTarget) removes sub-degree noise
	// with a continuous filter instead of a discontinuous cut. A hard cut here
	// made the target snap between 0 and the raw angle whenever the raw pitch
	// hovered at the threshold, which read as a head tremor while tracking.
	return yaw, pitch
}

func (tc *TickContext) storeIdleLook(targetType string, targetID uint64, targetPos mgl32.Vec3, until time.Time) {
	tc.B.Mu.Lock()
	tc.B.IdleLookTargetType = targetType
	tc.B.IdleLookTargetID = targetID
	tc.B.IdleLookTargetPos = targetPos
	tc.B.IdleLookTargetYaw = tc.TargetYaw
	tc.B.IdleLookTargetPitch = tc.TargetPitch
	tc.B.NextIdleLookChange = until
	tc.B.Mu.Unlock()
}

func (tc *TickContext) playerPositionByID(id uint64) (mgl32.Vec3, bool) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	pos, ok := tc.B.PlayerPositions[id]
	return pos, ok
}

func (tc *TickContext) actorPositionByID(id uint64) (mgl32.Vec3, bool) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	act, ok := tc.B.Actors[id]
	if !ok || act == nil {
		return mgl32.Vec3{}, false
	}
	return act.Position, true
}

func (tc *TickContext) nearestIdlePlayer(maxDist float32) (uint64, mgl32.Vec3, bool) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()

	var bestID uint64
	var bestPos mgl32.Vec3
	bestDist := maxDist * maxDist
	found := false
	for id, pos := range tc.B.PlayerPositions {
		dx := pos.X() - tc.CurrPos.X()
		dy := pos.Y() - tc.CurrPos.Y()
		dz := pos.Z() - tc.CurrPos.Z()
		dist := dx*dx + dy*dy + dz*dz
		if dist <= bestDist {
			bestDist = dist
			bestPos = pos
			bestID = id
			found = true
		}
	}
	return bestID, bestPos, found
}

func (tc *TickContext) nearestIdleActor(maxDist float32) (uint64, mgl32.Vec3, bool) {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()

	var bestID uint64
	var bestPos mgl32.Vec3
	bestDist := maxDist * maxDist
	found := false
	for id, act := range tc.B.Actors {
		if act == nil {
			continue
		}
		dx := act.Position.X() - tc.CurrPos.X()
		dy := act.Position.Y() - tc.CurrPos.Y()
		dz := act.Position.Z() - tc.CurrPos.Z()
		dist := dx*dx + dy*dy + dz*dz
		if dist <= bestDist {
			bestDist = dist
			bestPos = act.Position
			bestID = id
			found = true
		}
	}
	return bestID, bestPos, found
}

func (tc *TickContext) randomIdleBlock(maxDist int32) (mgl32.Vec3, bool) {
	feetX := int32(math.Floor(float64(tc.CurrPos.X())))
	feetY := int32(math.Floor(float64(tc.CurrPos.Y())))
	feetZ := int32(math.Floor(float64(tc.CurrPos.Z())))

	for attempt := 0; attempt < 36; attempt++ {
		dx := safecast.To[int32](rand.Intn(int(maxDist*2+1))) - maxDist
		dz := safecast.To[int32](rand.Intn(int(maxDist*2+1))) - maxDist
		if dx*dx+dz*dz < 4 {
			continue
		}
		// Bias dy downward/level (-1..+1) so the bot's idle gaze stays near
		// the horizon instead of picking blocks above eye level and pinning
		// the head upward for seconds at a time.
		dy := safecast.To[int32](rand.Intn(3)) - 1
		x, y, z := feetX+dx, feetY+dy, feetZ+dz
		if tc.B.WorldModel.IsSolid(x, y, z) &&
			!tc.B.WorldModel.IsHazard(x, y, z) &&
			!tc.B.WorldModel.IsSolid(x, y+1, z) {
			return mgl32.Vec3{float32(x) + 0.5, float32(y) + 0.55, float32(z) + 0.5}, true
		}
	}
	return mgl32.Vec3{}, false
}

func clampFloat32(v, min, max float32) float32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// Pitch stops just short of straight up and straight down.
//
// The protocol allows a pitch anywhere in [-90, 90], but a packet carrying
// exactly -90.0 or exactly +90.0 is a value a human client cannot produce. The
// two extremes are where the pitch derivative vanishes, so the eased angle
// asymptotes toward them and, once it has rounded to float32, sits exactly on
// the boundary for as long as the bot keeps looking that way. Mining a block
// directly underfoot does exactly that, which is why this showed up as a hard
// boundary rather than a rare rounding artefact.
//
// The margin is deliberately small. A human's neck does not actually reach
// 90 degrees -- measured flexion is around 70 -- so a tighter cap would be more
// faithful still, but looking straight down is a legitimate action the bot has
// to perform for mining and for the ground clearance work, and capping at 70
// would put the crosshair off the block it is aiming at. Holding half a tenth
// of a degree of daylight instead keeps every one of those actions aimed
// correctly while never emitting the impossible value.
const (
	// PitchBoundary is the protocol's limit.
	PitchBoundary = 90.0
	// PitchMargin is how far inside it a packet is allowed to sit.
	PitchMargin = 0.05
)

// ClampPitch bounds a pitch to the range a real client reports.
//
// Unlike a plain clampFloat32(v, -90, 90) it never returns the boundary value
// itself, which is the whole point: an out-of-range request comes back just
// inside the limit rather than exactly on it.
func ClampPitch(pitch float32) float32 {
	limit := float32(PitchBoundary - PitchMargin)
	return clampFloat32(pitch, -limit, limit)
}

func AngleDifference(target, current float32) float32 {
	diff := target - current
	for diff < -180 {
		diff += 360
	}
	for diff > 180 {
		diff -= 360
	}
	return diff
}

func normalizeYaw(yaw float32) float32 {
	for yaw < 0 {
		yaw += 360
	}
	for yaw >= 360 {
		yaw -= 360
	}
	return yaw
}
