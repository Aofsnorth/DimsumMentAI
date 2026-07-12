// Package chat handles player chat messages and AI-driven bot responses.
package chat

import (
	"fmt"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
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
	"stop":       handleAdminStop,
	"todo":       handleAdminTodo,
	"cancelplan": handleAdminCancelPlan,
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
	action.Execute(b, "goto", param, user)
	b.ReportActionStatus(user, event.ActionStatus{Action: "goto", Item: param, Success: true})
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
