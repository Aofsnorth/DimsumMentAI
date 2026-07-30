// Package chat handles player chat messages and AI-driven bot responses.
package chat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"
)

// HandleIncomingChat handles messages from players and queries the LLM when
// appropriate. It filters ignored messages, runs admin/plan commands, queries
// the AI, and dispatches the response.
func HandleIncomingChat(ctx context.Context, b *bot.Bot, evt event.ChatEvent) {
	msg := strings.TrimSpace(evt.Message)
	if msg == "" {
		b.Logger.Info("chat ignored: empty message")
		return
	}

	botName := b.Name
	b.Logger.Info("chat event received",
		slog.String("source", evt.SourceName),
		slog.String("message", msg),
		slog.String("bot_name", botName),
	)

	if !shouldProcessChat(b, evt, msg, botName) {
		return
	}

	b.Mu.Lock()
	b.LastChatPartner = evt.SourceName
	b.Mu.Unlock()

	if strings.HasPrefix(msg, "!") {
		HandleAdminCommand(b, msg, evt.SourceName)
		return
	}

	if strings.HasPrefix(strings.ToLower(msg), "plan:") {
		handlePlanCommand(b, evt, msg)
		return
	}

	if b.AiClient == nil {
		b.Logger.Info("chat ignored: AI client not configured")
		return
	}

	if strings.HasPrefix(strings.ToLower(msg), "!nofollow") {
		return
	}

	allowed, _ := b.Throttler.Filter(evt.SourceName, msg)
	if !allowed {
		b.Logger.Info("chat ignored: throttled or duplicate", slog.String("msg", msg))
		return
	}

	b.Logger.Info("chat processing: querying AI", slog.String("from", evt.SourceName))

	systemPrompt := buildChatContext(b, evt.SourceName, msg, botName)
	reply, err := b.AiClient.Ask(evt.SourceName, systemPrompt, msg)
	if err != nil {
		b.Logger.Error("Failed to ask Nvidia LLM", "error", err.Error())
		b.Throttler.Rollback(evt.SourceName, msg)
		return
	}

	b.Logger.Debug("Nvidia LLM raw response received", "raw", reply)
	dispatchChatResponse(b, evt.SourceName, msg, systemPrompt, reply)
}

// shouldProcessChat applies filters for bot echoes, player whitelist, tagging,
// and distance before a message is sent to the LLM.
func shouldProcessChat(b *bot.Bot, evt event.ChatEvent, msg, botName string) bool {
	if strings.EqualFold(evt.SourceName, botName) {
		b.Logger.Info("chat ignored: own message", slog.String("source", evt.SourceName))
		return false
	}
	if b.IsBotEcho(msg) {
		b.Logger.Info("chat ignored: bot echo detected")
		return false
	}

	if b.AiCfg.RespondOnlyToLinkedPlayer && b.AiCfg.MainPlayer != "" {
		if !strings.EqualFold(evt.SourceName, b.AiCfg.MainPlayer) {
			b.Logger.Info("chat ignored: not linked main player",
				slog.String("source", evt.SourceName),
				slog.String("main_player", b.AiCfg.MainPlayer),
			)
			return false
		}
	}

	if b.AiCfg.RespondOnlyWhenTagged {
		if !isTaggedInMessage(msg, botName) {
			b.Logger.Info("chat ignored: bot not tagged in message",
				slog.String("msg", msg),
				slog.String("bot_name", botName),
			)
			return false
		}
	}

	if b.AiCfg.MainPlayer == "" {
		pCoords, ok := b.GetPlayerCoords(evt.SourceName)
		if !ok {
			b.Logger.Info("chat ignored: player position unknown", slog.String("player", evt.SourceName))
			return false
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
			return false
		}
	}

	return true
}

// isTaggedInMessage reports whether the message explicitly mentions the bot.
func isTaggedInMessage(msg, botName string) bool {
	msgLower := strings.ToLower(msg)
	botNameLower := strings.ToLower(botName)
	return strings.Contains(msgLower, botNameLower) || strings.Contains(msgLower, "@"+botNameLower)
}

// handlePlanCommand handles explicit "plan:" prefix messages.
func handlePlanCommand(b *bot.Bot, evt event.ChatEvent, msg string) {
	request := strings.TrimSpace(msg[5:])
	if request == "" {
		return
	}
	if b.Planner == nil {
		return
	}
	b.Logger.Info("plan: command triggered", "user", evt.SourceName, "request", request)
	b.ReportActionStatus(evt.SourceName, event.ActionStatus{Action: "plan", Success: true})
	b.Planner.RunFromChat(evt.SourceName, request)
}

// buildChatContext synthesizes the system prompt used for a chat LLM query.
func buildChatContext(b *bot.Bot, sourceName, msg, botName string) string {
	hp, hunger, botCoords := b.GetStatusDetails()
	heldItem := b.GetHeldItem()
	invSummary := b.GetInventorySummary()
	playerCoordsStr := ""
	if pCoords, ok := b.GetPlayerCoords(sourceName); ok {
		playerCoordsStr = fmt.Sprintf("X:%.0f Y:%.0f Z:%.0f", pCoords.X(), pCoords.Y(), pCoords.Z())
	}

	visibleMobs := VisibleMobsSummary(b, 32, 8)
	botStatusText := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)
	systemPrompt := b.AiClient.BuildSystemPrompt(
		botName,
		botCoords+" ("+botStatusText+")",
		playerCoordsStr,
		heldItem,
		invSummary,
	)

	systemPrompt += "\n\n[GROUNDED PERCEPTION] Visible mobs (line-of-sight, non-item): " + visibleMobs + "."

	// Append current plan/todo state so the LLM is always aware of any
	// in-progress multi-step task, even when a new chat message arrives
	// mid-plan.
	if b.Planner != nil {
		todoStr := b.Planner.TodoRenderForPrompt()
		if todoStr != "" {
			systemPrompt += "\n\n" + todoStr
		}
	}

	return systemPrompt
}

// VisibleMobsSummary lists line-of-sight-visible non-item mobs nearest first,
// or explicitly "none" when the bot cannot see any. Distances are relative to
// the bot.
func VisibleMobsSummary(b *bot.Bot, maxDistance float32, limit int) string {
	b.Mu.Lock()
	origin := b.Pos
	actors := make(map[uint64]*entity.Info, len(b.Actors))
	for id, info := range b.Actors {
		if info == nil {
			continue
		}
		copied := *info
		actors[id] = &copied
	}
	b.Mu.Unlock()

	visible := entity.VisibleMobs(b.WorldModel, b, origin, actors, maxDistance, nil)
	if len(visible) == 0 {
		return "none"
	}
	parts := make([]string, 0, min(limit, len(visible)))
	for _, info := range visible {
		name := entity.NormalizeName(info.Name)
		if name == "" || name != entity.NormalizeName(info.Type) {
			if typ := entity.NormalizeName(info.Type); typ != "" {
				name = typ
			}
		}
		parts = append(parts, fmt.Sprintf("%s (%.0fm)", name, origin.Sub(info.Position).Len()))
		if len(parts) >= limit {
			break
		}
	}
	return strings.Join(parts, ", ")
}
