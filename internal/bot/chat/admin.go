// Package chat handles player chat messages and AI-driven bot responses.
package chat

import (
	"fmt"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/bot/affordance"
	"bedrock-ai/internal/event"
)

// HandleAdminCommand executes special administrative actions prefixed with '!'
func HandleAdminCommand(b *bot.Bot, cmd string, user string) {
	cmd = strings.TrimPrefix(cmd, "!")
	parts := strings.SplitN(cmd, " ", 2)
	act := strings.ToLower(strings.TrimSpace(parts[0]))
	param := ""
	if len(parts) > 1 {
		param = strings.TrimSpace(parts[1])
	}

	b.Logger.Info("Admin command triggered", "action", act, "param", param)

	if handler, ok := adminCommandHandlers[act]; ok {
		handler(b, param, user)
		return
	}
	b.Logger.Warn("Unknown admin command", "command", act)
}

// adminCommandHandlers maps admin command names to their handlers.
var adminCommandHandlers = map[string]func(b *bot.Bot, param, user string){
	"say":        handleAdminSay,
	"status":     handleAdminStatus,
	"inv":        handleAdminInv,
	"follow":     handleAdminFollow,
	"goto":       handleAdminGoto,
	"move":       handleAdminMove,
	"look":       handleAdminLook,
	"analyze":    handleAdminAnalyze,
	"remember":   handleAdminRemember,
	"recall":     handleAdminRecall,
	"memories":   handleAdminRecall,
	"forget":     handleAdminForget,
	"sethome":    handleAdminSetHome,
	"home":       handleAdminHome,
	"stop":       handleAdminStop,
	"todo":       handleAdminTodo,
	"cancelplan": handleAdminCancelPlan,
	"cmd":        handleAdminCmd,
	"command":    handleAdminCmd,
	"verb":       handleAdminVerb,
}

// handleAdminCmd runs a server command from chat: "!cmd /register pass pass".
// The leading "!" is the bot's own prefix, so the command itself keeps the
// slash a player would type.
func handleAdminCmd(b *bot.Bot, param, user string) {
	action.Execute(b, "cmd", param, user)
}

func handleAdminSay(b *bot.Bot, param, user string) {
	if param != "" {
		b.SendSafeChat(param)
	}
}

func handleAdminStatus(b *bot.Bot, param, user string) {
	hp, hunger, coords := b.GetStatusDetails()
	b.ReportActionStatus(user, event.ActionStatus{Action: "status", Item: fmt.Sprintf("HP:%d Hunger:%d Coords:%s", hp, hunger, coords), Success: true})
}

func handleAdminInv(b *bot.Bot, param, user string) {
	b.ReportActionStatus(user, event.ActionStatus{Action: "inventory", Item: b.GetInventorySummary(), Success: true})
}

func handleAdminFollow(b *bot.Bot, param, user string) {
	target := param
	if target == "" {
		target = user
	}
	b.FollowPlayer(target)
	b.ReportActionStatus(user, event.ActionStatus{Action: "follow", Item: target, Success: true})
}

func handleAdminGoto(b *bot.Bot, param, user string) {
	// No status report here: the goto handler reports the real outcome when
	// navigation finishes. Reporting success now claims arrival while the bot
	// is still walking, and the chat line lands mid-journey after LLM latency.
	action.Execute(b, "goto", param, user)
}

// The MinePal-parity admin commands below delegate to the action dispatch so
// chat (!) and LLM (<action>) share one code path.
func handleAdminMove(b *bot.Bot, param, user string) {
	// Same as goto: the handler owns the arrival verdict.
	action.Execute(b, "move", param, user)
}

func handleAdminLook(b *bot.Bot, param, user string) {
	// The lookat handler reports its own outcome; an instant ack here would
	// claim the look before it happens.
	action.Execute(b, "look", param, user)
}

func handleAdminAnalyze(b *bot.Bot, param, user string) {
	action.Execute(b, "analyze", param, user)
}

func handleAdminRemember(b *bot.Bot, param, user string) {
	action.Execute(b, "remember", param, user)
}

func handleAdminRecall(b *bot.Bot, param, user string) {
	action.Execute(b, "recall", param, user)
}

func handleAdminForget(b *bot.Bot, param, user string) {
	action.Execute(b, "forget", param, user)
}

func handleAdminSetHome(b *bot.Bot, param, user string) {
	action.Execute(b, "sethome", param, user)
}

func handleAdminHome(b *bot.Bot, param, user string) {
	action.Execute(b, "home", param, user)
}

func handleAdminStop(b *bot.Bot, param, user string) {
	b.Stop()
	if b.Planner != nil {
		b.Planner.Cancel()
	}
	b.ReportActionStatus(user, event.ActionStatus{Action: "stop", Success: true})
}

func handleAdminTodo(b *bot.Bot, param, user string) {
	if b.Planner == nil || !b.Planner.TodoIsActive() {
		b.ReportActionStatus(user, event.ActionStatus{Action: "todo", Success: true, Error: "gak ada plan yang aktif"})
		return
	}
	summary := b.Planner.TodoRenderForChat()
	if summary == "" {
		b.ReportActionStatus(user, event.ActionStatus{Action: "todo", Success: true, Error: "plan aktif tapi belum ada progress"})
		return
	}
	b.ReportActionStatus(user, event.ActionStatus{Action: "todo", Item: summary, Success: true})
}

func handleAdminCancelPlan(b *bot.Bot, param, user string) {
	if b.Planner == nil || !b.Planner.IsRunning() {
		b.ReportActionStatus(user, event.ActionStatus{Action: "cancelplan", Success: true, Error: "gak ada plan yang aktif"})
		return
	}
	b.Planner.Cancel()
	b.Planner.TodoClear()
	b.ReportActionStatus(user, event.ActionStatus{Action: "cancelplan", Success: true})
}

// handleAdminVerb runs one verb of the affordance catalogue by name, through the
// same lookup, the same gate and the same registry dispatch the deciding model's
// own answer goes through.
//
// It exists so a run can cover the catalogue deliberately. The model picks a verb
// once per tick and takes whatever the situation invites, so ordinary play
// returns to a handful of the set and never reaches the rest — a live run
// recorded zero dispatches across several minutes of wandering — and "the
// affordance layer was exercised" would otherwise be an impression rather than a
// fact.
//
// The verdict is logged rather than only answered in chat, because the record is
// the point of the exercise: which verbs were named, which the gate held back and
// why, and which actually dispatched.
func handleAdminVerb(b *bot.Bot, param, user string) {
	label := strings.ToLower(strings.TrimSpace(param))
	if label == "" {
		b.ReportActionStatus(user, event.ActionStatus{Action: "verb", Success: false, Error: "pakai: !verb <label>"})
		return
	}
	v, ok := affordance.Lookup(label)
	if !ok {
		b.Logger.Warn("affordance: no such verb", "verb", label)
		b.ReportActionStatus(user, event.ActionStatus{Action: "verb", Success: false, Error: "verb gak dikenal: " + label})
		return
	}

	// The same three refusals the model's own answer gets, in the same order.
	// Skipping any of them would exercise a different path than the one being
	// claimed, which is exactly how a probe ends up proving less than it reports.
	if !v.Offerable() {
		b.Logger.Warn("affordance: verb held back by the gate",
			"verb", v.Label, "changesWorld", v.ChangesWorld,
			"confirmation", v.Confirmation.String())
		b.ReportActionStatus(user, event.ActionStatus{Action: "verb", Success: false,
			Error: v.Label + " mengubah dunia tapi gak bisa dibuktikan (" + v.Confirmation.String() + ")"})
		return
	}
	if v.Kind != affordance.Action {
		// An Activity is carried out by the brain and has no registry entry, so
		// there is nothing to dispatch. Reported as exercised all the same: the
		// Kind branch is one of the things a run has to cover.
		b.Logger.Info("affordance: verb exercised",
			"verb", v.Label, "confirmation", v.Confirmation.String(),
			"changesWorld", v.ChangesWorld, "dispatched", false,
			"reason", "activity: dijalankan otak sendiri, bukan registry")
		b.ReportActionStatus(user, event.ActionStatus{Action: "verb", Success: true, Item: v.Label + " (activity)"})
		return
	}

	// Mirrors doActivity exactly: the label carries its own argument, and both
	// halves go to the registry so a verb like "take:oak_log" is dispatched with
	// the thing the model was shown rather than without it.
	_, arg := affordance.SplitVerb(v.Label)
	b.Logger.Info("affordance: verb exercised",
		"verb", v.Label, "confirmation", v.Confirmation.String(),
		"changesWorld", v.ChangesWorld, "arg", arg, "dispatched", true)
	action.Execute(b, v.Label, arg, user)
}
