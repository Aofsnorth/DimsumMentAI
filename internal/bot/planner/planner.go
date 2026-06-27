package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/event"
)

// Planner runs an agentic plan-execute-observe loop:
//
//  1. The LLM (or a player's "plan:" command) produces a multi-step plan.
//  2. The planner stores it as a TodoList and executes steps one at a time.
//  3. After each step, the planner re-queries the LLM with the result +
//     current todo state + bot inventory, letting the model decide whether
//     to continue, adjust the remaining plan, chat to the player, or stop.
//
// This mirrors how a real agent (e.g. Devin) works: plan → tool → observe →
// decide → tool → observe → ... rather than blindly executing a fixed list.
type Planner struct {
	bot     *bot.Bot
	client  *ai.NvidiaClient
	todo    *TodoList
	user    string // the player who initiated the current plan
	mu      sync.Mutex
	cancel  chan struct{}
	running bool
}

func New(b *bot.Bot, client *ai.NvidiaClient) *Planner {
	return &Planner{
		bot:    b,
		client: client,
		todo:   NewTodoList(),
	}
}

// Todo returns the shared todo list so the bot can surface it in the system
// prompt and chat commands.
func (p *Planner) Todo() *TodoList {
	return p.todo
}

// IsRunning reports whether the agentic loop is currently active.
func (p *Planner) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// TodoRenderForPrompt returns the todo list formatted for the LLM system
// prompt, or empty string if no plan is active. Implements bot.PlannerInterface.
func (p *Planner) TodoRenderForPrompt() string {
	return p.todo.RenderForPrompt()
}

// TodoRenderForChat returns the todo list formatted for player-facing chat.
// Implements bot.PlannerInterface.
func (p *Planner) TodoRenderForChat() string {
	return p.todo.RenderForChat()
}

// TodoIsActive reports whether a plan is currently tracked. Implements
// bot.PlannerInterface.
func (p *Planner) TodoIsActive() bool {
	return p.todo.IsActive()
}

// TodoClear resets the todo list. Implements bot.PlannerInterface.
func (p *Planner) TodoClear() {
	p.todo.Clear()
}

// Cancel stops the agentic loop after the current step finishes. The todo
// list is marked as skipped for remaining steps.
func (p *Planner) Cancel() {
	p.mu.Lock()
	if p.cancel != nil {
		select {
		case <-p.cancel:
			// already closed
		default:
			close(p.cancel)
		}
	}
	p.mu.Unlock()
}

// Run starts the agentic loop for a plan in a background goroutine. The
// goal and actions are stored in the todo list before execution begins.
// If a plan is already running, it is cancelled first.
func (p *Planner) Run(goal, user string, actions []string) {
	if len(actions) == 0 {
		return
	}

	// Cancel any existing plan.
	p.Cancel()
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		time.Sleep(300 * time.Millisecond) // give the old loop a moment to wind down
		p.mu.Lock()
	}
	p.cancel = make(chan struct{})
	p.running = true
	p.user = user
	p.mu.Unlock()

	p.todo.SetPlan(goal, actions)

	go p.loop()
}

// RunFromChat is a convenience for the "plan:" chat command. It asks the LLM
// to produce a plan for the given request, then runs it.
func (p *Planner) RunFromChat(user, request string) {
	if p.client == nil {
		return
	}

	go func() {
		// Ask the LLM to produce a structured plan.
		systemPrompt := p.buildPlannerSystemPrompt(user, "")
		plannerPrompt := fmt.Sprintf(
			`The player <%s> wants you to do the following: "%s"

Break this down into a step-by-step plan. Each step must be a single action you can perform.
Output ONLY a <plan> block with <step> tags. Each step is "label:param" (same as <action> tags).
Do NOT include any chat text — just the plan.

Example:
<plan>
<step>gather:oak_log,4</step>
<step>craft:oak_planks,16</step>
<step>craft:crafting_table,1</step>
</plan>`, user, request)

		reply, err := p.client.AskPlanner(systemPrompt, plannerPrompt)
		if err != nil {
			p.bot.Logger.Warn("planner: LLM plan request failed", "error", err)
			p.bot.ReportActionStatus(user, event.ActionStatus{Action: "plan", Success: false, Error: "gak bisa bikin plan sekarang, coba lagi"})
			return
		}

		planSteps := ai.ParsePlanSteps(reply)
		if len(planSteps) == 0 {
			p.bot.Logger.Warn("planner: LLM returned no plan steps", "reply", reply)
			p.bot.ReportActionStatus(user, event.ActionStatus{Action: "plan", Success: false, Error: "bingung harus ngapain dulu, minta lebih spesifik"})
			return
		}

		p.bot.Logger.Info("planner: starting plan from chat", "goal", request, "steps", len(planSteps), "user", user)
		p.Run(request, user, planSteps)
	}()
}

// loop is the core agentic plan → execute → observe → decide cycle.
func (p *Planner) loop() {
	defer func() {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}()

	cancel := p.getCancelChan()

	for {
		// Check cancellation.
		select {
		case <-cancel:
			p.bot.Logger.Info("planner: cancelled by user")
			p.markRemainingSkipped()
			return
		default:
		}

		// Get next pending step.
		step, ok := p.todo.NextPending()
		if !ok {
			p.bot.Logger.Info("planner: all steps completed")
			p.finishPlan()
			return
		}

		// Parse action label:param.
		label, param := splitAction(step.Action)

		p.bot.Logger.Info("planner: executing step",
			"index", step.Index, "action", step.Action, "label", label)

		// Execute the action synchronously.
		action.ExecuteAndWait(p.bot, label, param, p.user)

		// Check cancellation after action.
		select {
		case <-cancel:
			p.todo.MarkFailed(step.Index, "cancelled")
			p.bot.Logger.Info("planner: cancelled mid-step")
			p.markRemainingSkipped()
			return
		default:
		}

		// --- Re-evaluate with LLM (the "tool loop") -----------------------
		feedback := p.buildFeedback(step)
		systemPrompt := p.buildPlannerSystemPrompt(p.user, feedback)

		evalPrompt := fmt.Sprintf(
			`[PLANNER FEEDBACK] Step %d "%s" just completed.
%s
Review the result. You can:
1. Reply with <continue/> to proceed to the next step.
2. Reply with <replan>...</replan> containing new <step> tags to replace remaining steps.
3. Reply with <done/> if the goal is already achieved or should be abandoned.
4. Include chat text BEFORE any tag if you want to say something to the player.
5. Include <action>label:param</action> if you need an extra one-off action before continuing.

Keep any chat text SHORT (1 sentence, casual).`,
			step.Index+1, step.Action, feedback)

		reply, err := p.client.AskPlanner(systemPrompt, evalPrompt)
		if err != nil {
			p.bot.Logger.Warn("planner: LLM re-evaluation failed, continuing sequentially", "error", err)
			p.todo.MarkCompleted(step.Index, "executed (no LLM eval)")
			continue
		}

		p.handleEvalReply(reply, step)
	}
}

// handleEvalReply processes the LLM's re-evaluation response.
func (p *Planner) handleEvalReply(reply string, step TodoItem) {
	parsed := ai.Parse(reply)

	// Send any chat text to the player.
	if parsed.CleanReply != "" {
		p.bot.SendSafeChat(parsed.CleanReply)
	}

	// Check for explicit done/continue/replan markers in the raw reply.
	rawLower := strings.ToLower(reply)

	if strings.Contains(rawLower, "<done") {
		p.todo.MarkCompleted(step.Index, "done by LLM")
		p.bot.Logger.Info("planner: LLM signalled done")
		p.markRemainingSkipped()
		return
	}

	// Check for replan.
	replanSteps := ai.ParsePlanSteps(reply)
	if len(replanSteps) > 0 && strings.Contains(reply, "<replan") {
		p.todo.MarkCompleted(step.Index, "executed, replanned")
		p.todo.ReplaceRemaining(replanSteps)
		p.bot.Logger.Info("planner: LLM replanned", "new_steps", len(replanSteps))
		return
	}

	// Execute any extra one-off actions the LLM emitted.
	for _, act := range parsed.Actions {
		p.bot.Logger.Info("planner: executing extra action from eval", "action", act.Label+":"+act.Param)
		action.ExecuteAndWait(p.bot, act.Label, act.Param, p.user)
	}

	if strings.Contains(rawLower, "<continue") || len(parsed.Actions) > 0 {
		p.todo.MarkCompleted(step.Index, "executed")
		return
	}

	// Default: mark completed and continue.
	p.todo.MarkCompleted(step.Index, "executed")
}

// finishPlan is called when all steps are done. It optionally asks the LLM
// for a closing message to send to the player.
func (p *Planner) finishPlan() {
	completed, total := p.todo.Progress()
	p.bot.Logger.Info("planner: plan finished", "completed", completed, "total", total)

	if p.client == nil {
		p.todo.Clear()
		return
	}

	// Ask LLM for a natural closing message.
	systemPrompt := p.buildPlannerSystemPrompt(p.user, "")
	closingPrompt := fmt.Sprintf(
		`[PLANNER] The plan is complete. Goal: "%s"
Progress: %d/%d steps completed.
Say something SHORT and natural to the player <%s> about finishing the task. No action tags needed.`,
		p.todo.Goal(), completed, total, p.user)

	reply, err := p.client.AskPlanner(systemPrompt, closingPrompt)
	if err == nil {
		parsed := ai.Parse(reply)
		if parsed.CleanReply != "" {
			p.bot.SendSafeChat(parsed.CleanReply)
		}
	}

	p.todo.Clear()
}

func (p *Planner) markRemainingSkipped() {
	// The todo list doesn't have a direct "skip all" method, so we mark
	// each pending item as skipped by replacing remaining with empty.
	p.todo.ReplaceRemaining(nil)
}

// buildFeedback gathers the bot's current state for the LLM re-evaluation.
func (p *Planner) buildFeedback(step TodoItem) string {
	hp, hunger, coords := p.bot.GetStatusDetails()
	inv := p.bot.GetInventorySummary()
	held := p.bot.GetHeldItem()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Bot HP: %d/20, Hunger: %d/20\n", hp, hunger))
	b.WriteString(fmt.Sprintf("Bot Position: %s\n", coords))
	if held != "" {
		b.WriteString("Holding: " + held + "\n")
	}
	if inv != "" {
		b.WriteString("Inventory: " + inv + "\n")
	}
	b.WriteString("\n")
	b.WriteString(p.todo.RenderForPrompt())
	return b.String()
}

// buildPlannerSystemPrompt constructs the system prompt for planner LLM calls.
// It includes the bot persona, rules, current todo state, and optional feedback.
func (p *Planner) buildPlannerSystemPrompt(user, feedback string) string {
	hp, hunger, coords := p.bot.GetStatusDetails()
	inv := p.bot.GetInventorySummary()
	held := p.bot.GetHeldItem()

	playerCoordsStr := ""
	if pCoords, ok := p.bot.GetPlayerCoords(user); ok {
		playerCoordsStr = fmt.Sprintf("X:%.0f Y:%.0f Z:%.0f", pCoords.X(), pCoords.Y(), pCoords.Z())
	}

	botName := p.bot.Name
	botStatusText := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)

	prompt := p.client.BuildSystemPrompt(
		botName,
		coords+" ("+botStatusText+")",
		playerCoordsStr,
		held,
		inv,
	)

	// Append current plan state.
	todoStr := p.todo.RenderForPrompt()
	if todoStr != "" {
		prompt += "\n\n" + todoStr
	}

	// Append planner-specific instructions.
	prompt += "\n\n[PLANNER MODE] You are currently executing a multi-step plan. After each step you receive feedback. Use <continue/>, <replan>...</replan>, <done/>, or chat text to control the flow."

	if feedback != "" {
		prompt += "\n\n" + feedback
	}

	return prompt
}

func (p *Planner) getCancelChan() chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cancel
}

// splitAction parses "label:param" into (label, param).
func splitAction(action string) (string, string) {
	parts := strings.SplitN(action, ":", 2)
	label := strings.TrimSpace(parts[0])
	param := ""
	if len(parts) > 1 {
		param = strings.TrimSpace(parts[1])
	}
	return label, param
}
