// Natural mode: the bot playing for its own sake, with nobody instructing it.
//
// The two other modes both wait for something to happen. Default waits for a
// player or a decision worth making. Planning waits for an objective to arrive
// from a player or a model. Natural has neither, and has to be interesting
// anyway — because that is the case a recording is made in, and a bot that only
// does something once it is told is not a bot anybody wants to watch for three
// hours.
//
// So the whole file is about what to do when nothing is being asked. It is not
// about being busy. A bot that is never idle reads as a bot; a bot that is
// never still reads as a machine. The work here is choosing between them on
// purpose rather than by accident.

package agi

import (
	"context"
	"io"
	"log/slog"
	"time"

	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/evidence"
)

// SuspendAfter is how long a human's attention keeps the episode paused.
//
// It is short on purpose. The person is there now; a minute later they are
// still there or they are not, and the bot should not sit frozen waiting to
// find out. Once the attention lapses the episode picks up exactly where it
// stopped, with the time it was given still running.
const SuspendAfter = 90 * time.Second

// log returns the bot's logger, or a silent one.
//
// The agi package's own tests run in-package and cannot build a *bot.Bot — the
// bot imports agi, so constructing one here would be an import cycle. Every
// method that only *notifies* therefore has to survive a runner with no bot, and
// a notification that can panic is worse than one that is occasionally missing.
func (r *Runner) log() *slog.Logger {
	if r == nil || r.b == nil || r.b.Logger == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return r.b.Logger
}

// note records an evidence line, tolerating a runner with no bot for the same
// reason log does.
func (r *Runner) note(kind evidence.Kind, detail string, fields map[string]any) {
	if r == nil || r.b == nil || r.b.Evidence == nil {
		return
	}
	r.b.Evidence.Record(kind, detail, fields)
}

// OneBlockStyle answers how the brain should read the world, as a short style
// line the models can be told about.
//
// It is a style rather than a verdict because that is what the consumers need:
// the planner has to write a different plan, and the decision model has to
// stop offering fishing. Neither of them needs to be told the detection is a
// guess, and telling them so would only make them hedge.
func (r *Runner) OneBlockStyle(nearBlocks string) string {
	reading := DetectOneBlock(nearBlocks)

	// The second reading is what turns a hint into a conclusion. A world that
	// looks empty twice in a row, with the bot in the same spot, is a
	// single-block world; a room has walls the bot was not scanning past.
	if reading == PossiblyOneBlock && r.oneBlockSeen {
		reading = ConfirmOneBlock(reading, r.oneBlockReading)
	}

	switch reading {
	case DefinitelyOneBlock:
		if !r.oneBlockConfirmed {
			r.oneBlockConfirmed = true
			r.log().Info("AGI: this is a single-block world", slog.String("how", "chose to believe"))
		}
		return "single_block"
	case PossiblyOneBlock:
		r.oneBlockSeen = true
		r.oneBlockReading = reading
		return "maybe_single_block"
	default:
		// A normal world is a fact worth forgetting: having once looked like
		// one block must not colour how the bot reads every later frame.
		r.oneBlockSeen = false
		r.oneBlockConfirmed = false
		return "normal"
	}
}

// IsOneBlockWorld is the brain's own read, for the paths that do not go through
// the chat layer's hook.
func (r *Runner) IsOneBlockWorld() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.oneBlockConfirmed
}

// SingleBlockBrief is the instruction a single-block world needs, or empty when
// this is an ordinary one.
func (r *Runner) SingleBlockBrief(under string) string {
	if !r.IsOneBlockWorld() {
		return ""
	}
	return OneBlockPlan(under)
}

// NaturalTick is the natural-mode branch of the brain loop.
//
// It sits below the reflex layer like planning mode does — a bot that finishes
// an episode by dying has not finished anything.
func (r *Runner) NaturalTick(ctx context.Context, snap Snapshot, judgement Judgement) {
	r.log().Info("AGI: naturalTick",
		slog.Bool("busy", snap.Busy),
		slog.Bool("exploring", snap.Exploring),
		slog.String("episode", r.CurrentEpisode().Objective),
	)

	// The watchdog goes first and always. A recording nobody is watching is
	// exactly the case where a dead or wedged bot goes unnoticed for an hour,
	// so this is the one thing in the mode that is not optional.
	if fault, action := r.Watch(snap); action != ActNone {
		r.actOnFault(fault, action, snap)
		return
	}

	// A human outranks the schedule. If somebody is talking to the bot, the bot
	// is talking back, and the episode waits. This is the single rule that
	// decides whether natural mode is a companion or a machine with a calendar.
	if r.AttendingToPlayer(nowish(snap)) {
		r.PlayAlong(snap, judgement)
		return
	}

	episode := r.CurrentEpisode()
	switch {
	case episode.Objective == "" && r.ShouldPlanNaturally(judgement):
		r.planningTick(ctx, snap, judgement)

	case episode.Objective == "":
		// No brief yet. The bot is expected to be playing for hours before
		// anybody hands it one, so this is the normal state and not a gap.
		r.FreePlay(snap, judgement)

	case episode.Over(snap.Now):
		r.endEpisode(episode, snap)

	default:
		r.runEpisode(ctx, episode, snap)
	}
}

// ShouldPlanNaturally keeps one active plan in charge of the motor layer. A
// configured objective needs a plan even when Jev is temporarily unavailable.
func (r *Runner) ShouldPlanNaturally(j Judgement) bool {
	return r.CurrentPlan().Objective != "" || r.CurrentGoal().Pinned || WantsBigBrain(j, EscalateThreshold)
}

// BeginEpisode installs a brief and reports whether it was usable.
//
// It is exported because the chat path is what calls it: a player types
// "/episode 1 24m build a house" and the recording starts from there.
func (r *Runner) BeginEpisode(line string, now time.Time) (Episode, bool) {
	episode, ok := ParseEpisode(line, now)
	if !ok {
		return Episode{}, false
	}

	r.mu.Lock()
	r.episode = episode
	// A new brief invalidates the old plan outright. Carrying a plan across
	// would mean the bot spends the first minutes of episode 2 finishing
	// episode 1, which is exactly what a viewer would notice and read as a
	// glitch.
	r.plan = Plan{}
	r.mu.Unlock()

	r.log().Info("AGI: episode started",
		slog.Int("episode", episode.Number),
		slog.Duration("budget", episode.EndsAt.Sub(episode.StartedAt)),
		slog.String("objective", episode.Objective),
	)
	r.note(evidence.KindEpisodeStart, episode.Objective, map[string]any{
		"episode": episode.Number,
		"budget":  episode.EndsAt.Sub(episode.StartedAt).String(),
		"ends_at": episode.EndsAt,
	})
	return episode, true
}

// EndEpisode clears a brief, letting the bot go back to free play.
func (r *Runner) EndEpisode(reason string) {
	r.mu.Lock()
	episode := r.episode
	r.episode = Episode{}
	r.mu.Unlock()
	if episode.Objective == "" {
		return
	}
	r.log().Info("AGI: episode ended", slog.String("reason", reason))
	r.note(evidence.KindEpisodeEnd, reason, map[string]any{
		"episode":   episode.Number,
		"objective": episode.Objective,
	})
}

// CurrentEpisode returns the active brief, or the zero Episode.
func (r *Runner) CurrentEpisode() Episode {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.episode
}

// Suspend marks that a human has the bot's attention.
//
// The episode clock keeps running — the recording is still going and the wall
// is real whether anyone is talking or not — but the bot stops working on it
// until the attention lapses.
func (r *Runner) Suspend(who string) {
	r.mu.Lock()
	r.suspendedUntil = time.Now().Add(SuspendAfter)
	r.suspendedBy = who
	r.mu.Unlock()
	if r.b != nil && r.b.Explorer != nil && r.b.Explorer.IsExploring() {
		r.b.Explorer.Stop()
	}
}

// AttendingToPlayer reports whether a human currently has the bot.
func (r *Runner) AttendingToPlayer(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.suspendedBy != "" && now.Before(r.suspendedUntil)
}

// BodyCommitted reports whether the bot's body already belongs to something.
//
// A player's "come here" is an action, not a suggestion: the action layer owns
// the walk for as long as the walk takes, and that can outlast the ninety
// seconds of attention that started it. The natural loop runs every second
// regardless, so without this the loop renegotiates a command mid-stride — and
// it does not even need to do anything dramatic to break it. Opening a rest
// period is enough, because a resting bot calls StopMovement. On camera that is
// a bot that walks a few blocks toward somebody, stops, and stands there.
//
// The two ways a body can be in use are a walk already in progress and an
// exploration drift, which is itself a long walk. Neither is negotiable, and
// neither is a reason to start anything new.
func BodyCommitted(snap Snapshot) bool {
	return snap.Busy || snap.Exploring
}

// PlayAlong leaves the motor to the player's chat action while attention is
// suspended. Starting a Jev activity here competes with the reply being executed.
func (r *Runner) PlayAlong(snap Snapshot, _ Judgement) {
	if BodyCommitted(snap) {
		return
	}
	r.mu.Lock()
	who := r.suspendedBy
	r.mu.Unlock()
	if who != "" {
		r.b.LookAtPlayer(who, 2*time.Second)
	}
}

// FreePlay is what the bot does with no brief and nobody talking to it.
//
// It deliberately does not manufacture urgency. There is no objective to serve,
// so the honest options are: look at what has been discovered, go and look at
// something new, or stand still for a while. Inventing a goal here would produce
// a bot that is permanently busy for no reason, which is the exact thing that
// makes a long recording tiring to watch.
func (r *Runner) FreePlay(snap Snapshot, judgement Judgement) {
	// Diagnostic: log the decision inputs so a frozen bot is never a mystery.
	// INFO level so it is visible without enabling debug.
	r.log().Info("AGI: freePlay",
		slog.Bool("jev_known", judgement.Known),
		slog.String("activity", judgement.Activity),
		slog.Bool("engaged", judgement.Engaged),
		slog.Bool("explorer_nil", r.b.Explorer == nil),
		slog.Int("chunks", r.b.WorldCache.ChunkCount()),
	)

	// The body already belongs to something, so this tick has no say in what the
	// bot does next. The action layer is finishing a walk or a drift, and a
	// second decision on top of that is a second opinion nobody asked for.
	if BodyCommitted(snap) {
		return
	}

	// A single-block world has exactly one thing to do, and it is not any of the
	// things on the menu. Offering fishing in a world with no water, or
	// gathering in a world with no wood, is how a bot spends ten minutes failing
	// the same way.
	if style := r.OneBlockStyle(snap.NearBlocks); style != "normal" {
		if r.IsOneBlockWorld() {
			r.oneBlockStep(snap)
			return
		}
	}

	// Something worth doing and nothing pressing: take it.
	if judgement.Known && judgement.Activity != "" && judgement.Engaged {
		r.applyLocomotion(judgement.Locomotion)
		r.applyDrop(judgement.DropOK)
		r.b.SetAppetite(judgement.Appetite)
		r.rememberAffordance(judgement.Affordance)
		r.applyGaze(judgement.Gaze)
		r.doActivity(judgement.Activity)
		return
	}

	// Nothing urgent. A bot that always does something is the failure this whole
	// mode exists to avoid, so the resting branch is the common one and it is
	// allowed to be common. The test is "have I been still too long", not "am I
	// resting", so rest stays reachable without a decision that has to be talked
	// into happening.
	if r.ShouldDrift(snap) {
		r.maybeWander()
		return
	}
	r.Idle(snap)
}

// runEpisode works the brief, watching the clock the whole way.
func (r *Runner) runEpisode(ctx context.Context, episode Episode, snap Snapshot) {
	// Past three quarters of the budget, the objective is no longer the point.
	// A bot that is four minutes from the end of a twenty-four minute recording
	// and halfway through a house has one honest move: stop, say what it
	// managed, and be somewhere safe. Half a house plus an overrun is worse than
	// something small and finished, because the overrun is the part a viewer
	// sees.
	if episode.WrappingUp(snap.Now) {
		r.wrapUp(episode, snap)
		return
	}

	// A plan for this episode is the work itself; the planning branch already
	// runs one step per tick and hands failures back for revision.
	r.planningTick(ctx, snap, judgementOf(snap))
}

// wrapUp ends an episode tidily rather than letting it run over.
func (r *Runner) wrapUp(episode Episode, snap Snapshot) {
	done, total := 0, 0
	if plan := r.CurrentPlan(); plan.Objective != "" {
		done, total = plan.progress()
	}
	r.log().Info("AGI: wrapping up",
		slog.Int("episode", episode.Number),
		slog.Int("steps_done", done),
		slog.Int("steps_total", total),
		slog.Duration("left", episode.Remaining(snap.Now)),
	)
	r.note(evidence.KindEpisodeWrap, episode.Objective, map[string]any{
		"episode":     episode.Number,
		"steps_done":  done,
		"steps_total": total,
		"left":        episode.Remaining(snap.Now).String(),
	})
}

// endEpisode retires a brief whose time is up.
func (r *Runner) endEpisode(episode Episode, snap Snapshot) {
	done, total := 0, 0
	if plan := r.CurrentPlan(); plan.Objective != "" {
		done, total = plan.progress()
	}
	r.log().Info("AGI: episode over",
		slog.Int("episode", episode.Number),
		slog.String("objective", episode.Objective),
		slog.Int("steps_done", done),
		slog.Int("steps_total", total),
		slog.Int("overrun_sec", int(snap.Now.Sub(episode.EndsAt).Seconds())),
	)
	r.EndEpisode("deadline reached")
}

// judgementOf is a placeholder judgement for the planning branch, which reads
// the speech gate. A recording has no audience to speak to, so nothing here is
// ever gated and the episode runs at full speed.
func judgementOf(Snapshot) Judgement { return Judgement{} }

// nowish is the snapshot's clock, defaulting to the wall clock when there is
// none. Tests build snapshots by hand and should not have to set Now to make a
// duration come out right.
func nowish(snap Snapshot) time.Time {
	if snap.Now.IsZero() {
		return time.Now()
	}
	return snap.Now
}

// oneBlockStep is the only move there is on a single block: break it, see what
// turned up, and put the next one on top.
//
// It never idles. Standing still is the one thing that cannot be right here —
// the block under the bot is the entire world, and nothing about it changes on
// its own, so a bot that waits has ended the recording.
func (r *Runner) oneBlockStep(snap Snapshot) {
	r.note(evidence.KindDiscovery, "single-block step", map[string]any{
		"under": snap.NearBlocks,
		"at":    snap.Coords,
	})
	// automine rather than mine: the block is under the bot, and the gatherer
	// already knows how to break what it is standing on and collect it.
	action.Execute(r.b, "automine", "", r.audience())
}

// ModeIsNatural is the branch condition, named so Tick reads as prose.
func (r *Runner) ModeIsNatural() bool { return r.cfg.Mode == config.ModeNatural }

// Watch runs one watchdog pass and reports what, if anything, to do about it.
//
// The repeat counter lives here rather than at the call site because "the same
// fault twice in a row" is only knowable by keeping the last one, and a caller
// that forgot to would either thrash or never fire.
func (r *Runner) Watch(snap Snapshot) (Fault, Action) {
	now := nowish(snap)
	health := r.observePosition(now, snap)
	fault := Diagnose(health)
	repeats := r.recordFault(fault)

	// A fault that clears must not hand its accumulated patience to the next
	// one. recordFault resets the counter on a change, and that is why it is
	// reset rather than merely compared.
	if fault == FaultNone {
		return fault, ActNone
	}
	return fault, actForFault(fault, repeats)
}

// observePosition folds a reading into the movement history and returns the
// health view of it.
//
// Position is compared, not timed. A server that applies a freeze keeps
// answering ticks, so a clock alone calls a wedged bot healthy for as long as
// it keeps replying.
//
// This is its own function so the mutex is taken exactly once. The body used to
// hold the lock across the whole reading and then take it again to count the
// repeat, and sync.Mutex is not reentrant: the second Lock blocks forever, so
// the watchdog never finished a pass, the AGI loop wedged on its first tick,
// and the bot stopped deciding anything on its own — it still answered chat,
// because that runs on a different goroutine. Every later stage of the same
// pass also has to be able to read the runner's state, so holding the lock for
// the whole function could not be the answer either.
func (r *Runner) observePosition(now time.Time, snap Snapshot) Health {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Movement is measured against the real position, never against the
	// formatted coordinate string.
	//
	// Snapshot.Coords is rendered with "%.0f" because it is written for a model
	// to read: "X:51 Y:80 Z:194". It is whole blocks, so a bot walking inside
	// one block produced an identical string, the watchdog read that as "has
	// not moved", and after thirty seconds it declared a bot that was plainly
	// walking to be stuck — then fired ActUnstick, which clears the world model
	// and makes the bot less able to find its way. A live run showed 144 of
	// those in four minutes while the log recorded sixteen separate repaths and
	// a dozen distinct positions.
	//
	// The prompt still gets the rounded string. Only the judgement uses metres.
	moved := r.lastPosition == "" || snap.Coords != r.lastPosition
	if r.b != nil {
		if pos := r.b.GetCoords(); r.lastPosition == "" || pos != r.lastVec {
			moved = true
			r.lastVec = pos
		}
	}
	if moved {
		r.lastMoved = now
		r.lastPosition = snap.Coords
	}
	if r.lastMoved.IsZero() {
		r.lastMoved = now
	}
	return Health{
		HP:              snap.HP,
		Disconnected:    r.disconnected,
		PositionChanged: moved,
		LastMoved:       r.lastMoved,
		Now:             now,
		WantsToMove:     snap.WantsToMove,
		Exploring:       snap.Exploring,
	}
}

// recordFault notes a fault and returns how many readings in a row it has now
// been seen.
//
// The streak is updated with the reading in hand BEFORE the action is chosen, so
// a fault is counted from the tick it was first seen. Counting it afterwards
// makes the first sighting look like a non-event and a two-tick threshold
// silently becomes a three-tick one.
func (r *Runner) recordFault(fault Fault) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	if fault == r.lastFault {
		r.faultRepeats++
	} else {
		r.faultRepeats = 1
	}
	r.lastFault = fault
	return r.faultRepeats
}

// actForFault maps a confirmed fault to what to do, applying the repeat rule only
// to the ambiguous one.
//
// Death and disconnection act immediately: waiting to confirm a dead bot means
// lying on the respawn screen for another tick, and a connection that is down is
// unambiguous. Stillness is different — one motionless tick can be anything, and
// a watchdog that thrash is one nobody leaves switched on.
func actForFault(fault Fault, repeats int) Action {
	switch fault {
	case FaultDisconnected:
		return ActReconnect
	case FaultDead:
		return ActRespawn
	case FaultStuck:
		if repeats < StuckNeedsRepeats {
			return ActNone
		}
		return ActUnstick
	default:
		return ActNone
	}
}

// Disconnected reports the connection state for the watchdog.
//
// It is a method because the state arrives from the network layer rather than
// from the brain, and the brain is the only thing that reads it.
func (r *Runner) Disconnected(state bool) {
	r.mu.Lock()
	r.disconnected = state
	r.mu.Unlock()
}

// actOnFault does what the watchdog decided, and says so.
//
// Every branch records to the evidence log as well as the console, because this
// is precisely the question a recording cannot answer after the fact: at minute
// ninety the bot was still, and was anything noticed.
func (r *Runner) actOnFault(fault Fault, action Action, snap Snapshot) {
	line := WatchReport(fault, action, r.repeats())
	r.log().Warn(line, slog.String("at", snap.Coords))
	r.note(evidence.KindWatchdog, line, map[string]any{
		"fault":  fault.String(),
		"action": action.String(),
		"at":     snap.Coords,
		"hp":     snap.HP,
	})

	switch action {
	case ActRespawn:
		if r.b != nil && r.b.SurvivalMgr != nil {
			r.b.SurvivalMgr.Tick()
		}
	case ActReconnect:
		// The connection is gone. Nothing else can be attempted until the
		// session layer puts it back, so the honest thing is to stop acting as
		// though the world is there and let the reconnect path run.
		r.log().Warn("AGI: connection lost; waiting for the session to redial")
	case ActUnstick:
		// A bot wedged against terrain is usually a pathfinder that believes it
		// can get through. Clearing the model's learned overrides for the cells
		// it is standing on is cheaper and more honest than teleporting it.
		if r.b != nil {
			if model, ok := r.b.GetLocalWorldModel().(interface{ Reset() }); ok {
				model.Reset()
			}
			r.b.StopMovement()
		}
		r.log().Info("AGI: cleared the world model to try to get unstuck")
	}
}

func (r *Runner) repeats() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.faultRepeats
}
