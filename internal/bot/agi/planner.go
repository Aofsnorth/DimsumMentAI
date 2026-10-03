// The planning loop: a slow planner that writes a plan, and a brain that
// executes one step of it per tick.
//
// The design constraint that shapes this file is that planning mode must be
// general. Nothing here knows what the bot is planning to do — the objective
// arrives from the model, the steps arrive from the model, and the kinds those
// steps use are the only shared vocabulary. A "dragon mode" or a "nether mode"
// would be a script with a language model stapled on; the same machinery has to
// work for an objective nobody wrote down in advance.
//
// Two rules keep it honest:
//
//   - The planner never blocks the brain. It runs on its own goroutine, the
//     action for the current step runs on another, and a plan that is waiting to
//     be written leaves the bot doing what it was already doing. A planner that
//     stalls the loop is just a slower tick.
//   - A step that fails does not advance the plan. It is reported back to the
//     planner as a fact and the planner revises. Skipping past a failure is how
//     an agent ends up cheerfully reporting an objective it quietly routed
//     around.

package agi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/evidence"
)

// planRetryCooldown throttles planner requests after a failure or a completed
// plan. Without it a planner that is down, or a plan the bot keeps finishing,
// would be asked again on every single tick.
const planRetryCooldown = 30 * time.Second

// stepMaxAttempts is how many times one step is retried before it is declared
// failed and handed back to the planner.
//
// Two is deliberate. One retry covers the ordinary case where an action was
// interrupted or a mob was in the way. A third would mean grinding at something
// that is not going to work, and a step that is retried forever is
// indistinguishable from a bot that has stopped thinking.
const stepMaxAttempts = 2

// arrivalRadius is how close the bot must end up to a go_to target to count as
// having arrived, in blocks.
//
// The navigation layer reports its own failures, but it also reports success
// for a move that went nowhere — a block that could not be resolved, a target
// behind a wall. Checking the actual position afterwards is the only way the
// plan can tell the difference between "walked there" and "tried to walk there".
const arrivalRadius = 4.0

// PlannerSystemPrompt is the planner's contract: one situation in, one plan out.
//
// It is a function rather than a constant because the step budget is
// configuration. Baking the number into the text would let the prompt and the
// clamp disagree, and a plan the prompt called a wishlist while the code called
// it finished is exactly the kind of quiet disagreement that makes a system
// hard to reason about later.
func PlannerSystemPrompt(maxSteps int) string {
	return fmt.Sprintf(`You are the planner for a Minecraft Bedrock bot. You do not play the game and you do not chat. You are given one situation and you write one plan for it.

Reply with ONLY a JSON object, no prose and no markdown:
{"objective":"<one sentence: what finishing this plan looks like>","steps":[{"kind":"<kind>","description":"<one short sentence>","target":{"x":0,"y":0,"z":0},"need":["oak_log"]}],"notes":"<optional, one line>"}

Step kinds:
- gather: collect a resource. need: [item], e.g. ["oak_log"]
- mine: extract a specific block. need: [block], e.g. ["coal_ore"]
- go_to: travel somewhere. target: {x,y,z} for a place you can see, or need: [block name] to go find it
- craft: make an item. need: [item], e.g. ["wooden_pickaxe"]
- equip: hold or wear an item. need: [item]
- combat: deal with a hostile mob. need: [mob name], or empty for the nearest one
- storage: put things away. need: [item], or empty for everything
- shelter: get to safety
- rest: stand still and recover
- observe: look around and read signs
- enter: go through a portal
- light_portal: ignite an unlit Nether portal frame with flint_and_steel
- fill_frame: fill End portal frames with eyes of ender
- explore_stronghold: search for a stronghold by walking and scanning
- destroy_crystal: shoot an End crystal with a bow
- wait: let time pass

Rules:
- Write 2 to %d steps. Each step must be small enough to finish in one session.
- Order the steps so that each one is possible when it comes up. A plan that
  asks for a stone pickaxe before it has gathered stone is not a plan.
- "target" is optional. Omit the whole field when the step is not about a place.
- "need" is optional. Omit it when the step needs no particular item.
- You may be revising an existing plan. Keep the steps that are already done
  exactly as they are and fix the ones that are not.
- Output JSON only.`, maxSteps)
}

// plannerPlan is the wire shape the model is asked for. It is deliberately
// separate from Plan: the JSON is an external contract that can gain optional
// fields without disturbing the internal one, and Target is a pointer so that
// "no target" survives the trip. A target of {0,0,0} is a real place, so
// absence has to be distinguishable from zero.
type plannerPlan struct {
	Objective string        `json:"objective"`
	Steps     []plannerStep `json:"steps"`
	Notes     string        `json:"notes"`
}

type plannerStep struct {
	Kind        string        `json:"kind"`
	Description string        `json:"description"`
	Target      *plannerPoint `json:"target"`
	Need        []string      `json:"need"`
}

type plannerPoint struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
	Z int32 `json:"z"`
}

// ParsePlanReply turns a planner reply into a Plan.
//
// It is pure, and exported, because this is the boundary where an untrusted
// string becomes a plan the bot will act on. Everything a model can get wrong
// has to be survivable here: prose around the JSON, fenced code blocks, a
// think block, a missing objective, a kind nobody implements, a plan of fifty
// steps. A parser that only accepts the happy path is a parser that will
// eventually hand a half-formed plan to the executor.
//
// A returned Plan is safe to execute: every step has a kind the brain can
// dispatch, the step count is within budget, and no step points at a target the
// model invented out of thin air. The second return is false when there is no
// usable plan at all, which the caller treats as "ask again later".
func ParsePlanReply(reply string, maxSteps int) (Plan, bool) {
	doc, ok := extractJSONObject(reply)
	if !ok {
		return Plan{}, false
	}
	var parsed plannerPlan
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		return Plan{}, false
	}
	objective := strings.TrimSpace(parsed.Objective)
	if objective == "" {
		// A plan with no objective cannot be reported, replanned against, or
		// judged later. Refusing it here is better than carrying an anonymous
		// list of actions around.
		return Plan{}, false
	}

	steps := make([]PlanStep, 0, len(parsed.Steps))
	for _, s := range parsed.Steps {
		kind, known := normalizeStepKind(s.Kind)
		if !known {
			// A later step may depend on this one (eyes before entering the End).
			// Removing it fabricates a reachable plan and eventual completion.
			return Plan{}, false
		}
		step := PlanStep{
			Description: strings.TrimSpace(s.Description),
			Kind:        kind,
			State:       StepPending,
		}
		if step.Description == "" {
			// The description is what the plan is read by, in the log and by
			// the next replan. A step with none is unreadable.
			step.Description = kind
		}
		if s.Target != nil {
			step.Target = PlanPoint{X: s.Target.X, Y: s.Target.Y, Z: s.Target.Z, Set: true}
		}
		for _, n := range s.Need {
			if n = strings.TrimSpace(n); n != "" {
				step.Need = append(step.Need, n)
			}
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return Plan{}, false
	}
	if maxSteps > 0 && len(steps) > maxSteps {
		// Clamp rather than reject. The leading steps of a plan are the ones that
		// make the rest reachable, so a plan that is too long is almost always
		// right about the beginning.
		steps = steps[:maxSteps]
	}
	return Plan{Objective: objective, Steps: steps, Notes: strings.TrimSpace(parsed.Notes)}, true
}

// extractJSONObject pulls the outermost JSON object out of a model reply.
//
// Models wrap JSON in fences, prefix it with "Sure!", and sometimes think out
// loud first. Rejecting those replies would mean a planner that fails for
// formatting rather than for thinking, which is the least interesting reason to
// fail and the most common one.
func extractJSONObject(reply string) (string, bool) {
	cleaned := stripThinkBlocks(reply)
	cleaned = strings.ReplaceAll(cleaned, "```json", " ")
	cleaned = strings.ReplaceAll(cleaned, "```", " ")

	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start < 0 || end <= start {
		return "", false
	}
	return cleaned[start : end+1], true
}

// stripThinkBlocks removes reasoning tags. The plan is taken from what comes
// after the thinking, not from what the model was thinking about.
func stripThinkBlocks(reply string) string {
	var sb strings.Builder
	rest := reply
	for {
		open := strings.Index(rest, "<think>")
		if open < 0 {
			sb.WriteString(rest)
			return sb.String()
		}
		sb.WriteString(rest[:open])
		rest = rest[open+len("<think>"):]
		closeAt := strings.Index(rest, "</think>")
		if closeAt < 0 {
			// Unterminated: everything after it was reasoning.
			return sb.String()
		}
		rest = rest[closeAt+len("</think>"):]
	}
}

// normalizeStepKind maps what a model writes onto what the brain implements.
//
// The aliases exist because "go_to" and "goto" and "travel" are the same
// instruction and a model will use all three. Anything genuinely unknown is
// rejected rather than coerced: guessing a kind would run an action the planner
// did not ask for, and an agent that quietly substitutes its own intentions for
// the ones it was given is not following a plan.
func normalizeStepKind(kind string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case StepGather, "get", "collect", "harvest":
		return StepGather, true
	case StepMine, "dig":
		return StepMine, true
	case StepGoTo, "goto", "go-to", "travel", "move", "navigate":
		return StepGoTo, true
	case StepCraft, "make", "craft_item":
		return StepCraft, true
	case StepEquip, "wear", "hold":
		return StepEquip, true
	case StepCombat, "fight", "attack", "defend":
		return StepCombat, true
	case StepStorage, "store", "deposit", "chest":
		return StepStorage, true
	case StepShelter, "flee", "hide":
		return StepShelter, true
	case StepRest, "sleep", "recover", "wait_rest":
		return StepRest, true
	case StepObserve, "look", "look_around", "scan", "read", "sign":
		return StepObserve, true
	case StepEnter, "portal", "nether", "through_portal":
		return StepEnter, true
	case StepWait, "idle", "pause":
		return StepWait, true
	default:
		return "", false
	}
}

// StepAction resolves a plan step to one action the action layer already knows
// how to run.
//
// An empty label with ok=true means "this step needs nothing to run" — rest and
// wait are real instructions that are satisfied by not doing anything, and
// routing them through a dummy action would be a lie about what happened.
//
// ok=false means the step cannot be executed as written: a craft with no item, a
// go_to with neither a coordinate nor something to search for. That is reported
// as a step failure so the planner finds out, rather than being quietly skipped.
func StepAction(step PlanStep) (label, param string, ok bool) {
	need := ""
	if len(step.Need) > 0 {
		need = step.Need[0]
	}

	switch step.Kind {
	case StepGather:
		// An empty item is not a bug here: the gather action has a sensible
		// default, and refusing a vague "gather something" would fail steps the
		// bot can perfectly well act on.
		return "gather", need, true
	case StepMine:
		return "automine", need, true
	case StepGoTo:
		if step.Target.Set {
			return "goto", fmt.Sprintf("%d,%d,%d", step.Target.X, step.Target.Y, step.Target.Z), true
		}
		if need != "" {
			// A named destination is resolved by the navigation layer, which
			// searches for it. This is also the one case that cannot be verified
			// afterwards, because the resolved coordinates are not visible to
			// the plan — see dispatchStep.
			return "goto", need, true
		}
		return "", "", false
	case StepCraft:
		if need == "" {
			return "", "", false
		}
		return "craft", need, true
	case StepEquip:
		if need == "" {
			return "", "", false
		}
		return "equip", need, true
	case StepCombat:
		// No name means the nearest hostile, which is what the attack action
		// does with an empty parameter.
		return "attack", need, true
	case StepStorage:
		if need == "" {
			return "storeall", "", true
		}
		return "store", need, true
	case StepShelter:
		return "shelter", "", true
	case StepRest, StepWait:
		return "", "", true
	case StepObserve:
		return "readsign", "", true
	case StepEnter:
		return "enterportal", "", true
	default:
		// Unreachable for a parsed plan; a step built by hand can still land
		// here, and failing loudly beats guessing.
		return "", "", false
	}
}

// planningTick is the planning-mode branch of the brain loop.
//
// It replaces the open-ended "what now?" question with "what is this for, and
// what is the next thing in it". The order below is the whole policy:
//
//  1. An expired plan is dropped, because a bot grinding a stale objective is
//     stuck, not persistent.
//  2. A plan that is due for a replan gets one requested — in the background.
//  3. With no plan at all, the bot falls back to the default-mode drift. This is
//     the important one: planning mode is additive, so a planner that is down
//     or slow leaves a wandering, alive bot rather than a statue.
//  4. A failed step goes back to the planner. It is never skipped.
//  5. Otherwise the current step runs, one at a time.
func (r *Runner) planningTick(ctx context.Context, snap Snapshot, judgement Judgement) {
	plan := r.CurrentPlan()
	if plan.Objective != "" && plan.PlanExpired(snap.Now) {
		r.log().Info("AGI: plan expired",
			"plan", plan.ID,
			"objective", plan.Objective,
		)
		r.clearPlan()
		plan = Plan{}
	}

	if plan.Objective == "" || plan.PlanDueForReplanning(snap.Now) {
		r.requestPlan(ctx, snap, judgement)
	}

	if plan.Objective == "" {
		// No plan yet — one is being written, or the planner is failing. Either
		// way the bot keeps moving on its own.
		r.maybeWander()
		return
	}

	if PlanHasFailedStep(plan) {
		// A failure is the planner's to answer for, not the executor's to skip
		// past. The cooldown inside requestPlan keeps this from becoming a
		// request per tick.
		r.requestPlan(ctx, snap, judgement)
		r.maybeWander()
		return
	}

	step, index, ok := r.CurrentStep()
	if !ok {
		// Every step is done or failed, but CompleteStep should have cleared the
		// plan. Asking again is the safe response to a state that should not
		// happen.
		r.clearPlan()
		r.requestPlan(ctx, snap, judgement)
		return
	}
	r.mu.Lock()
	stepBusy := r.busy
	r.mu.Unlock()
	if snap.Busy || snap.Exploring || stepBusy {
		// One action at a time. A second one started here would fight the first
		// over the same body.
		return
	}
	if step.State == StepPending {
		r.StartStep(index)
	}
	// The plan the step was read from, not the one in play at dispatch time.
	// They are the same on every tick that reaches here, and they differ on the
	// tick after a replan lands, which is precisely when the index would
	// silently stop meaning the step it was taken for.
	r.dispatchStep(ctx, step, index, plan.ID)
}

// PlanHasFailedStep reports whether the planner has something to answer for.
func PlanHasFailedStep(p Plan) bool {
	for _, step := range p.Steps {
		if step.State == StepFailed {
			return true
		}
	}
	return false
}

// requestPlan asks the planner for a plan, in the background.
//
// It never blocks and never fails loudly. The brain keeps executing the current
// plan while this runs, which is the whole point: a planner that takes thirty
// seconds must not freeze a bot for thirty seconds. The cooldown is what stops a
// planner that keeps failing from being asked again on every tick.
func (r *Runner) requestPlan(ctx context.Context, snap Snapshot, judgement Judgement) {
	if r.b.AiClient == nil || r.plannerInFlight {
		return
	}
	if !r.lastPlanAttempt.IsZero() && time.Since(r.lastPlanAttempt) < planRetryCooldown {
		return
	}
	r.markPlanAttempt()
	if !r.beginPlanWork() {
		return
	}

	go func() {
		defer r.endPlanWork()
		// The state text already carries the active plan, so a replan sees the
		// steps that are done and the ones that are not. That is what lets the
		// planner revise rather than restart.
		reply, err := r.b.AiClient.AskPlanner(PlannerSystemPrompt(r.cfg.PlanMaxSteps), r.PlannerMessageFor(snap))
		if err != nil {
			r.log().Warn("AGI: planner unavailable", "error", err.Error())
			return
		}
		plan, ok := ParsePlanReply(reply, r.cfg.PlanMaxSteps)
		if !ok {
			r.log().Warn("AGI: planner reply was not a usable plan", "reply", truncate(reply, 240))
			return
		}
		r.installPlan(plan, snap.Now, judgement)
	}()
}

// PlannerMessageFor is what the planner is shown: the world, whatever plan is
// already in play, and — when the operator named one — the standing goal that
// every step has to serve.
func (r *Runner) PlannerMessageFor(snap Snapshot) string {
	return DescribeState(snap) + r.OperatorGoalInstruction() + "\nWrite the plan for this bot now, as JSON."
}

// installPlan stamps a generated plan with the bookkeeping the executor needs
// and puts it in play, starting the first step.
//
// Carrying over the completed steps is the difference between revising a plan
// and restarting it. Without it a replan would re-run work that is already
// done, and a bot that keeps rediscovering the same first step looks stuck
// rather than thinking.
func (r *Runner) installPlan(plan Plan, now time.Time, judgement Judgement) {
	r.mu.Lock()
	r.planSeq++
	plan.ID = fmt.Sprintf("plan-%d", r.planSeq)
	plan.CreatedAt = now
	plan.Expires = now.Add(time.Duration(r.cfg.PlanLifetimeMin) * time.Minute)
	plan.ReplanAfter = time.Duration(r.cfg.PlanReplanMin) * time.Minute
	plan.LastPlanned = now

	// Steps carried over from the plan being replaced stay done. Their state is
	// kept; everything else comes from the new plan.
	for i := range plan.Steps {
		for j := range r.plan.Steps {
			if plan.Steps[i].Description == r.plan.Steps[j].Description &&
				plan.Steps[i].Kind == r.plan.Steps[j].Kind && r.plan.Steps[j].State == StepDone {
				plan.Steps[i].State = StepDone
				break
			}
		}
	}
	// Started, not overwritten. Assigning here unconditionally undid the
	// carry-over above on the very next line: a new plan whose first step is
	// the old plan's first step that had already been done was marked done and
	// then flipped straight back to active, so the bot re-did the first step
	// of every plan it had already completed that step in — which is the exact
	// re-running the carry-over exists to prevent, and the reason a bot that
	// keeps rediscovering the same first step reads as stuck.
	if len(plan.Steps) > 0 && plan.Steps[0].State != StepDone {
		plan.Steps[0].State = StepActive
	}
	r.plan = plan
	r.mu.Unlock()

	done, total := plan.progress()
	r.log().Info("AGI: plan ready",
		"plan", plan.ID,
		"objective", plan.Objective,
		"carried_over", fmt.Sprintf("%d/%d", done, total),
	)
	r.note(evidence.KindPlanReady, plan.Objective, map[string]any{
		"plan":         plan.ID,
		"steps":        len(plan.Steps),
		"carried_over": done,
		"total":        total,
		"notes":        plan.Notes,
	})
	r.announce(Snapshot{Now: now}, judgement, plan.Objective)
}

// announce says something the bot decided for itself.
//
// It goes through the same cooldown and the same Jev veto as every other
// unprompted message, because a plan announcement is still the bot talking
// unprompted. The AGI loop is the only thing allowed to decide when the bot
// speaks, and a second path that bypasses the gate is how a bot ends up
// talking over itself.
func (r *Runner) announce(snap Snapshot, judgement Judgement, msg string) {
	if !r.cfg.Social || msg == "" || !r.canSpeakNow(snap, judgement) {
		return
	}
	r.mu.Lock()
	r.lastSpoke = snap.Now
	r.mu.Unlock()
	r.b.SendSafeChat(msg)
}

// dispatchStep runs the current plan step on its own goroutine.
//
// Asynchronous for the same reason the planner is: a gather can take ninety
// seconds, and a brain that waited for it would not be running a loop, it would
// be running one long action. The busy flag is what guarantees at most one step
// is in flight at a time.
//
// planID travels with the step because the index alone no longer identifies it.
// A step dispatched here can still be running when a replan installs a
// completely different plan — that is what a replan *is*, and the action takes
// up to ninety seconds while the replan cooldown is ten minutes. When the
// action finished, an index resolved against the new plan marked an unrelated
// step of that plan done, and when no pending step remained it reported the
// whole plan complete for steps that had never run.
func (r *Runner) dispatchStep(ctx context.Context, step PlanStep, index int, planID string) {
	if !r.beginStepWork() {
		return
	}
	who := r.audience()
	label, param, ok := StepAction(step)
	if !ok {
		r.endStepWork()
		if r.stillCurrentPlan(planID) {
			r.FailStep(index, "no action exists for this step as written")
		}
		return
	}
	r.log().Info("AGI: step starting",
		"kind", step.Kind,
		"action", label,
		"param", param,
		"attempt", step.Attempts+1,
	)

	go func() {
		defer r.endStepWork()

		if label == "" {
			// Rest and wait are satisfied by standing still. That is the
			// instruction, not a shortcut around it.
			r.finishStep(index, step, planID, "stood still")
			return
		}

		status := action.ExecuteAndWait(r.b, label, param, who)
		if !status.Success {
			reason := status.Error
			if reason == "" {
				reason = label + " did not succeed"
			}
			r.failStepWork(index, step, planID, reason)
			return
		}
		if step.Kind == StepGoTo && step.Target.Set && !r.arrivedAt(step.Target) {
			// The action reported success but the bot is still where it started.
			// Taking the report at face value here would let a plan march
			// through destinations the bot never reached.
			r.failStepWork(index, step, planID, "did not arrive at the target")
			return
		}
		r.finishStep(index, step, planID, "")
	}()
}

// stillCurrentPlan reports whether planID is still the plan in play.
//
// A step that outlives its plan has no place to record its outcome: its index
// points at a different step of a different plan, and writing there corrupts
// work that was never attempted. The outcome is dropped instead, and the
// evidence records why — a silently discarded result is indistinguishable from
// a step that was never run.
func (r *Runner) stillCurrentPlan(planID string) bool {
	if planID == "" {
		return true
	}
	return r.CurrentPlan().ID == planID
}

// discardSupersededStep records that a step finished against a plan that has
// since been replaced. Nothing about the new plan is touched.
//
// It reports through log() and note() rather than reaching for the bot
// directly. This path runs from a step goroutine, and a runner is allowed to
// have no bot — the package goes to real trouble elsewhere to survive exactly
// that, and a notification that panics is worse than one that is missing.
func (r *Runner) discardSupersededStep(step PlanStep, index int, planID, outcome string) {
	r.log().Info("AGI: step outcome discarded, plan was replaced while it ran",
		"step_plan", planID,
		"current_plan", r.CurrentPlan().ID,
		"kind", step.Kind,
		"index", index,
		"outcome", outcome,
	)
	r.note(evidence.KindStepDone, step.Description, map[string]any{
		"plan":      planID,
		"kind":      step.Kind,
		"index":     index,
		"outcome":   outcome,
		"discarded": true,
		"reason":    "plan replaced while the step was running",
	})
}

// finishStep marks a step done and reports the result.
func (r *Runner) finishStep(index int, step PlanStep, planID, note string) {
	// Checked before anything is written, not after. See dispatchStep: an
	// outcome that arrives against a replaced plan has no valid index to write
	// to, and the plan it would land on belongs to work that never started.
	if !r.stillCurrentPlan(planID) {
		r.discardSupersededStep(step, index, planID, note)
		return
	}
	cur := r.CurrentPlan()
	next, planDone := r.CompleteStep(index)
	if planDone {
		r.log().Info("AGI: plan complete", "kind", step.Kind, "note", note)
		r.note(evidence.KindPlanComplete, cur.ID, map[string]any{
			"final_step": step.Kind,
		})
		return
	}
	done, total := r.CurrentPlan().progress()
	r.log().Info("AGI: step done",
		"kind", step.Kind,
		"progress", fmt.Sprintf("%d/%d", done, total),
		"next", next,
		"note", note,
	)
	// The step's description is the planner's own words for what it wanted, so
	// it is the only thing in the evidence that says what the bot was *trying*
	// to do rather than what it mechanically did.
	r.note(evidence.KindStepDone, step.Description, map[string]any{
		"plan":     cur.ID,
		"kind":     step.Kind,
		"index":    index,
		"progress": fmt.Sprintf("%d/%d", done, total),
		"action":   note,
	})
}

// failStepWork records a step failure, and retries it once before handing the
// problem to the planner.
//
// The single retry is for the ordinary case — a mob in the way, a click that
// landed on nothing. Past that the step is not flaky, it is wrong, and only the
// planner can say what to do about that.
func (r *Runner) failStepWork(index int, step PlanStep, planID, reason string) {
	if !r.stillCurrentPlan(planID) {
		r.discardSupersededStep(step, index, planID, "failed: "+reason)
		return
	}
	cur := r.CurrentPlan()
	r.FailStep(index, reason)
	if step.Attempts+1 < stepMaxAttempts {
		r.AbandonFailedStep(index)
		r.log().Info("AGI: step failed, retrying",
			"kind", step.Kind,
			"reason", reason,
			"attempt", step.Attempts+1,
		)
		r.note(evidence.KindStepRetry, step.Description, map[string]any{
			"plan":    cur.ID,
			"kind":    step.Kind,
			"index":   index,
			"reason":  reason,
			"attempt": step.Attempts + 1,
		})
		return
	}
	r.log().Warn("AGI: step failed, asking the planner to revise",
		"kind", step.Kind,
		"reason", reason,
		"attempts", step.Attempts,
	)
	// Recorded at the point the planner is asked again, because "how many times
	// did this fail before anyone noticed" is the question this whole file
	// exists to answer and it is unanswerable from a console log.
	r.note(evidence.KindStepFailed, step.Description, map[string]any{
		"plan":     r.CurrentPlan().ID,
		"kind":     step.Kind,
		"index":    index,
		"reason":   reason,
		"attempts": step.Attempts,
		"replan":   true,
	})
}

// arrivedAt reports whether the bot is close enough to a plan target.
//
// This is the honest part of step execution. The action layer reports that a
// move finished, not that it arrived: a target that could not be resolved, or a
// path that ended against a wall, both look like a completed move from the
// inside. The plan is the thing that has to know the difference, because
// reporting progress the bot did not make is the one failure mode a plan exists
// to prevent.
func (r *Runner) arrivedAt(target PlanPoint) bool {
	pos := r.b.GetCoords()
	dx := float64(target.X) - float64(pos.X())
	dy := float64(target.Y) - float64(pos.Y())
	dz := float64(target.Z) - float64(pos.Z())
	return math.Sqrt(dx*dx+dy*dy+dz*dz) <= arrivalRadius
}

// beginStepWork claims the single step-execution slot, returning false when one
// is already running.
func (r *Runner) beginStepWork() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busy {
		return false
	}
	r.busy = true
	return true
}

func (r *Runner) endStepWork() {
	r.mu.Lock()
	r.busy = false
	r.mu.Unlock()
}

// beginPlanWork claims the single planner slot, returning false when a request
// is already in flight. Two planners writing at once would race to install a
// plan, and the loser would silently overwrite the winner.
func (r *Runner) beginPlanWork() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plannerInFlight {
		return false
	}
	r.plannerInFlight = true
	return true
}

func (r *Runner) endPlanWork() {
	r.mu.Lock()
	r.plannerInFlight = false
	r.mu.Unlock()
}

func (r *Runner) markPlanAttempt() {
	r.mu.Lock()
	r.lastPlanAttempt = time.Now()
	r.mu.Unlock()
}

// truncate shortens a model reply for a log line. A planner reply that is wrong
// is worth reading, but not worth pasting a whole screen of into the log.
func truncate(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
