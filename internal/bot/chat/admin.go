package chat

import (
	"fmt"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
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
		b.SendSafeChat(fmt.Sprintf("[Admin Status] HP: %d/20, Hunger: %d/20, Coords: %s", hp, hunger, coords))
	case "inv":
		b.SendSafeChat(fmt.Sprintf("[Admin Inventory] %s", b.GetInventorySummary()))
	case "follow":
		target := param
		if target == "" {
			target = user
		}
		b.FollowPlayer(target)
		b.SendSafeChat(fmt.Sprintf("Following %s", target))
	case "goto":
		action.Execute(b, "goto", param, user)
		b.SendSafeChat(fmt.Sprintf("Walking to %s", param))
	case "stop":
		b.Stop()
		b.SendSafeChat("Stopped all movements")
	default:
		b.Logger.Warn("Unknown admin command", "command", act)
	}
}
