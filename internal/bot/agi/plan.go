// The plan: a long-horizon objective broken into steps the brain can act on.
//
// This is the substrate that makes planning mode general. The default brain
// answers "what now?" every tick and that is a companion. Planning mode answers
// "what is this whole thing for?" and works toward it — but the objective is
// data. Nothing here knows about dragons, Nether travel, or storage rooms. The
// machinery is the same whatever the goal happens to be, and hardcoding a
// target into the brain is what turns an agent back into a script.
//
// The split mirrors a proven design: a slow, generative planner (the LLM) sets
// the objective and the shape of the work, and a fast, cheap decision model
// (Jev) picks one bounded action per tick toward the current step. Neither
// does the other's job. The planner is good at sequencing and bad at
// millisecond reactions; Jev is the reverse.

package agi

import (
	"fmt"
	"strings"
	"time"
)

// Plan is a long-horizon objective with ordered steps.
type Plan struct {
	// ID names the plan. It is generated rather than authored so a replan is
	// distinguishable from the plan it replaced in the log.
	ID string
	// Objective is one sentence: what finishing this plan would look like. It is
	// what the planner reasons about and what gets reported.
	Objective string
	// Steps are the units of work, in order.
	Steps []PlanStep
	// CreatedAt and Expires bound the plan's life. A plan is re-examined when it
	// runs out, for the same reason goals are: a bot that pursues a stale
	// objective forever is stuck, not persistent.
	CreatedAt time.Time
	Expires   time.Time
	// ReplanAfter is how long the planner waits before being consulted again
	// about this plan. It exists so a slow planner is consulted on a schedule
	// rather than stalling the brain, which is what "async planner" means in
	// practice.
	ReplanAfter time.Duration
	// LastPlanned is when the planner last spoke about this plan.
	LastPlanned time.Time
	// Notes is free-form context the planner left for itself.
	Notes string
}

// StepState is where a step is in its life.
type StepState int

const (
	// StepPending means the step has not been started.
	StepPending StepState = iota
	// StepActive means the step is the current one.
	StepActive
	// StepDone means the step was completed.
	StepDone
	// StepFailed means the step was given up on. A failed step does not advance
	// the plan; the planner is consulted instead, which is the difference
	// between a plan and a queue.
	StepFailed
)

// PlanStep is one unit of work.
type PlanStep struct {
	// Description is what the step is, in a sentence. The planner writes it, and
	// the brain reports progress in those terms.
	Description string
	// Kind is the step's category, which is what makes the plan general: the
	// brain dispatches on kind and the planner chooses them freely.
	Kind string
	// Target is where the step points, when it points somewhere. Zero means
	// "here", which is a valid waypoint for a step that is about the world the
	// bot is already standing in.
	Target PlanPoint
	// Need lists items the step wants. It is a wish list, not a reservation:
	// steps that need nothing are perfectly normal.
	Need []string
	// State is where the step is in its life.
	State StepState
	// Attempts counts how many times the step has been tried. A step that has
	// failed repeatedly should be abandoned rather than retried forever, and
	// this is what tells the planner it is not working.
	Attempts int
	// Note is why a step failed, kept so the planner can be told what went wrong
	// rather than having to rediscover it.
	Note string
}

// Step kinds. They are the vocabulary the planner writes in, and the only thing
// the brain dispatches on. Adding a kind is how a new capability becomes
// plannable; nothing here is specific to any one objective.
const (
	StepGather  = "gather"  // collect a resource
	StepMine    = "mine"    // extract a specific block
	StepGoTo    = "go_to"   // travel to a point
	StepCraft   = "craft"   // make something
	StepEquip   = "equip"   // put on armour or a tool
	StepCombat  = "combat"  // deal with a threat
	StepStorage = "storage" // put something away or take it out
	StepShelter = "shelter" // get to safety
	StepRest    = "rest"    // recover
	StepObserve = "observe" // look and report
	StepEnter   = "enter"   // go through a portal
	StepWait    = "wait"    // let time pass
)

// PlanPoint is a world position, kept separate from the vector type so a plan
// is plain data that can be serialised and logged.
type PlanPoint struct {
	X int32
	Y int32
	Z int32
	// Set distinguishes "no target" from "target at the origin", which matters
	// because the origin is a perfectly valid place to walk to.
	Set bool
}

// HasPlan reports whether there is a plan to work on.
func (r *Runner) HasPlan() bool {
	return r.currentPlan().Objective != ""
}

// currentPlan returns the active plan, or the zero Plan.
func (r *Runner) currentPlan() Plan {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.plan
}

// setPlan installs a plan, replacing any previous one.
func (r *Runner) setPlan(plan Plan) {
	r.mu.Lock()
	r.plan = plan
	r.mu.Unlock()
}

// clearPlan drops the plan.
func (r *Runner) clearPlan() {
	r.mu.Lock()
	r.plan = Plan{}
	r.mu.Unlock()
}

// CurrentStep returns the step the brain should act on: the first active step,
// or the first pending one so a plan that was never started still has a first
// thing to do.
func (r *Runner) CurrentStep() (PlanStep, int, bool) {
	plan := r.currentPlan()
	if plan.Objective == "" || len(plan.Steps) == 0 {
		return PlanStep{}, 0, false
	}
	// An active step wins over a pending one, so an in-progress step is not
	// abandoned just because an earlier step was never marked done.
	for i, step := range plan.Steps {
		if step.State == StepActive {
			return step, i, true
		}
	}
	for i, step := range plan.Steps {
		if step.State == StepPending {
			return step, i, true
		}
	}
	return PlanStep{}, 0, false
}

// StartStep marks a step active. The first step is started automatically when
// the plan is adopted, so a plan is never sitting there with nothing chosen.
func (r *Runner) StartStep(index int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index < 0 || index >= len(r.plan.Steps) {
		return
	}
	r.plan.Steps[index].State = StepActive
	r.plan.Steps[index].Note = ""
}

// CompleteStep marks a step done and activates the next one, returning the index
// that is now active.
//
// Completing the last step finishes the plan: the objective is marked achieved
// and the plan is cleared, because a finished plan that stays around makes the
// bot keep re-reading a plan with nothing left to do.
func (r *Runner) CompleteStep(index int) (next int, planDone bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index < 0 || index >= len(r.plan.Steps) {
		return 0, false
	}
	r.plan.Steps[index].State = StepDone
	r.plan.Steps[index].Note = ""

	for i := range r.plan.Steps {
		if r.plan.Steps[i].State == StepPending {
			r.plan.Steps[i].State = StepActive
			return i, false
		}
	}
	return 0, true
}

// FailStep records that a step could not be done, along with why.
//
// Failing does not advance the plan. A step that keeps failing means the plan
// is wrong, and quietly skipping to the next one is how an agent ends up
// claiming to have achieved an objective it quietly routed around.
func (r *Runner) FailStep(index int, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index < 0 || index >= len(r.plan.Steps) {
		return
	}
	r.plan.Steps[index].State = StepFailed
	r.plan.Steps[index].Attempts++
	r.plan.Steps[index].Note = reason
}

// AbandonFailedStep returns a failed step to pending so the planner can try a
// different approach to it.
func (r *Runner) AbandonFailedStep(index int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index < 0 || index >= len(r.plan.Steps) {
		return
	}
	if r.plan.Steps[index].State != StepFailed {
		return
	}
	r.plan.Steps[index].State = StepPending
	r.plan.Steps[index].Note = ""
	// It becomes the active step again, because the thing that failed is
	// usually still the thing that needs doing.
	for i := range r.plan.Steps {
		if r.plan.Steps[i].State == StepActive {
			r.plan.Steps[i].State = StepPending
		}
	}
	r.plan.Steps[index].State = StepActive
}

// planExpired reports whether the plan is due for re-examination.
func (p Plan) planExpired(now time.Time) bool {
	return !p.Expires.IsZero() && now.After(p.Expires)
}

// progress counts steps, for logging and for the planner's context.
func (p Plan) progress() (done, total int) {
	for _, step := range p.Steps {
		if step.State == StepDone {
			done++
		}
	}
	return done, len(p.Steps)
}

// renderPlan is the plan as text for the planner and for logs.
//
// It is deliberately compact and numbered. A planner reasoning about a long
// unstructured blob makes worse plans than one reasoning about "step 2 of 5,
// currently: mine obsidian", and a person reading the log needs the same
// shape to make sense of it.
func renderPlan(p Plan) string {
	if p.Objective == "" {
		return "no plan"
	}
	done, total := p.progress()
	var sb strings.Builder
	fmt.Fprintf(&sb, "Objective: %s (steps %d/%d done)\n", p.Objective, done, total)
	for i, step := range p.Steps {
		marker := " "
		switch step.State {
		case StepActive:
			marker = ">"
		case StepDone:
			marker = "x"
		case StepFailed:
			marker = "!"
		}
		line := fmt.Sprintf("  %s %d. [%s] %s", marker, i+1, step.Kind, step.Description)
		if step.Target.Set {
			line += fmt.Sprintf(" @ %d,%d,%d", step.Target.X, step.Target.Y, step.Target.Z)
		}
		if len(step.Need) > 0 {
			line += " (needs " + strings.Join(step.Need, ", ") + ")"
		}
		if step.Note != "" {
			line += " [failed: " + step.Note + "]"
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return sb.String()
}

// planDueForReplanning reports whether the planner should be consulted again.
func (p Plan) planDueForReplanning(now time.Time) bool {
	if p.Objective == "" {
		return false
	}
	if p.planExpired(now) {
		return true
	}
	if p.ReplanAfter <= 0 {
		return false
	}
	return now.Sub(p.LastPlanned) >= p.ReplanAfter
}
