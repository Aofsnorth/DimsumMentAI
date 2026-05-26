package bot

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/event"
)

// InitChatListener registers the bot's listener on the event bus for ChatEvents
func (b *Bot) InitChatListener(ctx context.Context) {
	b.Bus.Subscribe(reflect.TypeOf(event.ChatEvent{}), func(evt interface{}) {
		chatEvt, ok := evt.(event.ChatEvent)
		if !ok {
			return
		}
		go b.handleIncomingChat(ctx, chatEvt)
	})
	b.Logger.Info("Chat listener successfully registered on event bus")
}

func (b *Bot) handleIncomingChat(ctx context.Context, evt event.ChatEvent) {
	msg := strings.TrimSpace(evt.Message)
	if msg == "" {
		return
	}

	b.Mu.Lock()
	botName := b.Name
	b.Mu.Unlock()

	// 1. Skip bot's own echoes
	if strings.EqualFold(evt.SourceName, botName) {
		return
	}
	if b.IsBotEcho(msg) {
		return
	}

	// 2. Validate linked player whitelist
	if b.AiCfg.RespondOnlyToLinkedPlayer && b.AiCfg.MainPlayer != "" {
		if !strings.EqualFold(evt.SourceName, b.AiCfg.MainPlayer) {
			b.Logger.Debug("Ignoring message from unlinked player", "source", evt.SourceName, "linked", b.AiCfg.MainPlayer)
			return
		}
	}

	// 2b. Validate if the message tags the bot when respond_only_when_tagged is enabled
	if b.AiCfg.RespondOnlyWhenTagged {
		tagged := false
		botNameLower := strings.ToLower(botName)
		msgLower := strings.ToLower(msg)
		if strings.Contains(msgLower, botNameLower) || strings.Contains(msgLower, "@"+botNameLower) {
			tagged = true
		}
		if !tagged {
			b.Logger.Debug("Ignoring message: bot not tagged", "msg", msg, "botName", botName)
			return
		}
	}

	// 3. Fallback distance check if MainPlayer is not set
	if b.AiCfg.MainPlayer == "" {
		pCoords, ok := b.GetPlayerCoords(evt.SourceName)
		if !ok {
			b.Logger.Debug("Ignoring player (no coordinates tracked)", "player", evt.SourceName)
			return
		}
		botCoords := b.GetCoords()
		dx := pCoords.X() - botCoords.X()
		dy := pCoords.Y() - botCoords.Y()
		dz := pCoords.Z() - botCoords.Z()
		dist := float32(mathSqrt(float64(dx*dx + dy*dy + dz*dz)))
		if dist > 10.0 {
			b.Logger.Debug("Ignoring player (too far)", "player", evt.SourceName, "distance", dist)
			return
		}
	}

	// 4. Handle Debug/Admin commands prefixed with '!'
	if strings.HasPrefix(msg, "!") {
		b.handleAdminCommand(msg, evt.SourceName)
		return
	}

	// 5. Query AI (NVIDIA NIM) if enabled
	if b.AiClient == nil {
		return
	}

	// Skip certain trigger-less chat phrases
	if strings.HasPrefix(strings.ToLower(msg), "!nofollow") {
		return
	}

	// 6. Deduplication filter immediately (addresses the duplicate message race bug)
	allowed, _ := b.Throttler.Filter(msg)
	if !allowed {
		b.Logger.Info("Throttled duplicate or rate-limited message", "msg", msg)
		return
	}

	// 7. Synthesize real-time context
	hp, hunger, botCoords := b.GetStatusDetails()
	heldItem := b.GetHeldItem()
	invSummary := b.GetInventorySummary()
	playerCoordsStr := ""
	if pCoords, ok := b.GetPlayerCoords(evt.SourceName); ok {
		playerCoordsStr = fmt.Sprintf("X:%.0f Y:%.0f Z:%.0f", pCoords.X(), pCoords.Y(), pCoords.Z())
	}

	botStatusText := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)
	systemPrompt := b.AiClient.BuildSystemPrompt(
		botName,
		botCoords+" ("+botStatusText+")",
		playerCoordsStr,
		heldItem,
		invSummary,
	)

	// 8. Call Nvidia Client
	reply, err := b.AiClient.Ask(evt.SourceName, systemPrompt, msg)
	if err != nil {
		b.Logger.Error("Failed to ask Nvidia LLM", "error", err.Error())
		return
	}

	b.Logger.Info("Nvidia LLM raw response received", "raw", reply)

	// 9. Parse actions and clean reply
	parsed := ai.Parse(reply)

	// Check if this is a silent action (status or inventory)
	isSilent := false
	for _, act := range parsed.Actions {
		label := strings.ToLower(act.Label)
		if label == "status" || label == "inventory" {
			isSilent = true
			break
		}
	}

	if isSilent {
		// Suppress first response, execute silent tasks, and query follow-up system event
		for _, act := range parsed.Actions {
			label := strings.ToLower(act.Label)
			if label == "status" {
				// Status follow-up
				newHp, newHunger, newBotCoords := b.GetStatusDetails()
				newInvSummary := b.GetInventorySummary()
				followPrompt := fmt.Sprintf("[SYSTEM: Status bot — HP: %d/20, Hunger: %d/20, Posisi: %s. Inventory: %s. Laporkan status ini ke %s secara natural. JANGAN sertakan label [status] lagi di balasan ini.]", newHp, newHunger, newBotCoords, newInvSummary, evt.SourceName)
				
				reply2, err := b.AiClient.Ask(evt.SourceName, systemPrompt, followPrompt)
				if err == nil {
					parsed2 := ai.Parse(reply2)
					b.SendSafeChat(parsed2.CleanReply)
				}
			} else if label == "inventory" {
				// Inventory follow-up
				newInvSummary := b.GetInventorySummary()
				followPrompt := fmt.Sprintf("[SYSTEM: Isi inventory saat ini: %s. Beritahu %s apa saja yang kamu punya. JANGAN sertakan label [inventory] lagi di balasan ini.]", newInvSummary, evt.SourceName)
				
				reply2, err := b.AiClient.Ask(evt.SourceName, systemPrompt, followPrompt)
				if err == nil {
					parsed2 := ai.Parse(reply2)
					b.SendSafeChat(parsed2.CleanReply)
				}
			}
		}
		return
	}

	// 10. Send the main reply
	if parsed.CleanReply != "" {
		b.SendSafeChat(parsed.CleanReply)
	}

	// 11. Dispatch action labels
	for _, act := range parsed.Actions {
		b.ExecuteAction(act.Label, act.Param, evt.SourceName)
	}
}

func (b *Bot) handleAdminCommand(cmd string, user string) {
	cmd = strings.TrimPrefix(cmd, "!")
	parts := strings.SplitN(cmd, " ", 2)
	action := strings.ToLower(strings.TrimSpace(parts[0]))
	param := ""
	if len(parts) > 1 {
		param = strings.TrimSpace(parts[1])
	}

	b.Logger.Info("Admin command triggered", "action", action, "param", param)

	switch action {
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
		b.ExecuteAction("goto", param, user)
		b.SendSafeChat(fmt.Sprintf("Walking to %s", param))
	case "stop":
		b.Stop()
		b.SendSafeChat("Stopped all movements")
	default:
		b.Logger.Warn("Unknown admin command", "command", action)
	}
}

// Simple internal math helper for floats
func mathSqrt(v float64) float64 {
	// Reimplement sqrt to avoid math import issues if they arise
	// Newton-Raphson method or standard math library.
	// Since we import "math" in bot.go, we can use a basic helper.
	// We'll just define it or import math. Since this is a new file, we can import math package.
	return basicSqrt(v)
}

func basicSqrt(v float64) float64 {
	// basic square root implementation
	if v == 0 {
		return 0
	}
	z := 1.0
	for i := 0; i < 10; i++ {
		z -= (z*z - v) / (2 * z)
	}
	return z
}
