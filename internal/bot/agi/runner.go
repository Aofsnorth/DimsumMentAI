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
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/perception"
	"bedrock-ai/internal/bot/rand"
	"bedrock-ai/internal/config"
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
	seed      int
	// goal is the multi-tick objective the bot is currently pursuing. It is
	// empty when the bot has no goal, which is a supported state (and the
	// correct one at the start of a session): the brain then just wanders.
	goal Goal
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
	Enabled           bool
	TickIntervalSec   int
	LLMChance         float64
	SelfPreservation  bool
	LowHPThreshold    int
	LowHunger         int
	Wander            bool
	WanderDurationSec int
	Social            bool
	SocialCooldownSec int
	Vision            bool
	VisionRadius      float32
	VisionHoldSec     int
	VisionCooldownSec int

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

// ConfigFrom adapts the YAML config to the runner's own view of it, so the
// package does not depend on internal/config.
func ConfigFrom(c config.AGIConfig) Config {
	cfg := Config{
		Enabled:           c.Enabled,
		TickIntervalSec:   c.TickIntervalSec,
		LLMChance:         c.LLMChance,
		SelfPreservation:  c.SelfPreservation,
		LowHPThreshold:    c.LowHPThreshold,
		LowHunger:         c.LowHunger,
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
		if client := jev.FromEnv(c.Jev.BaseURL); client.Available {
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
	return &Runner{b: b, cfg: cfg, seed: rand.Intn(360)}
}

// Run drives the loop until the session ends. It is started as a goroutine by
// the session, so its context lifetime is the session lifetime.
func (r *Runner) Run(ctx context.Context) {
	if r == nil || r.b.AiClient == nil {
		return
	}

	interval := time.Duration(r.cfg.TickIntervalSec) * time.Second
	r.b.Logger.Info("AGI loop started",
		slog.Int("interval_sec", r.cfg.TickIntervalSec),
		slog.Float64("llm_chance", r.cfg.LLMChance),
		slog.Bool("social", r.cfg.Social),
		slog.Bool("wander", r.cfg.Wander),
		slog.Bool("vision", r.cfg.Vision),
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

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
		)
	} else {
		r.b.Logger.Info("AGI: running on local thresholds (Jev not configured)")
	}

	for {
		// Jitter the first tick so a reconnecting bot does not wake up in
		// lockstep with whatever it was doing before.
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(rand.Intn(r.cfg.TickIntervalSec)) * time.Second):
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.Tick(ctx)
			}
		}
	}
}

// Tick runs one pass of the brain: reflex first, then an optional decision.
func (r *Runner) Tick(ctx context.Context) {
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
			activity = r.alternativeActivity(activity)
		}
		r.goalProgress(activity)
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
// active goal narrows it — see (*Runner).menu, which is what the brain actually
// asks the model about. Keeping this pure and runner-free is what lets it be
// tested against snapshots directly.
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
	if s.NearBlocks != "" && s.NearBlocks != "none" {
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

// doActivity carries out the activity Jev chose.
func (r *Runner) doActivity(activity string) {
	who := r.audience()
	r.recordActivity(activity)
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

// currentGoal returns the active goal, or the zero Goal when there is none.
func (r *Runner) currentGoal() Goal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.goal
}

// menu is the activity list the model is asked about: the full curriculum,
// narrowed by the active goal.
//
// A goal that does not constrain the menu is a goal in name only — the bot
// would keep making independent choices and merely describe them as progress.
func (r *Runner) menu(s Snapshot) []string {
	full := Curriculum(s)
	goal := r.currentGoal()
	if goal.Name == "" {
		return full
	}
	return NarrowToGoal(full, goal)
}

// adoptGoal installs a goal and gives it a lifetime. An empty name clears it.
func (r *Runner) adoptGoal(name string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		r.goal = Goal{}
		return
	}
	entry, ok := goalCatalogue[name]
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
	r.goal = newGoal(entry, now, goalLifetime)
}

// considerGoal decides whether to adopt the model's goal choice, and reports
// whether the goal actually changed.
//
// Switching is the part that needs guarding. A model that is unsure about a new
// intention must not be allowed to talk the bot out of what it is already
// doing, because oscillating between two goals every tick looks busy while
// accomplishing nothing. Keeping the current goal is the human behaviour: people
// change their mind on evidence, not because a coin flipped. With no current
// goal there is nothing to defend, so even a weak answer establishes one --
// otherwise the bot could never start.
func (r *Runner) considerGoal(choice string, confidence float64, now time.Time) bool {
	current := r.currentGoal()
	switching := current.Name != "" && current.Name != choice
	if switching && confidence < goalConfidenceFloor {
		return false
	}
	r.adoptGoal(choice, now)
	return true
}

// goalProgress records that an activity advanced the active goal.
func (r *Runner) goalProgress(activity string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.goal.Name != "" && r.goal.advances(activity) {
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

// alternativeActivity picks a substitute when the model repeats itself. It
// steps down to rest rather than sideways into another action: a bot that
// alternates between two activities on a fixed beat is as mechanical as one
// that repeats a single action, and resting is the honest answer to "I have
// nothing better to do".
//
// The one exception is an activity that advances the active goal. Repeating
// "mine" while working towards getting wood is not a loop, it is the whole
// point; breaking out of it would make the goal impossible to finish.
func (r *Runner) alternativeActivity(activity string) string {
	if r.currentGoal().advances(activity) {
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
	if goal := r.currentGoal(); goal.Name != "" && goal.Expired(snap.Now) {
		r.b.Logger.Info("AGI: goal expired",
			slog.String("goal", goal.Name),
			slog.Int("progress", goal.Progress),
		)
		r.adoptGoal("", snap.Now)
	}

	if r.cfg.Jev == nil {
		return Judgement{}
	}
	state := describeState(snap)

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
	if choice, confidence, ok := resp.Choice(jev.QGoal); ok {
		if r.considerGoal(choice, confidence, snap.Now) {
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
		current := r.currentGoal()
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
	for name, raw := range jev.BuildActivityQuestion(r.menu(s)) {
		questions[name] = raw
	}
	return questions
}

// describeState renders the snapshot as the flat text Jev reasons over. It is
// deliberately a short, factual description: Jev decides, so it needs the
// situation, not a personality and not instructions about how to talk.
func describeState(s Snapshot) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Health %d/20. Hunger %d/20. Position %s.\n", s.HP, s.Hunger, s.Coords)
	fmt.Fprintf(&sb, "Time: %s. Free inventory slots: %d.\n", timeOfDay(s), s.FreeSlots)
	fmt.Fprintf(&sb, "Current goal: %s.\n", s.GoalSummary)
	fmt.Fprintf(&sb, "Holding: %s.\n", s.HeldItem)
	fmt.Fprintf(&sb, "Visible mobs: %s.\n", s.VisibleMob)
	fmt.Fprintf(&sb, "Nearby blocks: %s.\n", s.NearBlocks)
	fmt.Fprintf(&sb, "Visible signs: %s.\n", describeSigns(s))
	fmt.Fprintf(&sb, "Players nearby: %s.\n", DescribePeople(s.Nearby))
	if s.Busy {
		sb.WriteString("The bot is already doing something.\n")
	}
	return sb.String()
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

func (r *Runner) thresholds() Thresholds {
	return Thresholds{LowHP: r.cfg.LowHPThreshold, LowHunger: r.cfg.LowHunger}
}

// agiIsNight is the package's own night check, aliased so the runner reads
// without qualifying a name that would shadow the agi package qualifier.
func agiIsNight(ticks, start, end int64) bool { return IsNightTime(ticks, start, end) }

// Observe builds an immutable reading of the world for one decision.
func (r *Runner) Observe() Snapshot {
	b := r.b
	hp, hunger, coords := b.GetStatusDetails()

	b.Mu.Lock()
	busy := b.IsBusy()
	pos := b.Pos
	lookTarget := b.LookTargetName
	actors := make(map[uint64]*entity.Info, len(b.Actors))
	for id, info := range b.Actors {
		if info == nil {
			continue
		}
		copied := *info
		actors[id] = &copied
	}
	b.Mu.Unlock()

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

	snap := Snapshot{
		Now:          time.Now(),
		Coords:       coords,
		HP:           hp,
		Hunger:       hunger,
		HeldItem:     b.GetHeldItem(),
		Inventory:    b.GetInventorySummary(),
		VisibleMob:   visibleMobs,
		NearBlocks:   perception.BlocksSummary(b, r.cfg.BlockScanDistance, r.cfg.BlockScanLimit),
		Busy:         busy,
		Exploring:    b.Explorer != nil && b.Explorer.IsExploring(),
		IsNight:      r.isNight(),
		HasBed:       r.hasBed(),
		FreeSlots:    r.freeInventorySlots(),
		Nearby:       r.nearbyPeople(pos, lookTarget),
		VisibleSigns: r.visibleSignText(),
		GoalSummary:  describeGoal(r.currentGoal()),
		Features:     perception.VisibleFeatures(b, r.cfg.BlockScanDistance),
		Craftable:    r.craftableCount(),
	}
	return snap
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
	b.Mu.Lock()
	defer b.Mu.Unlock()

	people := make([]Person, 0, 4)
	for name, id := range b.PlayerEntityIDs {
		if strings.EqualFold(name, b.Name) {
			continue
		}
		playerPos, ok := b.PlayerPositions[id]
		if !ok {
			continue
		}
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
			SpeakingTo: b.LastChatPartner != "" && strings.EqualFold(name, b.LastChatPartner),
		})
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
	}
}

func (r *Runner) snapshotHP() int {
	hp, _, _ := r.b.GetStatusDetails()
	return hp
}

// lookAt turns towards a player, honouring the cooldown so someone pacing back
// and forth does not set the bot's head swinging like a metronome.
func (r *Runner) lookAt(name string) {
	r.mu.Lock()
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

	systemPrompt, prompt := r.buildDecisionPrompt(snap)

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
