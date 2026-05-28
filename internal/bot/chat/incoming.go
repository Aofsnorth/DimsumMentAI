package chat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/event"
)

// HandleIncomingChat handles messages from players and queries Nvidia LLM if appropriate
func HandleIncomingChat(ctx context.Context, b *bot.Bot, evt event.ChatEvent) {
	msg := strings.TrimSpace(evt.Message)
	if msg == "" {
		b.Logger.Info("chat ignored: empty message")
		return
	}

	b.Mu.Lock()
	botName := b.Name
	b.Mu.Unlock()

	b.Logger.Info("chat event received",
		slog.String("source", evt.SourceName),
		slog.String("message", msg),
		slog.String("bot_name", botName),
	)

	// 1. Skip bot's own echoes
	if strings.EqualFold(evt.SourceName, botName) {
		b.Logger.Info("chat ignored: own message", slog.String("source", evt.SourceName))
		return
	}
	if b.IsBotEcho(msg) {
		b.Logger.Info("chat ignored: bot echo detected")
		return
	}

	// 2. Validate linked player whitelist
	if b.AiCfg.RespondOnlyToLinkedPlayer && b.AiCfg.MainPlayer != "" {
		if !strings.EqualFold(evt.SourceName, b.AiCfg.MainPlayer) {
			b.Logger.Info("chat ignored: not linked main player",
				slog.String("source", evt.SourceName),
				slog.String("main_player", b.AiCfg.MainPlayer),
			)
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
			b.Logger.Info("chat ignored: bot not tagged in message",
				slog.String("msg", msg),
				slog.String("bot_name", botName),
			)
			return
		}
	}

	// 3. Fallback distance check if MainPlayer is not set
	if b.AiCfg.MainPlayer == "" {
		pCoords, ok := b.GetPlayerCoords(evt.SourceName)
		if !ok {
			b.Logger.Info("chat ignored: player position unknown", slog.String("player", evt.SourceName))
			return
		}
		botCoords := b.GetCoords()
		dx := pCoords.X() - botCoords.X()
		dy := pCoords.Y() - botCoords.Y()
		dz := pCoords.Z() - botCoords.Z()
		dist := float32(mathSqrt(float64(dx*dx + dy*dy + dz*dz)))
		if dist > 10.0 {
			b.Logger.Info("chat ignored: player too far",
				slog.String("player", evt.SourceName),
				slog.Float64("distance", float64(dist)),
			)
			return
		}
	}

	// 4. Handle Debug/Admin commands prefixed with '!'
	if strings.HasPrefix(msg, "!") {
		HandleAdminCommand(b, msg, evt.SourceName)
		return
	}

	// 5. Query AI (NVIDIA NIM) if enabled
	if b.AiClient == nil {
		b.Logger.Info("chat ignored: AI client not configured")
		return
	}

	// Skip certain trigger-less chat phrases
	if strings.HasPrefix(strings.ToLower(msg), "!nofollow") {
		return
	}

	// 6. Deduplication filter immediately
	allowed, _ := b.Throttler.Filter(evt.SourceName, msg)
	if !allowed {
		b.Logger.Info("chat ignored: throttled or duplicate", slog.String("msg", msg))
		return
	}

	b.Logger.Info("chat processing: querying AI", slog.String("from", evt.SourceName))

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
		// Allow the same player to retry immediately rather than being held
		// off by the throttler for a failed attempt.
		b.Throttler.Rollback(evt.SourceName, msg)
		return
	}

	b.Logger.Debug("Nvidia LLM raw response received", "raw", reply)

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

	if isSilent && len(parsed.Actions) == 1 {
		handleSilentResponse(b, evt.SourceName, systemPrompt, parsed.Actions)
		return
	}

	// 10. Send the main reply
	if parsed.CleanReply != "" {
		b.Logger.Info("chat reply sending", slog.String("reply", parsed.CleanReply))
		b.SendSafeChat(parsed.CleanReply)
	} else {
		b.Logger.Info("chat: AI returned no visible reply text")
	}

	// 11. Dispatch action labels. Multiple tags are treated as a small plan.
	steps := make([]action.Step, 0, len(parsed.Actions))
	for _, act := range parsed.Actions {
		steps = append(steps, action.Step{Label: act.Label, Param: act.Param})
	}
	if len(steps) == 0 {
		steps = fallbackMovementActions(msg)
		if len(steps) > 0 {
			b.Logger.Info("chat inferred movement action from message", slog.Any("steps", steps))
		}
	}
	action.ExecutePlan(b, steps, evt.SourceName)
}

func handleSilentResponse(b *bot.Bot, sourceName string, systemPrompt string, actions []ai.Action) {
	for _, act := range actions {
		label := strings.ToLower(act.Label)
		if label == "status" {
			newHp, newHunger, newBotCoords := b.GetStatusDetails()
			newInvSummary := b.GetInventorySummary()
			followPrompt := fmt.Sprintf("[SYSTEM: Status bot — HP: %d/20, Hunger: %d/20, Posisi: %s. Inventory: %s. Laporkan status ini ke %s secara natural. JANGAN sertakan label [status] lagi di balasan ini.]", newHp, newHunger, newBotCoords, newInvSummary, sourceName)

			reply2, err := b.AiClient.Ask(sourceName, systemPrompt, followPrompt)
			if err == nil {
				parsed2 := ai.Parse(reply2)
				b.SendSafeChat(parsed2.CleanReply)
			}
		} else if label == "inventory" {
			newInvSummary := b.GetInventorySummary()
			followPrompt := fmt.Sprintf("[SYSTEM: Isi inventory saat ini: %s. Beritahu %s apa saja yang kamu punya. JANGAN sertakan label [inventory] lagi di balasan ini.]", newInvSummary, sourceName)

			reply2, err := b.AiClient.Ask(sourceName, systemPrompt, followPrompt)
			if err == nil {
				parsed2 := ai.Parse(reply2)
				b.SendSafeChat(parsed2.CleanReply)
			}
		}
	}
}
