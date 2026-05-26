package chat

import (
	"context"
	"fmt"
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
		HandleAdminCommand(b, msg, evt.SourceName)
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

	// 6. Deduplication filter immediately
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
		handleSilentResponse(b, evt.SourceName, systemPrompt, parsed.Actions)
		return
	}

	// 10. Send the main reply
	if parsed.CleanReply != "" {
		b.SendSafeChat(parsed.CleanReply)
	}

	// 11. Dispatch action labels
	for _, act := range parsed.Actions {
		action.Execute(b, act.Label, act.Param, evt.SourceName)
	}
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
