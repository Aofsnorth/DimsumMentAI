// Swimming locomotion: what the movement input does once the body is in water.
//
// Before this file, a bot that walked into a river walked in and stopped. The
// steering layer has one vertical model — gravity, ladders, jumps — and none of
// those describe a body pushing itself along at the waterline or driving down
// toward something on the riverbed. Everything here is derived from two
// questions the world model can already answer: is this cell water, and where
// is the air above me.
//
// PlanSwim is pure and decides the mode and the drive magnitudes. The steering
// layer keeps owning the heading, because the heading comes from the path and
// the path does not care what medium the body is in.

package movement

import (
	"io"
	"log/slog"
	"math"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// SurfaceSearchLimit bounds how far up the body looks for breathable air. A
// flooded mine shaft is a handful of blocks; a hundred is a pathological
// column, and searching for one is a bot standing at the bottom of it doing
// nothing while the air bar runs out.
const SurfaceSearchLimit int32 = 24

// WaterWorld is the narrow view of the world that water movement needs. It is
// declared here rather than reusing the pathfinder's model so the swim planner
// can be exercised against a hand-written map of cells, and so depending on
// this package does not drag the whole search into every caller.
//
// A model that cannot answer is treated as dry, which is the safe direction:
// the controller never plans a dive rather than planning one on a guess.
type WaterWorld interface {
	IsWater(x, y, z int32) bool
}

// Bot is the slice of the bot that swimming needs — the same narrow-interface
// shape the storage and survival subsystems use, so the controller can be
// driven by a fake in a test and by *bot.Bot in production.
type Bot interface {
	GetCoords() mgl32.Vec3
	GetLocalWorldModel() entity.WorldModel
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	NavigateTo(pos mgl32.Vec3)
	StopMovement()
}

// Submersion is what the water is doing to the body at one tick.
//
// It is a sample, not a model: the controller rebuilds it every tick, and
// nothing else is allowed to cache a water fact, because a bucket and a piston
// and a waterfall all move water while a river does not.
type Submersion struct {
	FeetX, FeetY, FeetZ int32
	// InWater is true when the feet cell holds liquid.
	InWater bool
	// Submerged is true when the head cell holds liquid. Only this one drains
	// the air bar, so it is the only one the breath rules read. Waist-deep is
	// not submerged, and a bot that treats it as submerged stops working every
	// time it wades through a puddle.
	Submerged bool
	// SurfaceY is the feet cell the body must reach for the head to clear the
	// water. When HasSurface is false it is the top of the search window and
	// means nothing else.
	SurfaceY int32
	// ClimbBlocks is SurfaceY - FeetY: how far the body must rise to breathe.
	// It is also the number of water cells stacked above the head, which is why
	// one field serves both readings.
	ClimbBlocks int32
	// HasSurface records whether that surface was actually found.
	HasSurface bool
}

// SampleSubmersion reads the world once and reports the water state of the body.
//
// The head cell is the whole answer about drowning, and the surface scan is
// measured from the feet so the caller gets a distance it can act on rather
// than a boolean it has to re-derive.
func SampleSubmersion(world WaterWorld, feetX, feetY, feetZ int32) Submersion {
	if world == nil {
		return Submersion{FeetX: feetX, FeetY: feetY, FeetZ: feetZ}
	}

	_, headY, _ := pathfinder.HeadCell(feetX, feetY, feetZ)
	sub := Submersion{
		FeetX:     feetX,
		FeetY:     feetY,
		FeetZ:     feetZ,
		InWater:   world.IsWater(feetX, feetY, feetZ),
		Submerged: world.IsWater(feetX, headY, feetZ),
	}

	for climb := int32(0); climb <= SurfaceSearchLimit; climb++ {
		_, probeHeadY, _ := pathfinder.HeadCell(feetX, feetY+climb, feetZ)
		if world.IsWater(feetX, probeHeadY, feetZ) {
			continue
		}
		sub.SurfaceY = feetY + climb
		sub.ClimbBlocks = climb
		sub.HasSurface = true
		return sub
	}

	// No air in the window. The top of the window is the honest answer to "how
	// far would I have to climb", and HasSurface is what stops anyone acting on
	// it as if it were a real surface.
	sub.SurfaceY = feetY + SurfaceSearchLimit
	sub.ClimbBlocks = SurfaceSearchLimit
	return sub
}

// SwimMode is the swimming behaviour this tick.
type SwimMode string

const (
	// SwimModeDry is the ordinary case: land, no water, nothing to do.
	SwimModeDry SwimMode = "dry"
	// SwimModeSurface is a body floating with its head out — the river crossing.
	SwimModeSurface SwimMode = "surface"
	// SwimModeSwim is a body fully under, travelling horizontally.
	SwimModeSwim SwimMode = "swim"
	// SwimModeAscend is a climb to the surface or back to a dive target.
	SwimModeAscend SwimMode = "ascend"
	// SwimModeDescend is a deliberate dive.
	SwimModeDescend SwimMode = "descend"
)

// Swim speeds, as analogue drive magnitudes in [0,1]. They are lower than a
// land sprint because water is genuinely slower: sending the land number would
// not make the bot swim faster, only make the server's prediction fight the
// packet.
const (
	defaultSwimSpeed    float32 = 0.55
	defaultSurfaceSpeed float32 = 0.35
	defaultAscendSpeed  float32 = 0.30

	// swimDivePitch and swimAscendPitch tip the head where the body is going.
	// Looking where you swim is most of what separates a dive from a body being
	// dragged under.
	swimDivePitch   float32 = 35
	swimAscendPitch float32 = -25

	// surfaceFloatBias is the small continuous rise a body at the waterline is
	// given. The movement loop applies gravity on every tick that is not
	// grounded and has no idea what water is, so a floating body that is not
	// pushed up sinks a little further every tick until its head is under and
	// the breath reflex has to rescue it from its own buoyancy. A quarter of
	// the full climb holds the waterline without visibly pumping.
	surfaceFloatBias float32 = 0.25

	// swimVerticalDrive converts the plan's normalised Vertical axis into the
	// per-tick Y velocity the ground physics would otherwise own. It is sized
	// against the free-fall constant: the ground branch subtracts 0.08 every
	// ungrounded tick, so a drive smaller than that cannot climb, and a drive
	// larger than the jump velocity of 0.42 makes a dive feel like a rocket.
	// Holding jump is the client's own swim-up, and 0.34 is what that produces.
	swimVerticalDrive float32 = 0.34
)

// SwimOptions are the tunables of the swim plan.
type SwimOptions struct {
	SwimSpeed    float32
	SurfaceSpeed float32
	AscendSpeed  float32
	// Sprint asks for sprint-swimming. It is ignored at the surface, where a
	// player who is not under water does not sprint.
	Sprint bool
}

// DefaultSwimOptions is the production tuning.
func DefaultSwimOptions() SwimOptions {
	return SwimOptions{
		SwimSpeed:    defaultSwimSpeed,
		SurfaceSpeed: defaultSurfaceSpeed,
		AscendSpeed:  defaultAscendSpeed,
		Sprint:       true,
	}
}

// SwimIntent is the per-tick decision: the mode, and how hard to push in each
// direction. It carries magnitudes, not a heading — the heading is the path's.
type SwimIntent struct {
	Mode SwimMode
	// Strafe and Forward are the analogue horizontal drive, matching the shape
	// of TickContext.MoveVec: strafe on X, forward on Y.
	Strafe  float32
	Forward float32
	// Vertical is the swim up/down drive in [-1,1]; positive rises. On the wire
	// it is the Up and Down input flags, and a client holds jump to rise and
	// sneak to sink.
	Vertical float32
	// Jump, Sneak and Sprint are the keys a player actually holds.
	Jump   bool
	Sneak  bool
	Sprint bool
	// Pitch is the look pitch the mode wants.
	Pitch float32
	// InWater and Surfaced are the observation the plan was made from, carried
	// out so the caller can log the real state rather than re-deriving it.
	InWater  bool
	Surfaced bool
}

// PlanSwim turns an observation and a depth decision into a swim plan. It is
// pure, which is what makes "the bot surfaces before it drowns" and "the bot
// crosses a river" checkable without a server.
func PlanSwim(sub Submersion, depth DepthPlan, opts SwimOptions) SwimIntent {
	intent := SwimIntent{
		InWater:  sub.InWater,
		Surfaced: sub.InWater && !sub.Submerged,
	}
	if !sub.InWater && !sub.Submerged {
		intent.Mode = SwimModeDry
		return intent
	}

	switch depth.Action {
	case DepthAscend:
		intent.Mode = SwimModeAscend
		intent.Forward = opts.AscendSpeed
		intent.Vertical = 1
		intent.Pitch = swimAscendPitch
		// Holding jump is how a client swims up. Once the head is clear the
		// hold has to stop or the body bobs forever at the waterline.
		intent.Jump = sub.Submerged
	case DepthDescend:
		intent.Mode = SwimModeDescend
		intent.Forward = opts.SwimSpeed
		intent.Vertical = -1
		intent.Pitch = swimDivePitch
		intent.Sneak = true
	case DepthHold:
		if sub.Submerged {
			intent.Mode = SwimModeSwim
			intent.Forward = opts.SwimSpeed
		} else {
			// The head is clear, so the only job left is not to sink.
			intent.Mode = SwimModeSurface
			intent.Forward = opts.SurfaceSpeed
			intent.Vertical = surfaceFloatBias
		}
	}

	intent.Sprint = opts.Sprint && sub.Submerged
	return intent
}

// SwimMoveVector scales an already-computed heading into this tick's analogue
// move vector. Multiplying rather than replacing means a swim still follows the
// path instead of swimming at a fixed bearing.
//
// PlanSwim never sets Strafe, and the zero is deliberate rather than forgotten:
// a swimming body turns to face where it is going, it does not strafe.
func SwimMoveVector(intent SwimIntent, heading mgl32.Vec2) mgl32.Vec2 {
	if intent.Mode == SwimModeDry {
		return mgl32.Vec2{}
	}
	return mgl32.Vec2{heading.X() * intent.Strafe, heading.Y() * intent.Forward}
}

// ApplySwimInputFlags writes the swim half of a PlayerAuthInput's input flags.
//
// It is a separate exported call because a normal tick's flags are assembled in
// packet.go's buildInputData, shared with every other movement mode. The swim
// flags are additive and mode-local: the caller hands in the flag set it was
// going to send anyway and this adds the swim transitions on top.
//
// wasSwimming is the previous tick's state because StartSwimming and
// StopSwimming are edges. A real client sends each exactly once, on the tick
// the state changes; re-sending every tick is a different client.
func ApplySwimInputFlags(flags *protocol.InputFlags, intent SwimIntent, wasSwimming bool) {
	if flags == nil {
		return
	}
	switch {
	case intent.InWater && !wasSwimming:
		setSwimFlag(flags, packet.InputFlagStartSwimming)
	case !intent.InWater && wasSwimming:
		setSwimFlag(flags, packet.InputFlagStopSwimming)
	}
	if intent.Jump {
		setSwimFlag(flags, packet.InputFlagJumping)
	}
	if intent.Sneak {
		setSwimFlag(flags, packet.InputFlagSneaking)
	}
	if intent.Sprint {
		setSwimFlag(flags, packet.InputFlagSprinting)
	}
	if intent.Vertical > 0.1 {
		setSwimFlag(flags, packet.InputFlagUp)
	}
	if intent.Vertical < -0.1 {
		setSwimFlag(flags, packet.InputFlagDown)
	}
	if intent.Strafe > 0.1 {
		setSwimFlag(flags, packet.InputFlagRight)
	} else if intent.Strafe < -0.1 {
		setSwimFlag(flags, packet.InputFlagLeft)
	}
}

// setSwimFlag guards the size check protocol.InputFlags.Set insists on. The zero
// InputFlags has no size and Set panics on an index past the end, so a helper
// that called it blindly would take the bot down from inside the movement loop
// on the first malformed caller. Production always sizes from
// packet.InputFlagCount; this is what makes the helper safe everywhere else.
func setSwimFlag(flags *protocol.InputFlags, id int) {
	if flags.Len() <= id {
		return
	}
	flags.Set(id)
}

// SwimController holds the two things a swim needs that a pure function cannot:
// the dive the caller asked for, and the clock that says how long the head has
// been under.
type SwimController struct {
	bot    Bot
	logger *slog.Logger
	opts   SwimOptions

	mu       sync.Mutex
	dive     DiveIntent
	under    time.Time
	swimming bool
	budget   time.Duration
	now      func() time.Time
	// lastSub is the submersion the most recent plan sampled, kept so Breath
	// can answer without re-reading the world.
	lastSub   *Submersion
	blindSeen time.Time
}

// NewSwimController builds a controller over a narrow bot slice.
func NewSwimController(bot Bot, logger *slog.Logger) *SwimController {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &SwimController{
		bot:    bot,
		logger: logger,
		opts:   DefaultSwimOptions(),
		budget: DefaultBreathBudget,
		now:    time.Now,
	}
}

// Water narrows the bot's world model to the water question, or nil. The
// narrowing happens after the fact, the way the pathfinder already does with
// loadAwareWorld: widening entity.WorldModel for one caller would push the
// change through every implementer in the tree.
func (s *SwimController) Water() WaterWorld {
	if s == nil || s.bot == nil {
		return nil
	}
	model := s.bot.GetLocalWorldModel()
	if model == nil {
		return nil
	}
	water, ok := model.(WaterWorld)
	if !ok {
		return nil
	}
	return water
}

// SetBreathBudget overrides the air bar. Production leaves it at the vanilla
// fifteen seconds.
func (s *SwimController) SetBreathBudget(budget time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if budget > 0 {
		s.budget = budget
	}
}

// SetDive asks the controller to hold a depth. The request is parked, not
// started: nothing happens until the body is in water, and the breath reserve
// can suspend it at any time.
func (s *SwimController) SetDive(targetY int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dive = NewDiveIntent(targetY)
}

// ClearDive drops the request.
func (s *SwimController) ClearDive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dive = DiveIntent{}
}

// DiveIntent reports the current request.
func (s *SwimController) DiveIntent() (targetY int32, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dive.TargetY, s.dive.Active
}

// IsSwimming reports whether the last applied plan had the body in water.
func (s *SwimController) IsSwimming() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.swimming
}

// Sample reads the water around the bot. The second result is false when there
// is no world that can answer, which is a different thing from "the bot is dry"
// and must not be collapsed into it.
func (s *SwimController) Sample() (Submersion, bool) {
	world := s.Water()
	if world == nil {
		return Submersion{}, false
	}
	x, y, z := s.feetCell()
	return SampleSubmersion(world, x, y, z), true
}

// feetCell is the body cell the world is asked about. Floor, not truncation:
// int32() truncates toward zero, so a negative coordinate would be read one
// cell off — the same off-by-one the path smoother documents at canWalkLine.
func (s *SwimController) feetCell() (int32, int32, int32) {
	pos := s.bot.GetCoords()
	return blockCell(pos.X()), blockCell(pos.Y()), blockCell(pos.Z())
}

func blockCell(v float32) int32 { return int32(math.Floor(float64(v))) }

// Plan samples the world, updates the submersion clock, and returns this tick's
// swim plan. The second result is false when there is no water-capable world,
// in which case nothing should be sent to the server on the plan's account.
func (s *SwimController) Plan() (SwimIntent, bool) {
	world := s.Water()
	if world == nil {
		return PlanSwim(Submersion{}, DepthPlan{Action: DepthHold}, s.opts), false
	}

	x, y, z := s.feetCell()
	sub := SampleSubmersion(world, x, y, z)
	s.remember(sub)
	depth := PlanDepth(sub, s.currentDive(), s.advanceBreathClock(sub))
	s.reportBlindClimb(sub, depth)
	return PlanSwim(sub, depth, s.opts), true
}

// PlanFrom is Plan for a caller that already sampled the submersion this tick,
// which is what the movement loop wants: one world read, not two.
func (s *SwimController) PlanFrom(sub Submersion) SwimIntent {
	s.remember(sub)
	depth := PlanDepth(sub, s.currentDive(), s.advanceBreathClock(sub))
	s.reportBlindClimb(sub, depth)
	return PlanSwim(sub, depth, s.opts)
}

// remember keeps the last sample for Breath to read.
func (s *SwimController) remember(sub Submersion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := sub
	s.lastSub = &copied
}

func (s *SwimController) currentDive() DiveIntent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dive
}

// advanceBreathClock starts, keeps and clears the submersion timer, and reports
// the breath reading for this tick.
//
// It resets on the surface rather than decaying, so surfacing for one tick and
// ducking back down does not hand the bot a fresh air supply it never had.
//
// The clock starts when the controller first observes the head under, not when
// it went under, because a sample is all the controller has. That makes it
// count later and surface later — the direction that drowns — and the reserve
// is what absorbs the difference.
func (s *SwimController) advanceBreathClock(sub Submersion) BreathState {
	now := s.nowFunc()()

	s.mu.Lock()
	defer s.mu.Unlock()
	if sub.Submerged {
		if s.under.IsZero() {
			s.under = now
		}
	} else {
		s.under = time.Time{}
	}

	secondsUnder := 0
	if !s.under.IsZero() {
		secondsUnder = int(now.Sub(s.under) / time.Second)
	}
	return NewBreathState(secondsUnder, int(s.budget/time.Second))
}

func (s *SwimController) nowFunc() func() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.now == nil {
		return time.Now
	}
	return s.now
}

// Breath reports the current submersion reading without advancing the clock.
//
// It exists so the AGI breath reflex and the swim plan cannot disagree. Both
// used to keep their own record of when the head went under, sampled from the
// same world on the same tick, and the two could be a second or an air-bar's
// worth of air apart — the reflex deciding there was time left while the plan
// had already committed to a dive, or the other way round.
//
// Read-only is the point. Callers must not be able to start, stop or age the
// clock, or a second reader would become a second writer and the disagreement
// would come back in a worse shape. The reading is whatever the last plan saw;
// the third result is false before the first plan of a connection, so a caller
// that arrives early knows it has no reading rather than reading a fresh air
// bar.
func (s *SwimController) Breath() (underwater bool, secondsUnder int, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastSub == nil {
		return false, 0, false
	}
	if !s.lastSub.Submerged {
		return false, 0, true
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	return true, int(now().Sub(s.under) / time.Second), true
}

// reportBlindClimb says out loud that the body is climbing toward a surface
// nobody can see. A flooded shaft is a real failure, and a log line that does
// not say so is how it becomes a mystery. Rate-limited, because a stuck body
// would otherwise write the same warning twenty times a second.
func (s *SwimController) reportBlindClimb(sub Submersion, depth DepthPlan) {
	if !depth.Blind || !sub.Submerged {
		return
	}
	now := s.nowFunc()()

	s.mu.Lock()
	recent := !s.blindSeen.IsZero() && now.Sub(s.blindSeen) < 5*time.Second
	s.blindSeen = now
	s.mu.Unlock()

	if recent {
		return
	}
	s.logger.Warn("swim: climbing blind, no air found above the bot",
		"y", int(sub.FeetY),
		"searched_blocks", int(SurfaceSearchLimit),
		"action", string(depth.Action),
	)
}

// ApplyInput writes the swim flags onto the flag set for this tick and records
// the swimming state for the next one.
func (s *SwimController) ApplyInput(flags *protocol.InputFlags, intent SwimIntent) {
	was := s.IsSwimming()
	ApplySwimInputFlags(flags, intent, was)

	s.mu.Lock()
	s.swimming = intent.InWater
	s.mu.Unlock()
}

// NavigateToUnderwater asks the bot to reach a cell under water, holding depth
// on the way.
//
// The result is reported honestly: a world that cannot answer, a body that is
// not in water, a target with no water in it, and a pathfinder that did not
// arrive are four different failures and each says which one it was. Returning
// true for any of them would be a dive that never happened.
func (s *SwimController) NavigateToUnderwater(x, y, z int32, tolerance float32) (bool, string) {
	world := s.Water()
	if world == nil {
		return false, "the world model cannot answer questions about water"
	}

	sub, known := s.Sample()
	if !known {
		return false, "the world model cannot answer questions about water"
	}
	if !sub.InWater && !sub.Submerged {
		return false, "the bot is not in water"
	}
	if !world.IsWater(x, y, z) && !world.IsWater(x, y+1, z) {
		return false, "the target cell is not in water"
	}

	s.SetDive(y)
	if !s.bot.NavigateToBlock(x, y, z, tolerance) {
		return false, "the pathfinder did not reach the underwater target"
	}
	return true, ""
}
