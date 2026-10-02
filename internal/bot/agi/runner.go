package agi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/bot/affordance"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/bot/perception"
	"bedrock-ai/internal/bot/rand"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/evidence"
	"bedrock-ai/internal/jev"

	"github.com/go-gl/mathgl/mgl32"
)

// entityTargetEyeHeight was a fixed guess at where a player's eyes sit. It now
// comes from config as EyeHeight — the same value the bot's own sight rays use,
// so the two cannot drift apart.

// sightReader reports whether the bot knows about a world cell, for the
// line-of-sight walk entity visibility performs. It reads the world model
// rather than the chunk cache because the cache is 16-block granular: once any
// cell in a chunk is stored the whole chunk claims to be loaded, which would let
// sight pass through a gap the bot has no knowledge of.
type sightReader struct {
	b *bot.Bot
}

func (s sightReader) GetBlockName(x, y, z int32) (string, bool) {
	if s.b.WorldModel == nil || !s.b.WorldModel.IsLoaded(x, y, z) {
		return "", false
	}
	return "minecraft:air", true
}

// Runner is the autonomy loop. It owns everything the brain needs to touch on
// the bot, and keeps its own state so a rejoin starts from a clean slate
// instead of inheriting a cooldown from the previous world.
type Runner struct {
	b   *bot.Bot
	cfg Config

	mu        sync.Mutex
	lastSpoke time.Time
	lastGaze  time.Time
	// greeted remembers who the vision reflex has already reacted to, and
	// greetedAt when. The record outlives the gaze on purpose: ReflexLook
	// consumes an entire tick, so re-firing it every time a gaze lapses starves
	// the decision layer and the bot stands there looking at people instead of
	// playing.
	greeted   map[string]time.Time
	greetedAt time.Time
	seed      int
	// goal is the multi-tick objective the bot is currently pursuing. It is
	// empty when the bot has no goal, which is a supported state (and the
	// correct one at the start of a session): the brain then just wanders.
	goal Goal
	// plan is the long-horizon objective used by planning mode. It is separate
	// from goal on purpose: a goal is what the bot wants for the next few
	// minutes and is chosen by Jev, while a plan is what it is trying to achieve
	// over a whole expedition and is chosen by the planner. The two nest — a
	// plan's current step is what the current goal should be serving.
	plan Plan
	// planSeq numbers generated plan IDs so a replan is distinguishable from the
	// plan it replaced in the log.
	planSeq int
	// plannerInFlight and lastPlanAttempt throttle the planner. Without them a
	// planner that is down — or a bot that keeps finishing plans — would be
	// asked again on every single tick, and the log would fill with the same
	// failure while the brain did nothing.
	plannerInFlight bool
	lastPlanAttempt time.Time
	// busy is true while a plan step is executing. Planning mode runs one
	// action at a time on purpose: two steps in flight would be two sets of
	// instructions fighting over the same body, and the second one would be
	// reported as progress the first had already undone.
	busy bool
	// sinceSubmerged is when the bot's head last went under. Zero means it is
	// breathing. The breath reflex reads the gap between this and now, which is
	// the only air warning this bot actually has: there is no air bar to read.
	//
	// It is a fallback. When swimBreath is wired the reflex reads the movement
	// package's clock instead, because two clocks sampling the same body on the
	// same tick can be an air bar apart and the two halves of the same decision
	// then disagree. With nothing wired this is the only record there is, and
	// the reflex still works.
	sinceSubmerged time.Time
	// swimBreath is the optional seam onto the movement package's swim
	// controller. It is an interface rather than a concrete type so this package
	// keeps no dependency on the movement package.
	swimBreath BreathSource
	// episode is the recording brief: an objective with a hard wall-clock
	// deadline. Empty is the normal state for hours at a time and is not a gap.
	episode Episode
	// vocabulary is what this server has been observed to contain. It replaces
	// a hardcoded survival list as the bot's working vocabulary, which is what
	// lets the same brain play a survival world, a one-block world and a server
	// full of blocks it has never heard of.
	vocabulary *Vocabulary
	// oneBlockSeen, oneBlockReading and oneBlockConfirmed carry the two-reading
	// test. A hint is not a conclusion, and a world that looks empty twice from
	// the same spot is a different thing from one that looked empty once.
	oneBlockSeen      bool
	oneBlockReading   OneBlockConfidence
	oneBlockConfirmed bool
	// lastPosition is the previous tick's coordinates, for the stuck check. The
	// clock alone is not enough: a server that applies a freeze still answers
	// ticks, so a motionless bot and a healthy idle look identical on time.
	lastPosition string
	// lastMoved is when the bot last actually changed position, and lastVec is
	// where it was. The comparison is against the vector rather than the
	// rendered coordinate string because that string is whole blocks, and a bot
	// moving inside one block is still moving.
	lastMoved time.Time
	lastVec   mgl32.Vec3

	// pendingAffordance is the verb the model chose this tick, waiting to be
	// consumed by doActivity. It lives on the runner because the answer arrives
	// in consult and is spent in doActivity, and threading a parameter between
	// them would be one more thing to forget.
	pendingAffordance string
	// faultRepeats counts consecutive ticks of the same fault, so the watchdog
	// can insist on seeing something twice before acting on the ambiguous one.
	faultRepeats int
	lastFault    Fault
	// disconnected is the connection state, fed in from the network layer. The
	// watchdog is the only reader, and a connection that is down is the one
	// fault nothing else can be tried against.
	disconnected bool
	// suspendedUntil and suspendedBy are the human-holds-the-bot's-attention
	// window. The episode clock keeps running through it — the recording is
	// still going whether anyone is talking or not — but the bot stops working
	// until the attention lapses.
	suspendedUntil time.Time
	suspendedBy    string
	// idleUntil and idleMode hold the rest period currently in progress. Kept on
	// the runner rather than recomputed, so a duration is decided once instead
	// of sliding forward by a tick every time the clock is read.
	idleUntil time.Time
	idleMode  IdleMode
	// activityLog is a short history of the activities the brain has already
	// chosen. It exists so the loop can notice it is repeating itself: a model
	// that keeps picking the same activity is not deciding, and the caller
	// turns that into a different choice rather than into another lap.
	activityLog []string
}

// activityMemory is how many recent choices the loop check remembers. Three is
// enough to catch an immediate repeat without penalising a natural rhythm of
// "mine, wander, mine".
const activityMemory = 3

// goalLifetime is how long one goal is pursued before it is re-examined. It is
// long enough that a goal can actually be progressed, and short enough that the
// bot re-reads the world rather than grinding a stale intention. This is a
// constant rather than config because it is a property of the loop, not a
// taste knob: any value in the few-minutes range behaves the same way.
const goalLifetime = 4 * time.Minute

// goalConfidenceFloor is the confidence below which a goal CHANGE is ignored.
//
// It applies only to switching, never to keeping the current goal: the bot is
// free to be unsure about what to do next, but it should not be talked out of
// what it is already doing on a whim.
const goalConfidenceFloor = 0.5

// Config is the subset of the AGI settings the runner needs. Declared here
// rather than importing the config package so agi can be tested with a plain
// struct literal, and so the config surface does not leak into the loop.
type Config struct {
	Enabled bool
	// TickInterval is the gap between two brain ticks. It is a range rather
	// than a number because a brain on a fixed ticker idles in visible
	// lockstep, and the idle is what a viewer learns to read as a bot.
	TickInterval     TimeRange
	LLMChance        float64
	SelfPreservation bool
	LowHPThreshold   int
	LowHunger        int
	// LowAirSeconds is how long the head can be under before the breath reflex
	// takes over from whatever the bot was doing.
	LowAirSeconds     int
	Wander            bool
	WanderDurationSec int
	Social            bool
	SocialCooldownSec int
	Vision            bool
	VisionRadius      float32
	VisionHoldSec     int
	VisionCooldownSec int

	// Mode selects the brain: default (reactive), planning (long-horizon) or
	// natural (playing for its own sake). It is normalised on the way in, so a
	// typo lands on default rather than leaving the bot half-configured.
	Mode string

	// Goal is an objective the operator set in words, e.g. "kill the ender
	// dragon". Empty leaves the choice to the model.
	Goal string
	// GoalDeadlineMin is how long that goal is pursued. Zero means no deadline.
	GoalDeadlineMin int

	// IdleStillBias is the share of rest periods that are fully motionless.
	//
	// It is a taste knob, so it is config rather than a constant: the right
	// number for a five-minute clip is not the right number for a three-hour
	// stream, and neither of those should need a recompile.
	IdleStillBias float64

	// PlanLifetimeMin bounds how long one plan is pursued before the planner is
	// consulted again, and PlanReplanMin is how often within that the planner
	// gets a look in. The second is what keeps a slow planner from stalling the
	// brain: actions continue against the current plan while a replan is pending.
	PlanLifetimeMin int
	PlanReplanMin   int
	// PlanMaxSteps bounds a generated plan. An unbounded plan is a wishlist, and
	// a wishlist cannot be finished.
	PlanMaxSteps int

	// Jev is the optional System One model. Nil means the hardcoded thresholds
	// are in charge, which is a supported configuration rather than a degraded
	// one.
	Jev *jev.Client
	// DangerThreshold and SpeakThreshold are where to cut Jev's probabilities.
	// They stay in Go on purpose: the model says how likely it is, the program
	// decides what likely means.
	DangerThreshold float64
	SpeakThreshold  float64
	// EngageThreshold is where Jev's "should I be busy" answer is read as
	// engaged. It sits low by default: a bot that only bothers when it is
	// certain to be busy ends up standing still, and stillness on its own is
	// just as robotic as constant motion.
	EngageThreshold float64

	// Perception bounds what the bot notices. Config rather than constants
	// because these are taste knobs: a mob range that is right in a plain is
	// wrong in a cave.
	MobScanDistance   float32
	BlockScanDistance float32
	BlockScanLimit    int
	MobPromptLimit    int
	NearbyRadius      float32
	BedKeywords       []string

	// NightStartTicks and NightEndTicks bound nightfall in world ticks.
	NightStartTicks int64
	NightEndTicks   int64
	// EyeHeight is where the bot's eyes sit above its feet, in blocks.
	EyeHeight float32
	// WanderRadius is how far one wander step may land.
	WanderRadius float32
}

// TimeRange is a closed range of durations the brain draws its next gap from.
// It is the runner's own view of the configured tick interval, so this package
// still does not depend on internal/config.
type TimeRange struct {
	Min time.Duration
	Max time.Duration
}

// Draw returns one gap. Reversed bounds are swapped rather than collapsed, so
// this setting has one meaning wherever it is interpreted; a range at or below
// the floor falls back to the floor, so a half-built Runner in a test still
// ticks rather than spinning.
func (r TimeRange) Draw() time.Duration {
	const floor = 500 * time.Millisecond
	minD, maxD := r.Min, r.Max
	if maxD < minD {
		minD, maxD = maxD, minD
	}
	if minD < floor {
		minD = floor
	}
	if maxD <= minD {
		return minD
	}
	return minD + time.Duration(rand.Int63n(int64(maxD-minD)))
}

// String renders the range for the log line at startup.
func (r TimeRange) String() string {
	if r.Max <= r.Min {
		return r.Min.String()
	}
	return fmt.Sprintf("%s-%s", r.Min, r.Max)
}

// ConfigFrom adapts the YAML config to the runner's own view of it, so the
// package does not depend on internal/config.
func ConfigFrom(c config.AGIConfig) Config {
	cfg := Config{
		Enabled:           c.Enabled,
		TickInterval:      TimeRange{Min: c.TickInterval.Min, Max: c.TickInterval.Max},
		LLMChance:         c.LLMChance,
		SelfPreservation:  c.SelfPreservation,
		LowHPThreshold:    c.LowHPThreshold,
		LowHunger:         c.LowHunger,
		LowAirSeconds:     c.LowAirSeconds,
		Wander:            c.Wander,
		WanderDurationSec: c.WanderDurationSec,
		Social:            c.Social,
		SocialCooldownSec: c.SocialCooldownSec,
		Vision:            c.Vision,
		VisionRadius:      c.VisionRadius,
		VisionHoldSec:     c.VisionHoldSec,
		VisionCooldownSec: c.VisionCooldownSec,
		DangerThreshold:   c.Jev.DangerThreshold,
		SpeakThreshold:    c.Jev.SpeakThreshold,
		EngageThreshold:   c.Jev.EngageThreshold,

		Mode:            config.NormalizeMode(c.Mode),
		Goal:            strings.TrimSpace(c.Goal),
		GoalDeadlineMin: c.GoalDeadlineMin,
		IdleStillBias:   c.IdleStillBias,
		PlanLifetimeMin: c.PlanLifetimeMin,
		PlanReplanMin:   c.PlanReplanMin,
		PlanMaxSteps:    c.PlanMaxSteps,

		MobScanDistance:   c.Perception.MobScanDistance,
		BlockScanDistance: c.Perception.BlockScanDistance,
		BlockScanLimit:    c.Perception.BlockScanLimit,
		MobPromptLimit:    c.Perception.MobPromptLimit,
		NearbyRadius:      c.Perception.NearbyRadius,
		BedKeywords:       c.Perception.BedKeywords,
		NightStartTicks:   c.NightStartTicks,
		NightEndTicks:     c.NightEndTicks,
		EyeHeight:         c.EyeHeight,
		WanderRadius:      c.WanderRadius,
	}
	if c.Jev.Enabled {
		// Key and model come from the environment; only the endpoint, the
		// timeout and the thresholds are configuration.
		if client := jev.FromEnv(c.Jev.BaseURL, c.Jev.Model); client.Available {
			client.SetTimeout(time.Duration(c.Jev.TimeoutSec) * time.Second)
			cfg.Jev = client
		}
	}
	return cfg
}

// New builds a runner. Returns nil when AGI is off, so callers can start it
// unconditionally and let a nil runner mean "autonomy disabled".
func New(b *bot.Bot, cfg Config) *Runner {
	if !cfg.Enabled {
		return nil
	}
	r := &Runner{
		b:          b,
		cfg:        cfg,
		seed:       rand.Intn(360),
		vocabulary: NewVocabulary(),
		greeted:    make(map[string]time.Time),
	}
	// An operator-set goal is installed before the loop starts, so the very
	// first tick already has one. Waiting for the model to install it would make
	// a configured goal indistinguishable from a typo that never took effect.
	if cfg.Goal != "" {
		r.pinOperatorGoal(time.Now())
	}
	return r
}

// Run drives the loop until the session ends. It is started as a goroutine by
// the session, so its context lifetime is the session lifetime.
func (r *Runner) Run(ctx context.Context) {
	if r == nil || r.b.AiClient == nil {
		return
	}

	r.b.Logger.Info("AGI loop started",
		slog.String("interval", r.cfg.TickInterval.String()),
		slog.String("mode", r.cfg.Mode),
		slog.Float64("llm_chance", r.cfg.LLMChance),
		slog.Bool("social", r.cfg.Social),
		slog.Bool("wander", r.cfg.Wander),
		slog.Bool("vision", r.cfg.Vision),
	)

	// Say plainly, at startup, whether the System One layer is actually wired
	// in. A bot that logs "AGI enabled" and then silently runs on hardcoded
	// thresholds is the exact failure this line exists to make obvious.
	if r.cfg.Jev != nil {
		r.b.Logger.Info("AGI: Jev System One active",
			slog.String("endpoint", r.cfg.Jev.Endpoint()),
			slog.String("model", r.cfg.Jev.ModelName()),
			slog.Float64("danger_threshold", r.cfg.DangerThreshold),
			slog.Float64("speak_threshold", r.cfg.SpeakThreshold),
			slog.Float64("engage_threshold", r.cfg.EngageThreshold),
			// The tick is also the model's request rate, so a short interval is
			// a spend, not just a pace. Logging it here is what makes a
			// two-second brain an informed choice rather than a surprise bill.
			slog.String("one_request_per_tick", r.cfg.TickInterval.String()),
		)
	} else {
		r.b.Logger.Info("AGI: running on local thresholds (Jev not configured)")
	}

	// A timer rather than a ticker: the gap is redrawn every time, so a
	// recurring interval is not what this loop can express. The first wait is
	// drawn from the same range rather than from a fraction of it, so a
	// reconnecting bot does not wake up in lockstep with whatever it was doing
	// before — and, unlike the old rand.Intn(interval), it cannot divide by zero
	// when the configured interval is not a positive number.
	for {
		wait := r.cfg.TickInterval.Draw()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		r.Tick(ctx)
	}
}

// Tick runs one pass of the brain: reflex first, then an optional decision.
func (r *Runner) Tick(ctx context.Context) {
	// Don't decide anything until the bot can see the ground it is standing on.
	//
	// This is a precondition rather than a politeness. Right after a join the
	// terrain is still streaming in, and every perception helper here answers
	// "air" for a cell it has not decoded. A brain fed that sees an empty world:
	// no blocks, no features, nothing to gather or build with, so Jev has nothing
	// to choose from and the tick falls through to idle. A bot that idles
	// forever while its own world loads is indistinguishable from a frozen one.
	//
	// The check is on the bot's own cell, not on how many chunks have arrived. On
	// a LAN join the first sub-chunk to decode is frequently nowhere near the
	// player, so a chunk count can read as satisfied while everything under the
	// bot's feet is still unknown.
	if !r.groundKnown() {
		return
	}
	snap := r.Observe()

	// Ask Jev once, up front, for every judgement this tick needs. One call
	// answers all of them in a single parallel pass, so there is no reason to
	// split them across round trips.
	judgement := r.consult(ctx, snap)

	// The reflex layer runs on every tick and never waits for a model. It is
	// the part that keeps the bot alive between decisions.
	reflex := DecideReflex(snap, r.thresholds())
	if judgement.Danger && r.cfg.SelfPreservation {
		// Jev outranks the thresholds here: it saw the state as a whole,
		// while the threshold only saw a health number.
		reflex = Reflex{Kind: ReflexFlee}
	}
	if reflex.Kind != ReflexNone {
		r.runReflex(ctx, reflex)
		// A reflex consumed this tick. Acting and deliberating in the same
		// breath is how a bot ends up eating while it flees.
		return
	}

	// Planning mode replaces the open-ended half of the loop with plan
	// execution. It sits below the reflex layer and above Jev's activity pick:
	// survival still outranks a plan, because a bot that finishes an objective
	// by dying has not finished anything.
	if r.cfg.Mode == config.ModePlanning {
		r.planningTick(ctx, snap, judgement)
		return
	}

	// Natural mode plays for its own sake. It is checked last of the three
	// because it is the broadest: it is the one that has to cope with having no
	// objective at all.
	if r.ModeIsNatural() {
		r.NaturalTick(ctx, snap, judgement)
		return
	}

	// Jev gets a veto on unprompted speech, and that veto is applied in
	// canSpeakNow rather than here — a second copy of the same rule is a second
	// place for it to be wrong.

	// Jev's activity pick is an instruction, not a hint: when it chose one, run
	// it through the same action layer a player's request uses. Falling through
	// to the LLM here would throw away a decision that already cost a round
	// trip and replace it with a slower, vaguer one.
	if judgement.Known && judgement.Activity != "" {
		// A model that keeps picking the same thing is not deciding. Rather than
		// doing it again, the bot steps aside: this is what stops the visible
		// failure where the bot walks in a small circle forever because every
		// tick it "decides" to wander.
		activity := judgement.Activity
		if r.recentActivity(activity) {
			activity = r.AlternativeActivity(activity)
		}
		r.GoalProgress(activity)
		r.applyLocomotion(judgement.Locomotion)
		r.applyDrop(judgement.DropOK)
		r.b.SetAppetite(judgement.Appetite)
		r.rememberAffordance(judgement.Affordance)
		r.applyGaze(judgement.Gaze)
		r.doActivity(activity)
		return
	}

	if !r.shouldDeliberate(snap, judgement) {
		// Nothing urgent, nothing worth saying, and the model was not consulted.
		// A person standing in a field with nothing to do drifts — that motion
		// is most of what reads as "playing" rather than "idling".
		r.maybeWander()
		return
	}
	r.deliberate(ctx, snap, judgement)
}

// Curriculum returns the activities worth offering for this snapshot.
//
// This is the "propose tasks suited to the current world" half of Voyager's
// curriculum idea, done cheaply. The point is not to be clever, it is to stop
// offering the model things that cannot work: sleep without a bed, shelter at
// midday, approach with nobody in sight. A choice question built from a menu of
// impossible options produces confident nonsense, and confident nonsense is what
// makes an agent look broken rather than busy.
// Curriculum returns the activities worth offering for this snapshot.
//
// This is the unfiltered menu: everything plausible in the current world. The
// active goal narrows it — see (*Runner).Menu, which is what the brain actually
// asks the model about. Keeping this pure and runner-free is what lets it be
// tested against snapshots directly.
// logsToStopOfferingGather is how many logs the bot may carry before "gather"
// leaves the menu.
//
// It is a floor rather than a goal because the goal is not known here: the menu
// is built before anything has said what number it wants. A player with thirty
// logs is not looking for wood, and neither is a bot — offering it the choice
// only produces an action that reports success without changing anything.
const logsToStopOfferingGather = 16

func Curriculum(s Snapshot) []string {
	curriculum := make([]string, 0, 8)
	// Rest leads the list and is always present. It is the answer that makes
	// this an agent rather than a loop, so it must never be a fallback that
	// only appears when the world looks empty.
	curriculum = append(curriculum, jev.ActivityRest)
	curriculum = append(curriculum, jev.ActivityWander)

	// Night removes the daylight activities and adds the night ones. A bot that
	// offers to "explore a little" at midnight is offering to get killed.
	if s.IsNight {
		if s.HasBed {
			curriculum = append(curriculum, jev.ActivitySleep)
		} else {
			curriculum = append(curriculum, jev.ActivityShelter)
		}
		return curriculum
	}

	curriculum = append(curriculum, jev.ActivityExplore)

	// Only offer to collect when there is something to collect. An empty
	// "gather" is a task the bot cannot finish, and finishing nothing reads as
	// a bug from the outside.
	//
	// The test is for real block names, not for the string merely being
	// non-empty. A rendered sentence is non-empty too, and a bot offered
	// "gather wood" in a world it can see no wood in walks off and fails.
	//
	// Gathering is not offered when the bot is already carrying a pile of it.
	// The offer was free before and it cost a loop: the gatherer correctly
	// declined, the action reported itself satisfied, and the model chose it
	// again on the next tick because the reason it was still on the menu was
	// "there are logs in the world", not "the bot needs logs". Thirty-nine
	// consecutive skipped gathers is what that looks like.
	if len(blockNames(s.NearBlocks)) > 0 && s.LogsHeld < logsToStopOfferingGather {
		curriculum = append(curriculum, jev.ActivityGather)
		// Mining is only offered when there is a resource AND the bot is not
		// already carrying too much. Offering "go mine" with a full inventory
		// produces a bot that swings at a tree and then has nowhere to put the
		// wood, which is a worse failure than never offering it.
		if s.InventoryFree() {
			curriculum = append(curriculum, jev.ActivityMine)
		}
	}
	// Only offer to walk over to someone who is actually visible.
	if _, ok := nearestVisible(s); ok {
		curriculum = append(curriculum, jev.ActivityApproach)
		curriculum = append(curriculum, jev.ActivityChat)
	}
	// Gesturing is always available. Emoting at nothing in particular is one of
	// the most human things a player does while waiting, and it costs nothing.
	// It is offered in daylight only: a bot that emotes around at 3am reads as
	// scripted, not as spontaneous.
	curriculum = append(curriculum, jev.ActivityGesture)
	// Reading the surroundings is offered whenever there is something to read.
	curriculum = append(curriculum, jev.ActivityLook)

	// The rest are gated on the world actually offering them. This is the whole
	// point of the Features scan: a menu built from wishful thinking produces a
	// bot that repeatedly picks an action that cannot work, and a player can see
	// that it cannot work.
	if s.Features.Water {
		curriculum = append(curriculum, jev.ActivityFish)
	}
	if s.Features.RipeCrops > 0 {
		curriculum = append(curriculum, jev.ActivityHarvest)
	}
	if s.Features.Animals > 0 {
		curriculum = append(curriculum, jev.ActivityTendAnimals)
	}
	if s.Craftable > 0 {
		curriculum = append(curriculum, jev.ActivityCraft)
	}
	return curriculum
}

// nearestVisible reports whether anyone is in sight, not merely nearby. "Walk
// over to a player" needs eyes on them.
func nearestVisible(s Snapshot) (string, bool) {
	for _, p := range s.Nearby {
		if p.HasLineOf {
			return p.Name, true
		}
	}
	return "", false
}

// pendingVerb reads the affordance answer and clears it, reporting whether it
// names a registry action the bot can actually perform.
//
// A verb that is not in the catalogue, or that is gated out for a world-changing
// intent, is dropped rather than executed. The gate has already refused to offer
// those, so an answer naming one means the model invented it or answered a
// different question — and executing a label the world does not allow is exactly
// the unverified action the affordance layer exists to prevent.
func (r *Runner) pendingVerb() (string, bool) {
	r.mu.Lock()
	picked := r.pendingAffordance
	r.pendingAffordance = ""
	r.mu.Unlock()

	if picked == "" {
		return "", false
	}
	v, ok := affordance.Lookup(picked)
	if !ok || !v.Offerable() {
		return "", false
	}
	// An Activity is carried out by the brain, not dispatched as a label; the
	// activity switch above is what handles those.
	if v.Kind != affordance.Action {
		return "", false
	}
	return v.Label, true
}

// execute dispatches a verb to the action registry.
//
// It is a variable so a test can observe the dispatch itself rather than
// re-deriving what the call site would have passed. A test that calls
// takeAffordanceParam and asserts on the result proves the helper works, which
// is not the same as proving the handler is given the argument.
var execute = action.Execute

// takeAffordanceParam extracts the argument a picked verb carries, so the
// handler receives what the model was shown.
func takeAffordanceParam(label string) string {
	_, param := affordance.SplitVerb(label)
	return param
}

// doActivity carries out the activity Jev chose.
func (r *Runner) doActivity(activity string) {
	who := r.audience()
	r.recordActivity(activity)

	// The verb the model chose out of the affordance set runs first, when it is
	// a real registry action.
	//
	// It goes first because it is the more specific answer. The activity menu
	// says "gather"; the affordance set says which of the things it could do
	// right now actually applies, and the model was asked that separately for a
	// reason. Ignoring it and running the activity would make the second
	// question decorative, and a decorative question is worse than no question:
	// the model picks it every tick, nothing happens, and the bot stands still.
	if verb, ok := r.pendingVerb(); ok {
		r.b.Logger.Info("AGI: acting on an affordance",
			slog.String("verb", verb),
			slog.String("activity", activity))
		execute(r.b, verb, takeAffordanceParam(verb), who)
		// The activity still runs. The verb is the what; the activity is the
		// why, and a bot that did both is not contradicting itself — it is
		// doing the thing the model picked with the thing it picked it for.
	}

	switch activity {
	case jev.ActivityRest:
		// Resting is a real behaviour, not a log line: the bot settles where it
		// is and lets the idle gaze take over. That stillness is the point —
		// an agent that always finds something to do is more obviously a bot
		// than one that sometimes just stands there.
		r.b.Logger.Debug("AGI: jev chose to rest", slog.String("who", who))
	case jev.ActivityWander:
		r.Wander()
	case jev.ActivityExplore:
		action.Execute(r.b, "explore", "25", who)
	case jev.ActivityGather:
		action.Execute(r.b, "gather", "", who)
	case jev.ActivityMine:
		// Mining is delegated to the gather path with a longer window, because
		// that is the action that actually walks to a resource, swings at it and
		// collects the drops.
		action.Execute(r.b, "automine", "wood", who)
	case jev.ActivityApproach:
		// Walk to the player and stop there. Approaching is not the same as
		// talking, and conflating them is what produced a bot that stood next to
		// someone in silence.
		r.approachNearest(who)
	case jev.ActivityChat:
		r.approachNearest(who)
	case jev.ActivityShelter:
		action.Execute(r.b, "shelter", "", who)
	case jev.ActivitySleep:
		action.Execute(r.b, "sleep", "", who)
	case jev.ActivityGesture:
		r.gesture()
	case jev.ActivityLook:
		action.Execute(r.b, "readsign", "", who)
	case jev.ActivityFish:
		// Only reachable when the curriculum saw water; the handler still walks
		// to it and reports honestly if the water is not actually castable.
		action.Execute(r.b, "fish", "", who)
	case jev.ActivityHarvest:
		action.Execute(r.b, "harvest", "", who)
	case jev.ActivityTendAnimals:
		action.Execute(r.b, "feed", "", who)
	case jev.ActivityCraft:
		action.Execute(r.b, "craft", "", who)
	}
}

// CurrentGoal returns the active goal, or the zero Goal when there is none.
func (r *Runner) CurrentGoal() Goal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.goal
}

// Menu is the activity list the model is asked about: the full curriculum,
// narrowed by the active goal.
//
// A goal that does not constrain the menu is a goal in name only — the bot
// would keep making independent choices and merely describe them as progress.
func (r *Runner) Menu(s Snapshot) []string {
	full := Curriculum(s)
	goal := r.CurrentGoal()
	if goal.Name == "" {
		return full
	}
	return NarrowToGoal(full, goal)
}

// AdoptGoal installs a goal and gives it a lifetime. An empty name clears it.
func (r *Runner) AdoptGoal(name string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		r.goal = Goal{}
		return
	}
	entry, ok := GoalCatalogue[name]
	if !ok {
		return
	}
	// Re-adopting the goal the bot is already pursuing refreshes its deadline
	// without resetting progress, so continuing something does not look like
	// starting over.
	if r.goal.Name == name {
		r.goal.Deadline = now.Add(goalLifetime)
		return
	}
	r.goal = NewGoal(entry, now, goalLifetime)
}

// ConsiderGoal decides whether to adopt the model's goal choice, and reports
// whether the goal actually changed.
//
// Switching is the part that needs guarding. A model that is unsure about a new
// intention must not be allowed to talk the bot out of what it is already
// doing, because oscillating between two goals every tick looks busy while
// accomplishing nothing. Keeping the current goal is the human behaviour: people
// change their mind on evidence, not because a coin flipped. With no current
// goal there is nothing to defend, so even a weak answer establishes one --
// otherwise the bot could never start.
func (r *Runner) ConsiderGoal(choice string, confidence float64, now time.Time) bool {
	current := r.CurrentGoal()
	// An operator-set goal outranks the model's opinion. The whole point of
	// writing one down is that the bot keeps working on it while the model has
	// better ideas, and a model given a free hand here will have better ideas
	// every tick.
	if current.Pinned {
		return false
	}
	switching := current.Name != "" && current.Name != choice
	if switching && confidence < goalConfidenceFloor {
		return false
	}
	r.AdoptGoal(choice, now)
	return true
}

// GoalProgress records that an activity advanced the active goal.
func (r *Runner) GoalProgress(activity string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.goal.Name != "" && r.goal.AdvancesActivity(activity) {
		r.goal.Progress++
	}
}

// maybeWander starts an idle drift, but only when nothing else wants the bot.
// Wandering while busy would fight whatever it is already doing.
func (r *Runner) maybeWander() {
	if r.b.Explorer != nil && r.b.Explorer.IsExploring() {
		return
	}
	r.Wander()
}

// approachNearest walks over to the closest visible player and stops there.
//
// This replaces the old "approach is really wander" behaviour, which sent the
// bot off in a random direction when the model asked it to approach someone.
// The result was a bot that looked like it was ignoring the person it had just
// decided to go see.
func (r *Runner) approachNearest(who string) {
	name, ok := r.nearestVisiblePerson()
	if !ok {
		// Nobody in sight: wandering is the honest fallback, and pretending
		// otherwise would log a success for an approach that never happened.
		r.Wander()
		return
	}
	action.Execute(r.b, "come", name, who)
}

func (r *Runner) nearestVisiblePerson() (string, bool) {
	for _, p := range r.nearbyPeople(r.b.GetCoords(), r.b.LookTargetName) {
		if p.HasLineOf {
			return p.Name, true
		}
	}
	return "", false
}

// gesture plays a short emote.
//
// Emoting for no reason is one of the most human things a player does while
// waiting, and it is the cheapest way for a bot to read as "idle but present"
// rather than as a frozen client. The choice is random because picking the
// same one every time is its own tell.
func (r *Runner) gesture() {
	emotes := []string{"nod", "shake", "lookaround", "jump"}
	emote := emotes[rand.Intn(len(emotes))]
	action.Execute(r.b, "emote", emote, r.audience())
}

// recentActivity reports whether the bot just did this, which is what keeps the
// brain from looping. Without it a model that keeps choosing "wander" sends the
// bot walking in a circle forever, which is the single most obvious sign that
// the "decision" is not a decision at all.
func (r *Runner) recentActivity(activity string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, prev := range r.activityLog {
		if prev == activity {
			return true
		}
	}
	return false
}

// recordActivity pushes a choice onto the short history the loop check reads.
func (r *Runner) recordActivity(activity string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activityLog = append(r.activityLog, activity)
	if len(r.activityLog) > activityMemory {
		r.activityLog = r.activityLog[len(r.activityLog)-activityMemory:]
	}
}

// AlternativeActivity picks a substitute when the model repeats itself. It
// steps down to rest rather than sideways into another action: a bot that
// alternates between two activities on a fixed beat is as mechanical as one
// that repeats a single action, and resting is the honest answer to "I have
// nothing better to do".
//
// The one exception is an activity that advances the active goal. Repeating
// "mine" while working towards getting wood is not a loop, it is the whole
// point; breaking out of it would make the goal impossible to finish.
func (r *Runner) AlternativeActivity(activity string) string {
	if r.CurrentGoal().AdvancesActivity(activity) {
		return activity
	}
	if activity == jev.ActivityRest {
		return jev.ActivityExplore
	}
	return jev.ActivityRest
}

// Judgement is what the System One layer concluded about one tick.
type Judgement struct {
	// Danger and WorthSpeak come from Jev. When Jev is absent they are false
	// and Known is false, which is how callers tell "no" from "not asked".
	Danger     bool
	WorthSpeak bool
	// Activity is Jev's pick for what to do, empty when Jev is absent.
	Activity string
	// Escalate is Jev's read on whether this is a moment the big model is worth
	// calling. It is a probability, like Danger, and the program decides what
	// likely means — Jev never gets to spend the expensive tier on its own
	// judgement about whether the expensive tier is needed.
	Escalate float64
	// Engaged is Jev's read on whether the bot should be visibly busy. A calm
	// answer here is what lets the bot choose to stand still, which is the
	// behaviour that separates a companion from a task runner.
	Engaged bool
	// The raw scores are kept alongside the thresholded booleans. Logging the
	// model's own number rather than only the verdict is what makes a decision
	// auditable after the fact — a bot that acted on a 0.51 and a bot that acted
	// on a 0.99 look identical in the logs otherwise.
	DangerScore float64
	SpeakScore  float64
	// ActivityConfidence is Jev's confidence in the activity it chose, straight
	// from the choice answer.
	ActivityConfidence float64
	// Goal is the multi-tick objective Jev picked. It is installed on the
	// runner rather than merely returned, because a goal that did not persist
	// past the tick that chose it would be a suggestion, not a goal.
	Goal string
	// Locomotion is how Jev wants the body to travel while it does this tick's
	// activity: walk, sprint, or sprint-jump. Auto when the model was not
	// asked (busy/exploring ticks) or did not answer — never a zero value
	// that means something else, so "no opinion" and "walk" stay distinct.
	Locomotion LocomotionHint
	// Appetite is how much risk the bot should take on purpose. It is asked on
	// every tick and it is a disposition rather than a setting: it is the reason
	// the same creeper at four blocks produces a different answer on different
	// ticks, which is the whole difference between a bot and a lookup table.
	Appetite affordance.Appetite
	// Affordance is the verb the model picked out of the set the world allowed.
	// It is consumed by doActivity, which is why the field exists at all: an
	// offered choice that is never read is worse than not offering it, because
	// the model will pick it every tick and nothing will happen.
	Affordance string
	// DropOK is Jev's answer to "leap this ledge, or stop at the edge". It is
	// only asked when there is a drop, and it is deliberately separate from the
	// gait: "sprint" is how fast to travel, "drop" is whether to leave the ground
	// at all. Default false means "stop", which is the safe reading of a
	// question that was not asked.
	DropOK bool
	// Gaze is what Jev wants the head to settle on while the bot is standing
	// still: a player, a creature, a block, or nothing in particular. Auto when
	// the model was not asked or did not answer.
	//
	// Separate from Locomotion because it answers the opposite question at the
	// opposite moment: locomotion is about a body that is going somewhere, gaze
	// is about a body that has stopped. A single "style" field would have had to
	// mean both, and every value in it would have been wrong half the time.
	Gaze GazeHint
	// Known is true when a Jev answer actually arrived. A missing answer must
	// never be read as a confident "no" — that would silently turn the brain
	// off whenever the API hiccups.
	Known bool
}

// consult asks Jev what it thinks, and degrades quietly when it is not there.
// Every failure path returns a zero Judgement with Known=false, which the
// caller treats as "carry on with the hardcoded rules".
func (r *Runner) consult(ctx context.Context, snap Snapshot) Judgement {
	// An expired goal is cleared before the model is asked what to do, so the
	// goal question sees the real state and the bot is not told it is midway
	// through something it abandoned an hour ago.
	if goal := r.CurrentGoal(); goal.Name != "" && goal.Expired(snap.Now) {
		r.b.Logger.Info("AGI: goal expired",
			slog.String("goal", goal.Name),
			slog.Int("progress", goal.Progress),
		)
		r.AdoptGoal("", snap.Now)
	}

	if r.cfg.Jev == nil {
		return Judgement{}
	}
	state := DescribeState(snap)

	resp, err := r.cfg.Jev.Evaluate(ctx, state, r.questions(snap))
	if err != nil {
		// WARN, not DEBUG. This path means the configured System One model is
		// not being consulted at all, and the bot silently carries on with its
		// hardcoded thresholds — which looks identical to "Jev is working" from
		// the outside. A 404 from a wrong endpoint, a dead key, or a network
		// hiccup are all completely invisible at DEBUG, which is how an enabled
		// but non-functional feature can sit in a config for weeks.
		r.b.Logger.Warn("AGI: jev unavailable, using local rules", slog.String("error", err.Error()))
		return Judgement{}
	}

	j := Judgement{Known: true}
	if p, ok := resp.Noul(jev.QDanger); ok {
		j.Danger = p >= r.cfg.DangerThreshold
		j.DangerScore = p
	}
	if p, ok := resp.Noul(jev.QWorthSay); ok {
		j.WorthSpeak = p >= r.cfg.SpeakThreshold
		j.SpeakScore = p
	}
	if p, ok := resp.Noul(jev.QEngagement); ok {
		j.Engaged = p >= r.cfg.EngageThreshold
	}
	if choice, confidence, ok := resp.Choice(jev.QActivity); ok {
		j.Activity = choice
		j.ActivityConfidence = confidence
	}
	if choice, _, ok := resp.Choice(jev.QLocomotion); ok {
		j.Locomotion = parseLocomotion(choice)
	}
	if choice, _, ok := resp.Choice(jev.QDrop); ok {
		// Absent means "stop". The body refuses a cliff on its own, so the only
		// thing this can add is the deliberate leap; an unasked question must
		// never be read as permission.
		j.DropOK = strings.EqualFold(strings.TrimSpace(choice), jev.DropLeap)
	}
	if choice, _, ok := resp.Choice(jev.QRisk); ok {
		j.Appetite = affordance.ParseAppetite(choice)
	}
	if choice, _, ok := resp.Choice(jev.QAffordance); ok {
		j.Affordance = choice
	}
	if choice, _, ok := resp.Choice(jev.QGaze); ok {
		j.Gaze = parseGaze(choice)
	}
	if p, ok := resp.Noul(EscalateQuestion); ok {
		j.Escalate = p
	}
	if choice, confidence, ok := resp.Choice(jev.QGoal); ok {
		if r.ConsiderGoal(choice, confidence, snap.Now) {
			j.Goal = choice
		}
	}
	// One INFO line per consultation. It is the only way to tell a live System
	// One loop from a fallback: the numbers are the model speaking, and seeing
	// them is what makes a "the bot is thinking" claim checkable.
	r.b.Logger.Info("AGI: jev decided",
		slog.String("model", resp.Model),
		slog.Float64("danger", j.DangerScore),
		slog.Float64("speak", j.SpeakScore),
		slog.Bool("engaged", j.Engaged),
		slog.String("activity", j.Activity),
		slog.Float64("confidence", j.ActivityConfidence),
		slog.String("locomotion", string(j.Locomotion)),
		slog.String("gaze", string(j.Gaze)),
		slog.Int("input_tokens", resp.Usage.InputTokens),
	)
	return j
}

// questions assembles every question this tick needs. The reflex questions are
// always asked; the activity question is only added when there is a real choice
// to put to the model, because asking "what now?" with one option is a wasted
// round trip that can only ever answer "that one".
func (r *Runner) questions(s Snapshot) map[string]json.RawMessage {
	questions := jev.BuildReflexQuestions()
	// The escalation question is asked every tick, but it is cheap: it rides
	// along in the same parallel pass as everything else rather than being a
	// second round trip. Jev is the only thing allowed to ask for the big
	// model, so its opinion has to be in the batch or the gate never opens.
	questions[EscalateQuestion] = jev.MustNoul(EscalateInstructions)

	// The disposition is asked on every tick, INCLUDING a busy one, and it is the
	// one question that sits above the early return below.
	//
	// That return is right for everything after it: a bot that is already
	// working does not need asking what to work on. It is wrong for the
	// disposition, because a disposition is exactly what changes mid-task. A bot
	// gathering wood when a creeper walks up is not a bot that should be asked
	// what to do next; it is a bot whose idea of what is reasonable has just
	// changed, and a dial it can only move while idle cannot express that.
	//
	// It rides in the same parallel batch, so the honest answer to "does this
	// cost a round trip" is no.
	for name, raw := range jev.BuildRiskQuestion() {
		questions[name] = raw
	}

	// Everything below here is skipped while the body is committed. A busy bot is
	// not asked what to do next, where to go, or what to look at: it is already
	// doing one of those things and changing its mind mid-action is how work gets
	// abandoned half-finished.
	if s.Busy || s.Exploring {
		return questions
	}

	// Goal first, then activity. The goal is a separate decision with a
	// different horizon, and asking for both in one call is exactly the parallel
	// fan-out this client is built for.
	goals := AvailableGoals(s)
	if len(goals) > 1 {
		descriptions := make(map[string]string, len(goals))
		for _, g := range goals {
			descriptions[g.Name] = g.Description
		}
		current := r.CurrentGoal()
		currentName := ""
		if current.Name != "" {
			currentName = current.Name
		}
		for name, raw := range jev.BuildGoalQuestion(GoalsFor(goals), descriptions, currentName) {
			questions[name] = raw
		}
	}

	// The activity menu is narrowed by the goal, so the model is choosing among
	// things that actually move it forward rather than among everything.
	for name, raw := range jev.BuildActivityQuestion(r.Menu(s)) {
		questions[name] = raw
	}

	// And underneath that, the verbs themselves, derived from where the body
	// actually is.
	//
	// This is the question the whole affordance layer exists to improve. The
	// activity menu is a programmer's list of intentions; this is the world's
	// list of possibilities. It is offered separately rather than merged because
	// the two answer different questions — "what should this tick be for" and
	// "what could the body even do" — and collapsing them into one menu is how
	// a model ends up choosing an intention whose verb does not work here.
	if available := r.affordances(s); len(available) > 0 {
		for name, raw := range jev.BuildAffordanceQuestion(available) {
			questions[name] = raw
		}
	}

	// Travel style is only worth asking about when the activity it seasons
	// covers ground. Asking a resting bot how to move spends a decision on
	// noise; asking a wandering one lets Jev pick the gait for the trip.
	if s.Busy || s.Exploring {
		return questions
	}
	menu := r.Menu(s)
	for _, a := range menu {
		if a == jev.ActivityWander || a == jev.ActivityExplore || a == jev.ActivityApproach || a == jev.ActivityChat {
			for name, raw := range jev.BuildLocomotionQuestion() {
				questions[name] = raw
			}
			break
		}
	}
	// Attention, for the same reasoning as gait: asking a bot that is about to
	// sprint across a field what it feels like looking at spends a decision on
	// noise. It rides in the same parallel batch, so it costs no extra round trip
	// and the answer is simply unused on the ticks the body is travelling.
	if anyGazeApplies(r.Menu(s)) {
		for name, raw := range jev.BuildGazeQuestion() {
			questions[name] = raw
		}
	}

	// The drop question rides in the same batch and costs no extra round trip.
	// It is asked only when there is actually a cliff, because "there is no drop
	// in front of you, so should the bot leap?" is a question whose only answer
	// is "no" and it is asked every tick.
	if s.LedgeAhead {
		for name, raw := range jev.BuildDropQuestion() {
			questions[name] = raw
		}
	}

	return questions
}

// anyGazeApplies reports whether the menu holds an activity where the head is
// free, and so where the answer could be used.
// affordances renders what the body could actually do from where it is standing.
//
// The intent class is the one the snapshot implies rather than one the caller
// passes: a bot with a goal in hand is doing something to the world, and a bot
// idling is not. Offering an idle bot only world-changing verbs would make it
// busy, and offering a busy bot only self-changing ones would make it inert.
func (r *Runner) affordances(s Snapshot) map[string]string {
	if r == nil || r.b == nil {
		return nil
	}
	changesWorld := s.GoalSummary != "" && s.GoalSummary != "no goal"
	return r.b.DeriveAffordances(changesWorld).Criteria()
}

func anyGazeApplies(menu []string) bool {
	for _, a := range menu {
		if gazeAppliesTo(a) {
			return true
		}
	}
	return false
}

// DescribeState renders the snapshot as the flat text Jev reasons over. It is
// deliberately a short, factual description: Jev decides, so it needs the
// situation, not a personality and not instructions about how to talk.
func DescribeState(s Snapshot) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Health %d/20. Hunger %d/20. Position %s.\n", s.HP, s.Hunger, s.Coords)
	fmt.Fprintf(&sb, "Time: %s. Free inventory slots: %d.\n", timeOfDay(s), s.FreeSlots)
	if s.IsThundering {
		sb.WriteString("Weather: thunderstorm.\n")
	} else if s.IsRaining {
		sb.WriteString("Weather: raining.\n")
	}
	fmt.Fprintf(&sb, "Current goal: %s.\n", s.GoalSummary)
	// The vocabulary goes in right after the goal, because the two are read
	// together: the goal says what the bot is trying to do and this says what it
	// could possibly do it with. A model reasoning about "build a house" is
	// useless unless it also knows there is no wood.
	fmt.Fprintf(&sb, "This world: %s.\n", s.Vocabulary.Describe())
	if s.PlanSummary != "" && s.PlanSummary != "no plan" {
		fmt.Fprintf(&sb, "Active plan:\n%s", s.PlanSummary)
	}
	fmt.Fprintf(&sb, "Holding: %s.\nInventory: %s.\n", s.HeldItem, s.Inventory)
	if s.EpisodeText != "" {
		fmt.Fprintf(&sb, "Episode: %s\n", s.EpisodeText)
	}
	if s.Conversation != "" {
		fmt.Fprintf(&sb, "Recent player conversation (dialogue, not world observations):\n%s\n", s.Conversation)
	}
	fmt.Fprintf(&sb, "Visible mobs: %s.\n", s.VisibleMob)
	// The prose rendering, not the name list: a model can act on "chest (4m N)"
	// and cannot act on a bare "chest", and the name list is there for the rules
	// in this package rather than for the model.
	fmt.Fprintf(&sb, "Nearby blocks: %s.\n", s.nearBlocksForPrompt())
	fmt.Fprintf(&sb, "Visible signs: %s.\n", describeSigns(s))
	fmt.Fprintf(&sb, "Players nearby: %s.\n", DescribePeople(s.Nearby))
	if s.LedgeAhead {
		sb.WriteString("There is a drop ahead. The bot will stop at the edge unless you ask it to leap.\n")
	}
	if s.Busy {
		sb.WriteString("The bot is already doing something.\n")
	}
	return sb.String()
}

// blockNames returns the distinct block names in a summary, which is the only
// question the curriculum and the goal filter actually mean to ask.
//
// "Is there anything to gather?" is not "is this string non-empty?". The
// rendered sentence is non-empty in every world, including an empty plain, and
// asking the second question is what put "gather wood" on the menu of a bot
// standing in a field with no tree in it.
//
// The "none" sentinel is dropped here rather than at each call site, because it
// is a claim about the absence of blocks rather than the name of one. Leaving it
// in means a bot standing on nothing is offered mining, which is the same
// failure wearing a different hat.
func blockNames(nearBlocks string) []string {
	names, _ := ReadableBlockNames(nearBlocks)
	return names
}

// ReadableBlockNames returns the distinct block names in a summary, and whether
// the summary was a list of names in the first place.
//
// The second return value is the important one. "The scan found no blocks" and
// "I could not read the scan" both leave the caller holding an empty list, and
// a caller that cannot tell them apart will read every unreadable summary as an
// empty world — which for the single-block detector means a conclusion it has no
// evidence for, confirmed on the next tick into a state the bot never leaves.
func ReadableBlockNames(nearBlocks string) (names []string, readable bool) {
	text := strings.TrimSpace(nearBlocks)
	if text == "" {
		// Nothing was scanned. That is a real absence, not a parsing failure.
		return nil, true
	}

	terms := SplitList(text)
	if len(terms) == 0 {
		// There was content, and none of it was a bare name. The summary is in a
		// shape this code does not understand.
		return nil, false
	}

	distinct := make([]string, 0, len(terms))
	seen := make(map[string]bool, len(terms))
	for _, term := range terms {
		name := normaliseTerm(term)
		if name == "" || name == "none" || seen[name] {
			continue
		}
		seen[name] = true
		distinct = append(distinct, name)
	}
	return distinct, true
}

// nearBlocksForPrompt returns the readable rendering of the block scan,
// falling back to the name list for a snapshot built without one.
//
// The fallback is what keeps hand-built snapshots in the tests meaningful, and
// it is not merely a test convenience: any path that assembles a Snapshot
// without scanning still has to put something truthful in the prompt, and a
// bare list of names is truthful where an empty string would be a lie.
func (s Snapshot) nearBlocksForPrompt() string {
	if s.NearBlocksText != "" {
		return s.NearBlocksText
	}
	return s.NearBlocks
}

// timeOfDay names the world clock in words. Jev decides from this text, and a
// bare tick count means nothing to it — "it is getting dark" is a fact a model
// can weigh, "18000" is not.
func timeOfDay(s Snapshot) string {
	switch {
	case s.IsNight:
		return "night"
	case s.Nearby == nil && s.HP >= 16:
		return "day"
	default:
		return "daytime"
	}
}

// describeSigns lists the signage the bot can read right now.
//
// Signage is the difference between a bot that knows where things are and one
// that has to search blindly, so it belongs in the state Jev reasons over. The
// sign text is already cleaned of colour codes by the storage layer.
func describeSigns(s Snapshot) string {
	if len(s.VisibleSigns) == 0 {
		return "none"
	}
	if len(s.VisibleSigns) > 3 {
		s.VisibleSigns = s.VisibleSigns[:3]
	}
	return strings.Join(s.VisibleSigns, "; ")
}

// groundKnown reports whether the world can describe the block under the bot's
// feet. It is the brain's precondition for having a world at all.
//
// The world's own model is asked first, because it is the layer the perception
// scans actually read. A bot whose model cannot resolve its footing has an
// all-air view of the world, and every decision taken from that is noise.
func (r *Runner) groundKnown() bool {
	if r == nil || r.b == nil {
		return false
	}
	if model := r.b.WorldModel; model != nil {
		pos := r.b.GetCoords()
		return model.CanResolve(
			int32(math.Floor(float64(pos.X()))),
			int32(math.Floor(float64(pos.Y())))-1,
			int32(math.Floor(float64(pos.Z()))),
		)
	}
	// No model to ask, so fall back to whether any terrain has been decoded.
	return r.b.WorldCache != nil && r.b.WorldCache.ChunkCount() > 0
}

func (r *Runner) thresholds() Thresholds {
	return Thresholds{
		LowHP:         r.cfg.LowHPThreshold,
		LowHunger:     r.cfg.LowHunger,
		LowAirSeconds: r.cfg.LowAirSeconds,
	}
}

// agiIsNight is the package's own night check, aliased so the runner reads
// without qualifying a name that would shadow the agi package qualifier.
func agiIsNight(ticks, start, end int64) bool { return IsNightTime(ticks, start, end) }

// wantsToMove reports whether the body has an outstanding reason to be in
// motion: it is travelling somewhere or has a path left to walk.
//
// It is read off the body rather than off the decision the model happened to
// make this tick. "rest" is only rest if the body is not also part-way along a
// path it has not finished, and a decision to walk is not a reason to move once
// the path is done — the path and the movement state are what the movement loop
// is actually obeying.
//
// A known travelling state only. An unknown or empty state is not intent: when
// in doubt the watchdog stays quiet, because ActUnstick clears the world model
// and a watchdog that damages a healthy bot is one nobody leaves switched on.
func wantsToMove(b *bot.Bot) bool {
	if b == nil {
		return false
	}
	b.Mu.Lock()
	state, onPath := b.MovementState, len(b.CurrentPath) > 0
	b.Mu.Unlock()
	return onPath || state == "walk_to" || state == "follow"
}

// Observe builds an immutable reading of the world for one decision.
func (r *Runner) Observe() Snapshot {
	b := r.b
	now := time.Now()
	hp, hunger, coords := b.GetStatusDetails()

	b.Mu.Lock()
	// IsBusy() is a Bot method that takes b.Mu itself, so it must NOT be
	// called from inside this critical section. sync.Mutex is not reentrant:
	// the second Lock waits for the first to be released by the very goroutine
	// that is now blocked waiting for it.
	//
	// That is not a theoretical hazard. It presents as a bot that connects,
	// answers chat, and then never takes a single AGI tick — the loop starts,
	// calls Observe, and wedges silently on the first tick with nothing in the
	// log to say why. The whole autonomy layer looked "enabled but inert" and
	// the only visible symptom was a bot standing still.
	pos := b.Pos
	lookTarget := ""
	if b.LookTargetName != "" && time.Now().Before(b.LookTargetUntil) {
		lookTarget = b.LookTargetName
	}
	actors := make(map[uint64]*entity.Info, len(b.Actors))
	for id, info := range b.Actors {
		if info == nil {
			continue
		}
		copied := *info
		actors[id] = &copied
	}
	b.Mu.Unlock()

	// IsBusy is asked for here, outside b.Mu, rather than recomputed inline.
	//
	// It used to be recomputed inline, and that is precisely the copy this method
	// exists to prevent: IsBusy carries the rule that a body with work in
	// progress is spoken for, and an inline copy of the motion-only half of that
	// rule reported a chopping bot — standing still, arm swinging — as free. The
	// natural loop then correctly saw a free body and started an exploration.
	// It has to be called after the unlock above, because IsBusy takes b.Mu
	// itself and sync.Mutex is not reentrant.
	busy := b.IsBusy()

	// Grounded perception, the same line-of-sight view the chat prompt uses.
	// Jev is deciding from this text, so anything it is told has to be true.
	visibleMobs := "none"
	if len(actors) > 0 {
		seen := entity.VisibleMobs(b.WorldModel, sightReader{b}, pos, actors, r.cfg.MobScanDistance, nil)
		names := make([]string, 0, len(seen))
		for _, info := range seen {
			names = append(names, entity.NormalizeName(info.Type))
			if len(names) >= r.cfg.MobPromptLimit {
				break
			}
		}
		if len(names) > 0 {
			visibleMobs = strings.Join(names, ", ")
		}
	}

	underwater, secondsUnder := r.observeSubmersion(now)

	// One scan, two shapes: the names the brain's rules read and the prose the
	// models read. Scanning twice would double a per-tick cost to produce two
	// descriptions that could disagree about the same instant.
	nearBlocks, nearBlocksText := perception.VisibleBlocks(b, r.cfg.BlockScanDistance, r.cfg.BlockScanLimit)

	snap := Snapshot{
		Now:            now,
		Coords:         coords,
		HP:             hp,
		Hunger:         hunger,
		HeldItem:       b.GetHeldItem(),
		Inventory:      b.GetInventorySummary(),
		Conversation:   r.ConversationContext(),
		VisibleMob:     visibleMobs,
		NearBlocks:     nearBlocks,
		NearBlocksText: nearBlocksText,
		Busy:           busy,
		Exploring:      b.Explorer != nil && b.Explorer.IsExploring(),
		WantsToMove:    wantsToMove(b),
		IsNight:        r.isNight(),
		HasBed:         r.hasBed(),
		FreeSlots:      r.freeInventorySlots(),
		Nearby:         r.nearbyPeople(pos, lookTarget),
		VisibleSigns:   r.visibleSignText(),
		GoalSummary:    DescribeGoal(r.CurrentGoal()),
		PlanSummary:    RenderPlan(r.CurrentPlan()),
		Features:       perception.VisibleFeatures(b, r.cfg.BlockScanDistance),
		Craftable:      r.craftableCount(),
		LogsHeld:       b.CountInventoryItemsFor("oak_log") + b.CountInventoryItemsFor("birch_log") + b.CountInventoryItemsFor("spruce_log"),

		// Read the cliff the same way the body will. Measuring it here from a
		// second source would let the model decide against a cliff the bot
		// never sees, which is the one disagreement this cannot afford.
		LedgeAhead: movement.SenseLedge(b.WorldModel, pos, r.facingYaw()).IsCliff(),

		Underwater:        underwater,
		SecondsUnderwater: secondsUnder,
		IsRaining:         b.SurvivalMgr != nil && b.SurvivalMgr.IsRaining(),
		IsThundering:      b.SurvivalMgr != nil && b.SurvivalMgr.IsThundering(),
	}

	// Fold what is in view into the vocabulary, then hand the same pointer to
	// the snapshot. The state text, the curriculum and the planner all read it
	// in this tick, and a vocabulary that lags a tick behind the world is a
	// vocabulary that describes where the bot used to be.
	r.vocabulary.Merge(snap)
	snap.Vocabulary = r.vocabulary

	// The episode description carries the time left, which is the part the
	// model needs: a brief without a clock is a wish, and the model will happily
	// propose a build that takes two hours inside a 24 minute recording.
	if ep := r.CurrentEpisode(); ep.Objective != "" {
		snap.EpisodeText = ep.Describe(snap.Now)
	}

	return snap
}

// facingYaw reads the direction the body is pointing, for the readings that have
// to agree with the movement layer's.
func (r *Runner) facingYaw() float32 {
	if r == nil || r.b == nil {
		return 0
	}
	r.b.Mu.Lock()
	defer r.b.Mu.Unlock()
	return r.b.Yaw
}

// ConversationContext uses the same per-player history as chat, without holding
// the bot mutex while acquiring the history lock. Internal result prompts are omitted.
func (r *Runner) ConversationContext() string {
	b := r.b
	if b.AiClient == nil || b.AiClient.History == nil {
		return ""
	}
	b.Mu.Lock()
	user := b.LastChatPartner
	b.Mu.Unlock()
	if user == "" {
		return ""
	}
	history := b.AiClient.History.GetHistory(user)
	if len(history) > 6 {
		history = history[len(history)-6:]
	}
	var sb strings.Builder
	for _, message := range history {
		if strings.Contains(message.Content, "ACTION RESULT #") {
			continue
		}
		fmt.Fprintf(&sb, "%s: %s\n", message.Role, truncate(message.Content, 400))
	}
	return sb.String()
}

// visibleSignText lists the signage the bot can currently read. It is fed to
// Jev so a labelled room can be understood rather than blindly searched.
func (r *Runner) visibleSignText() []string {
	signs := r.b.Storage().FindSigns()
	out := make([]string, 0, len(signs))
	for i, sign := range signs {
		if i >= 3 {
			break
		}
		out = append(out, fmt.Sprintf("%q at %d,%d,%d", sign.Text, sign.Pos.X(), sign.Pos.Y(), sign.Pos.Z()))
	}
	return out
}

// craftableCount is how many distinct recipes the bot could make right now.
//
// It gates the craft activity. Offering "craft something" to a bot holding two
// sticks and nothing else produces a repeated attempt that always fails, which
// is a visible failure mode; offering it only when a recipe genuinely exists
// turns the activity into one that works.
func (r *Runner) craftableCount() int {
	held := r.b.GetHeldItem()
	hasTable := strings.Contains(held, "crafting_table")
	return len(r.b.ListCraftableItems(hasTable))
}

// freeInventorySlots counts the empty slots in the bot's inventory. The
// curriculum uses it so "go mine" is not offered to a bot that has nowhere to
// put what it mines.
func (r *Runner) freeInventorySlots() int {
	slots := r.b.GetInventorySlots()
	free := 0
	for slot := uint32(0); slot < 36; slot++ {
		stack, ok := slots[slot]
		if !ok || stack.Count <= 0 {
			free++
		}
	}
	return free
}

// isNight reads the world clock. It is deliberately nil-safe: a bot that has
// not spawned a survival manager yet has no opinion about the time rather than
// assuming it is day, because assuming wrongly means walking off into the dark
// on the first tick.
func (r *Runner) isNight() bool {
	if r.b.SurvivalMgr == nil {
		return false
	}
	return agiIsNight(r.b.SurvivalMgr.GetWorldTime(), r.cfg.NightStartTicks, r.cfg.NightEndTicks)
}

// hasBed reports whether the bot is carrying somewhere to sleep.
func (r *Runner) hasBed() bool {
	for _, name := range r.b.GetItemNames() {
		clean := strings.ToLower(strings.TrimPrefix(name, "minecraft:"))
		for _, keyword := range r.cfg.BedKeywords {
			if strings.Contains(clean, strings.ToLower(keyword)) {
				return true
			}
		}
	}
	return false
}

// nearbyPeople lists players within the vision radius, nearest first.
//
// The radius is the vision radius when vision is on, and a fixed 30 blocks
// otherwise — the decision layer still needs to know who is around even if the
// bot never turns its head at them.
func (r *Runner) nearbyPeople(pos mgl32.Vec3, lookTarget string) []Person {
	radius := r.cfg.NearbyRadius
	if r.cfg.Vision {
		radius = r.cfg.VisionRadius
	}

	b := r.b
	// Snapshot mutable player data under the bot lock. Visibility helpers take
	// that same lock themselves, so ray tests must run after it is released.
	b.Mu.Lock()
	positions := make(map[string]mgl32.Vec3, len(b.PlayerEntityIDs))
	lastChatPartner := b.LastChatPartner
	for name, id := range b.PlayerEntityIDs {
		if strings.EqualFold(name, b.Name) {
			continue
		}
		if playerPos, ok := b.PlayerPositions[id]; ok {
			positions[name] = playerPos
		}
	}
	b.Mu.Unlock()

	people := make([]Person, 0, len(positions))
	for name, playerPos := range positions {
		dist := float32(math.Sqrt(float64(playerPos.Sub(pos).LenSqr())))
		if dist > radius {
			continue
		}
		people = append(people, Person{
			Name:     name,
			Distance: dist,
			// Real line-of-sight, the same rule the block scan uses. Distance
			// alone would have the bot turn towards someone standing behind a
			// wall, which is the one thing a vision reflex must never do.
			HasLineOf:  perception.SeesPoint(b, playerPos.Add(mgl32.Vec3{0, r.cfg.EyeHeight, 0})),
			LookingAt:  strings.EqualFold(lookTarget, name),
			SpeakingTo: lastChatPartner != "" && strings.EqualFold(name, lastChatPartner),
			// Greeted is resolved outside the bot lock below, since it is the
			// runner's own state.
			Greeted: false,
		})
	}
	// The acknowledgement lives on the runner, not the bot, so it is read here
	// rather than under b.Mu. This is the field that stops ReflexLook from
	// re-firing on every gaze lapse and starving the rest of the tick.
	for i := range people {
		people[i].Greeted = r.greetedRecently(people[i].Name)
	}
	sortPeople(people)
	return people
}

func sortPeople(people []Person) {
	for i := 1; i < len(people); i++ {
		for j := i; j > 0 && people[j].Distance < people[j-1].Distance; j-- {
			people[j], people[j-1] = people[j-1], people[j]
		}
	}
}

// runReflex executes one local reaction.
func (r *Runner) runReflex(ctx context.Context, reflex Reflex) {
	// Recorded before the action, not after. "The reflex fired" and "the reflex
	// did what it was supposed to" are different claims, and a log that only
	// records the second one is a log that cannot tell you why the bot did not
	// eat, or did not surface, or did not run.
	r.b.Evidence.Record(evidence.KindReflex, reflex.Kind.String(), map[string]any{
		"person": reflex.Person,
		"hp":     r.snapshotHP(),
	})

	switch reflex.Kind {
	case ReflexFlee:
		r.b.Logger.Info("AGI: fleeing", slog.Int("hp", r.snapshotHP()))
		action.Execute(r.b, "flee", "", r.b.Name)
	case ReflexEat:
		r.b.Logger.Info("AGI: hungry, eating")
		if r.b.SurvivalMgr != nil {
			r.b.SurvivalMgr.Tick()
		}
	case ReflexLook:
		r.lookAt(reflex.Person)
	case ReflexSleep:
		r.b.Logger.Info("AGI: nightfall, going to bed")
		action.Execute(r.b, "sleep", "", r.audience())
	case ReflexShelter:
		r.b.Logger.Info("AGI: nightfall, seeking shelter")
		action.Execute(r.b, "shelter", "", r.audience())
	case ReflexSurface:
		seconds := r.secondsUnderwater()
		r.b.Logger.Warn("AGI: out of air, surfacing", slog.Int("seconds_under", seconds))
		r.surface()
	}
}

// waterWorld is the narrow view of the world that submersion needs.
//
// The bot hands out its world model as a three-method interface, and water is
// not one of them. Widening that interface for one reflex would push the change
// through every implementer in the tree; narrowing here, the way the pathfinder
// already does with loadAwareWorld and nameAwareQuerier, keeps the cost on the
// one place that wants it. A model that cannot answer is treated as dry, which
// is the safe direction: the breath reflex simply never fires.
type waterWorld interface {
	IsWater(x, y, z int32) bool
}

// waterWorldOf returns the bot's world model as something that can answer
// questions about water, or nil when it cannot.
func (r *Runner) waterWorldOf() waterWorld {
	// A runner with no bot is a legitimate state — the decision methods are
	// written to survive it, and NewBareForTest exists to exercise exactly that.
	// Asking a nil bot for its world model is not a thing that can be answered,
	// so the guard is here rather than at every call site.
	if r.b == nil {
		return nil
	}
	model := r.b.GetLocalWorldModel()
	if model == nil {
		return nil
	}
	water, ok := model.(waterWorld)
	if !ok {
		return nil
	}
	return water
}

// headUnderwater reports whether the bot's head is currently in water.
//
// The head is the whole answer, not a sample of the body. A bot can stand
// waist-deep and breathe; it drowns when the air runs out. Testing the cell at
// the head is the difference between "reflexively swims to the surface in a
// shallow puddle" and "keeps working", and only one of those is a person.
func (r *Runner) headUnderwater() bool {
	world := r.waterWorldOf()
	if world == nil {
		return false
	}
	pos := r.b.GetCoords()
	hx, hy, hz := pathfinder.HeadCell(
		int32(math.Floor(float64(pos.X()))),
		int32(math.Floor(float64(pos.Y()))),
		int32(math.Floor(float64(pos.Z()))),
	)
	return world.IsWater(hx, hy, hz)
}

// secondsUnderwater is how long the bot's head has been under, counting from
// the last time it was not.
//
// It resets on the surface rather than decaying, so surfacing for one tick and
// ducking back down does not hand the bot a fresh air supply it never had.
func (r *Runner) secondsUnderwater() int {
	if _, seconds, known := r.breath(); known {
		return seconds
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sinceSubmerged.IsZero() {
		return 0
	}
	return int(time.Since(r.sinceSubmerged) / time.Second)
}

// BreathSource is the read-only view of the movement package's swim controller.
//
// It is declared here rather than imported so the AGI layer keeps no dependency
// on movement, and it is read-only on purpose: the reflex must not be able to
// start, stop or age the clock, because a second writer is how the two clocks
// came to disagree in the first place.
type BreathSource interface {
	Breath() (underwater bool, secondsUnder int, known bool)
}

// SetBreathSource points the breath reflex at the swim controller's clock.
func (r *Runner) SetBreathSource(src BreathSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.swimBreath = src
}

// breath reports the submersion reading, preferring the swim controller's and
// falling back to this runner's own clock when nothing is wired.
func (r *Runner) breath() (underwater bool, seconds int, known bool) {
	r.mu.Lock()
	src := r.swimBreath
	r.mu.Unlock()
	if src != nil {
		return src.Breath()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sinceSubmerged.IsZero() {
		return false, 0, false
	}
	return true, int(time.Since(r.sinceSubmerged) / time.Second), true
}

// observeSubmersion updates the submersion clock and reports the two facts the
// breath reflex reads.
func (r *Runner) observeSubmersion(now time.Time) (underwater bool, seconds int) {
	// The swim controller's reading wins when it has one, so the snapshot the
	// brain sees and the plan the body is following are the same reading.
	if u, s, known := r.breath(); known {
		return u, s
	}

	underwater = r.headUnderwater()

	r.mu.Lock()
	defer r.mu.Unlock()
	if underwater {
		if r.sinceSubmerged.IsZero() {
			r.sinceSubmerged = now
		}
		seconds = int(now.Sub(r.sinceSubmerged) / time.Second)
		return underwater, seconds
	}
	r.sinceSubmerged = time.Time{}
	return false, 0
}

// maxSurfaceClimb bounds how far the surfacing reflex will look for air above
// the bot. A flooded mine shaft is a handful of blocks; a hundred is a
// pathological column, and searching for one is a bot standing at the bottom of
// it doing nothing while the air runs out.
const maxSurfaceClimb int32 = 24

// surface sends the bot up.
//
// It goes through the pathfinder rather than reaching into the movement input
// loop. Holding the jump key is what a player does, but jump state is owned by
// the movement agent and is rebuilt every tick from the steering solution, so
// setting it from outside would be overwritten on the next frame. Navigating to
// the first cell above with air at head height is the same instruction expressed
// in the vocabulary the bot already has: a reachable destination.
//
// If no air is found within the search window the bot stays where it is rather
// than climbing blindly. That is a real failure, and it is logged as one.
func (r *Runner) surface() {
	world := r.waterWorldOf()
	if world == nil {
		return
	}
	pos := r.b.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	for climb := int32(1); climb <= maxSurfaceClimb; climb++ {
		_, headY, _ := pathfinder.HeadCell(bx, by+climb, bz)
		if world.IsWater(bx, headY, bz) {
			continue
		}
		r.b.NavigateToBlock(bx, by+climb, bz, 1.0)
		return
	}
	r.b.Logger.Warn("AGI: no air found above the bot",
		slog.Int("searched_blocks", int(maxSurfaceClimb)),
		slog.Int("y", int(by)),
	)
}

func (r *Runner) snapshotHP() int {
	hp, _, _ := r.b.GetStatusDetails()
	return hp
}

// lookAt turns towards a player, honouring the cooldown so someone pacing back
// and forth does not set the bot's head swinging like a metronome.
//
// The acknowledgement is recorded whether or not the head actually turns. The
// reflex that calls this consumes the whole tick, so if a player stayed
// "unacknowledged" for as long as the gaze cooldown, every single tick would be
// spent looking at them and the brain would never decide to do anything. One
// arrival is one look; after that the player is known about and the rest of the
// loop is free to run.
func (r *Runner) lookAt(name string) {
	r.mu.Lock()
	// Acknowledge first: this is what stops the reflex re-firing, so it must not
	// depend on the head turn succeeding.
	r.greeted[strings.ToLower(name)] = time.Now()
	r.greetedAt = time.Now()

	if time.Since(r.lastGaze) < time.Duration(r.cfg.VisionCooldownSec)*time.Second {
		r.mu.Unlock()
		return
	}
	r.lastGaze = time.Now()
	r.mu.Unlock()

	hold := time.Duration(r.cfg.VisionHoldSec) * time.Second
	if !r.b.LookAtPlayer(name, hold) {
		return
	}
	r.b.Logger.Info("AGI: acknowledged a player", slog.String("player", name))
}

// greetedRecently reports whether the vision reflex has already reacted to a
// player within the gaze cooldown.
func (r *Runner) greetedRecently(name string) bool {
	window := time.Duration(r.cfg.VisionCooldownSec) * time.Second
	if window <= 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.greeted[strings.ToLower(name)]
	return ok && time.Since(at) < window
}

// shouldDeliberate gates the expensive half: is the model worth asking, and is
// the bot free to act on the answer.
func (r *Runner) shouldDeliberate(snap Snapshot, judgement Judgement) bool {
	if snap.Busy || snap.Exploring {
		return false
	}
	// When Jev is answering, its opinion is the reason to ask. Falling back to
	// the raw probability when it is not keeps the old cadence.
	if judgement.Known {
		return judgement.WorthSpeak || judgement.Activity != ""
	}
	return rand.Float64() < r.cfg.LLMChance
}

// deliberate asks the model what to do with itself and carries out the answer.
// deliberate asks the model what to do with itself and carries out the answer.
// The Jev judgement is passed in rather than re-fetched: it was taken a moment
// ago on the same snapshot, and a second call would be both slower and able to
// disagree with the one that gated this very decision.
func (r *Runner) deliberate(ctx context.Context, snap Snapshot, judgement Judgement) {
	if !r.canSpeakNow(snap, judgement) {
		// The model can still hand back an action tag even when it must not
		// speak, so this is a gate on words only — not on the whole call.
		r.b.Logger.Debug("AGI: deliberating without the right to speak")
	}

	systemPrompt, prompt := r.BuildDecisionPrompt(snap)

	reply, err := r.b.AiClient.Ask(r.audience(), systemPrompt, prompt)
	if err != nil {
		r.b.Logger.Debug("AGI decision call failed", slog.String("error", err.Error()))
		return
	}
	parsed := ai.Parse(reply)
	if isSilent(parsed) {
		return
	}

	if parsed.CleanReply != "" && r.cfg.Social && r.canSpeakNow(snap, judgement) {
		r.mu.Lock()
		r.lastSpoke = time.Now()
		r.mu.Unlock()
		r.b.Logger.Info("AGI: unprompted message", slog.String("reply", parsed.CleanReply))
		r.b.SendSafeChat(parsed.CleanReply)
	}

	for _, act := range parsed.Actions {
		// A model asked to act on the world gets to act on it, including
		// wandering. Gated through the same action dispatch as a player's
		// request, so there is exactly one execution path.
		action.Execute(r.b, act.Label, act.Param, r.audience())
	}
}

// audience is who an unprompted act is attributed to. The nearest player is
// right: it is the person most likely to have seen it happen.
func (r *Runner) audience() string {
	b := r.b
	b.Mu.Lock()
	defer b.Mu.Unlock()
	best := ""
	bestDist := float32(math.MaxFloat32)
	for name, id := range b.PlayerEntityIDs {
		pos, ok := b.PlayerPositions[id]
		if !ok {
			continue
		}
		dist := pos.Sub(b.Pos).Len()
		if dist < bestDist {
			bestDist = dist
			best = name
		}
	}
	if best == "" {
		return b.Name
	}
	return best
}

func (r *Runner) canSpeakNow(snap Snapshot, judgement Judgement) bool {
	r.mu.Lock()
	last := r.lastSpoke
	r.mu.Unlock()
	if !CanSpeak(last, snap.Now, time.Duration(r.cfg.SocialCooldownSec)*time.Second, snap.Busy) {
		return false
	}
	// Jev's silence is a real answer, not an absence: when it was asked and
	// said the moment is not worth speaking, honour it.
	if judgement.Known && !judgement.WorthSpeak {
		return false
	}
	return true
}

// isSilent reports a "stay quiet" answer, including an empty one. A model that
// has nothing to say usually says nothing at all, and inventing a task to fill
// the silence is the opposite of what makes a bot feel natural.
func isSilent(parsed ai.ParsedReply) bool {
	if strings.Contains(strings.ToLower(parsed.CleanReply), "<silent") {
		return true
	}
	return parsed.CleanReply == "" && len(parsed.Actions) == 0 && len(parsed.PlanSteps) == 0
}

// wander is exported for the session bootstrap so a bot with nothing to do can
// drift somewhere before the first decision tick.
func (r *Runner) Wander() {
	if r == nil || !r.cfg.Wander || r.b.Explorer == nil {
		return
	}
	r.mu.Lock()
	r.seed++
	seed := r.seed
	r.mu.Unlock()

	pos := r.b.GetCoords()
	x, y, z := WanderTarget(seed, [3]float32{pos.X(), pos.Y(), pos.Z()}, r.cfg.WanderRadius)
	r.b.Logger.Info("AGI: wandering",
		slog.Float64("x", float64(x)), slog.Float64("y", float64(y)), slog.Float64("z", float64(z)))
	go r.b.Explorer.ExploreRandom(context.Background(), time.Duration(r.cfg.WanderDurationSec)*time.Second)
}
