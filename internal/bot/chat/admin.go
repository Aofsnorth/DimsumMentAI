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

	switch act {
	case "say":
		if param != "" {
			b.SendSafeChat(param)
		}
	case "status":
		hp, hunger, coords := b.GetStatusDetails()
		b.ReportActionStatus(user, event.ActionStatus{Action: "status", Item: fmt.Sprintf("HP:%d Hunger:%d Coords:%s", hp, hunger, coords), Success: true})
	case "inv":
		b.ReportActionStatus(user, event.ActionStatus{Action: "inventory", Item: b.GetInventorySummary(), Success: true})
	case "follow":
		target := param
		if target == "" {
			target = user
		}
		b.FollowPlayer(target)
		b.ReportActionStatus(user, event.ActionStatus{Action: "follow", Item: target, Success: true})
	case "goto":
		action.Execute(b, "goto", param, user)
		b.ReportActionStatus(user, event.ActionStatus{Action: "goto", Item: param, Success: true})
	case "stop":
		b.Stop()
		if b.Planner != nil {
			b.Planner.Cancel()
		}
		b.ReportActionStatus(user, event.ActionStatus{Action: "stop", Success: true})
	case "todo":
		if b.Planner != nil && b.Planner.TodoIsActive() {
			summary := b.Planner.TodoRenderForChat()
			if summary != "" {
				b.ReportActionStatus(user, event.ActionStatus{Action: "todo", Item: summary, Success: true})
			} else {
				b.ReportActionStatus(user, event.ActionStatus{Action: "todo", Success: true, Error: "plan aktif tapi belum ada progress"})
			}
		} else {
			b.ReportActionStatus(user, event.ActionStatus{Action: "todo", Success: true, Error: "gak ada plan yang aktif"})
		}
	case "cancelplan":
		if b.Planner != nil && b.Planner.IsRunning() {
			b.Planner.Cancel()
			b.Planner.TodoClear()
			b.ReportActionStatus(user, event.ActionStatus{Action: "cancelplan", Success: true})
		} else {
			b.ReportActionStatus(user, event.ActionStatus{Action: "cancelplan", Success: true, Error: "gak ada plan yang aktif"})
		}
	default:
		b.Logger.Warn("Unknown admin command", "command", act)
	}
}
