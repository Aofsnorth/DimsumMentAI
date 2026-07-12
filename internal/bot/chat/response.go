// Package chat handles player chat messages and AI-driven bot responses.
package chat

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
)

// dispatchChatResponse parses the LLM reply and sends chat, executes actions,
// routes plans, and schedules followups for a chat interaction.
func dispatchChatResponse(b *bot.Bot, user, msg, systemPrompt, reply string) {
	parsed := ai.Parse(reply)

	if isSilentResponse(reply) {
		b.Logger.Debug("chat response: LLM chose to stay silent")
		return
	}

	if handleSilentActionResponse(b, user, systemPrompt, parsed) {
		return
	}

	sendMainChatReply(b, parsed)

	if handlePlanResponse(b, user, parsed) {
		return
	}

	steps := buildChatSteps(b, parsed, msg)
	action.ExecutePlan(b, steps, user)

	if parsed.FollowupSec > 0 {
		scheduleFollowup(b, user, parsed.FollowupSec)
	}
}

// handleSilentActionResponse returns true and performs the follow-up LLM call
// for silent status/inventory actions.
func handleSilentActionResponse(b *bot.Bot, user, systemPrompt string, parsed ai.ParsedReply) bool {
	if !isSilentAction(parsed) || len(parsed.Actions) != 1 {
		return false
	}
	if parsed.CleanReply != "" {
		b.Logger.Info("chat reply sending (pre-silent)", slog.String("reply", parsed.CleanReply))
		b.SendSafeChat(parsed.CleanReply)
	}
	if parsed.FollowupSec > 0 {
		go func() {
			time.Sleep(time.Duration(parsed.FollowupSec) * time.Second)
			handleSilentResponse(b, user, systemPrompt, parsed.Actions)
		}()
	} else {
		handleSilentResponse(b, user, systemPrompt, parsed.Actions)
	}
	return true
}

// sendMainChatReply sends the visible chat reply to the server.
func sendMainChatReply(b *bot.Bot, parsed ai.ParsedReply) {
	if parsed.CleanReply != "" {
		b.Logger.Info("chat reply sending", slog.String("reply", parsed.CleanReply))
		b.SendSafeChat(parsed.CleanReply)
		return
	}
	b.Logger.Info("chat: AI returned no visible reply text")
}

// handlePlanResponse routes a <plan> block through the planner if present.
func handlePlanResponse(b *bot.Bot, user string, parsed ai.ParsedReply) bool {
	if len(parsed.PlanSteps) == 0 || b.Planner == nil {
		return false
	}
	b.Logger.Info("plan detected from LLM, routing through planner",
		"steps", len(parsed.PlanSteps), "user", user)
	b.Planner.Run(parsed.CleanReply, user, parsed.PlanSteps)
	return true
}

// buildChatSteps converts parsed actions into steps, falling back to movement
// or intent inference when no action tags are present.
func buildChatSteps(b *bot.Bot, parsed ai.ParsedReply, msg string) []action.Step {
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
	// Intent fallback: LLM said "Siap/Oke/Bentar..." but forgot to emit an
	// <action> tag. Synthesize one from the user's request when their verb
	// clearly maps to a known action.
	if len(steps) == 0 && isAffirmativeReply(parsed.CleanReply) {
		steps = inferActionIntent(msg)
		if len(steps) > 0 {
			b.Logger.Info("chat inferred action from intent (LLM forgot tag)",
				slog.String("user_msg", msg),
				slog.String("llm_reply", parsed.CleanReply),
				slog.Any("steps", steps),
			)
		}
	}
	return steps
}

// isSilentAction reports whether the parsed reply contains a silent action
// (status or inventory) that needs a follow-up LLM call.
func isSilentAction(parsed ai.ParsedReply) bool {
	for _, act := range parsed.Actions {
		label := strings.ToLower(act.Label)
		if label == "status" || label == "inventory" {
			return true
		}
	}
	return false
}

// handleSilentResponse performs follow-up LLM calls for silent action tags
// like status and inventory.
func handleSilentResponse(b *bot.Bot, sourceName string, systemPrompt string, actions []ai.Action) {
	for _, act := range actions {
		label := strings.ToLower(act.Label)
		if label == "status" {
			newHp, newHunger, newBotCoords := b.GetStatusDetails()
			newInvSummary := b.GetInventorySummary()
			followPrompt := fmt.Sprintf(
				"[SYSTEM: Hasil cek status MILIKMU (bot) saat ini: HP %d/20, Hunger %d/20, Posisi %s. Inventory: %s. "+
					"LAPORKAN LANGSUNG ke <%s> dengan format 'Aku ...' atau 'Status aku ...'. "+
					"JANGAN bilang 'Oke aku cek dulu'. JANGAN pakai kata 'kamu' untuk merujuk diri sendiri. "+
					"JANGAN sertakan label [status] atau tag <action> lagi.]",
				newHp, newHunger, newBotCoords, newInvSummary, sourceName)

			reply2, err := b.AiClient.Ask(sourceName, systemPrompt, followPrompt)
			if err != nil {
				b.Logger.Error("silent response follow-up failed (status)", "error", err)
				continue
			}
			parsed2 := ai.Parse(reply2)
			if parsed2.CleanReply != "" {
				b.Logger.Info("chat reply sending (status follow-up)", slog.String("reply", parsed2.CleanReply))
				b.SendSafeChat(parsed2.CleanReply)
			}
		} else if label == "inventory" {
			newInvSummary := b.GetInventorySummary()
			followPrompt := fmt.Sprintf(
				"[SYSTEM: Hasil cek inventory MILIKMU (bot) saat ini: %s. "+
					"LAPORKAN LANGSUNG ke <%s> dengan format 'Aku punya ...' atau 'Aku masih punya ...'. "+
					"KAMU adalah bot Luna. JANGAN bilang 'Kamu punya' — itu berarti player. "+
					"JANGAN bilang 'Oke aku cek dulu' atau 'Aku cek lagi'. Langsung sebutkan isi inventory. "+
					"JANGAN sertakan label [inventory] atau tag <action> lagi.]",
				newInvSummary, sourceName)

			reply2, err := b.AiClient.Ask(sourceName, systemPrompt, followPrompt)
			if err != nil {
				b.Logger.Error("silent response follow-up failed (inventory)", "error", err)
				continue
			}
			parsed2 := ai.Parse(reply2)
			if parsed2.CleanReply != "" {
				b.Logger.Info("chat reply sending (inventory follow-up)", slog.String("reply", parsed2.CleanReply))
				b.SendSafeChat(parsed2.CleanReply)
			}
		}
	}
}

// scheduleFollowup starts a goroutine that waits for the given delay, then
// queries the LLM with fresh context and sends the reply. This enables the
// bot to send multi-part messages autonomously (e.g. "Oke aku cek dulu" →
// 2s later → "Aku punya kayu 4, batu 12...").
func scheduleFollowup(b *bot.Bot, user string, delaySec int) {
	go func() {
		time.Sleep(time.Duration(delaySec) * time.Second)

		if b.AiClient == nil {
			return
		}

		// Re-synthesize context at follow-up time so the LLM sees the
		// current state (inventory may have changed, etc.).
		hp, hunger, botCoords := b.GetStatusDetails()
		heldItem := b.GetHeldItem()
		invSummary := b.GetInventorySummary()
		playerCoordsStr := ""
		if pCoords, ok := b.GetPlayerCoords(user); ok {
			playerCoordsStr = fmt.Sprintf("X:%.0f Y:%.0f Z:%.0f", pCoords.X(), pCoords.Y(), pCoords.Z())
		}

		b.Mu.Lock()
		botName := b.Name
		b.Mu.Unlock()

		botStatusText := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)
		systemPrompt := b.AiClient.BuildSystemPrompt(
			botName,
			botCoords+" ("+botStatusText+")",
			playerCoordsStr,
			heldItem,
			invSummary,
		)

		if b.Planner != nil {
			todoStr := b.Planner.TodoRenderForPrompt()
			if todoStr != "" {
				systemPrompt += "\n\n" + todoStr
			}
		}

		followPrompt := fmt.Sprintf(
			"[SYSTEM: Ini adalah follow-up message. Kamu tadi bilang akan mengecek sesuatu ke <%s>. "+
				"Sekarang berikan laporan/results secara natural. Inventory: %s. HP: %d/20. "+
				"JANGAN ulangi pesan sebelumnya. Berikan info baru saja.]",
			user, invSummary, hp)

		reply, err := b.AiClient.Ask(user, systemPrompt, followPrompt)
		if err != nil {
			b.Logger.Error("follow-up LLM call failed", "error", err, "user", user)
			return
		}

		parsed := ai.Parse(reply)
		if parsed.CleanReply != "" {
			b.Logger.Info("chat reply sending (follow-up)", slog.String("reply", parsed.CleanReply))
			b.SendSafeChat(parsed.CleanReply)
		}

		// Execute any actions from the follow-up.
		if len(parsed.Actions) > 0 {
			steps := make([]action.Step, 0, len(parsed.Actions))
			for _, act := range parsed.Actions {
				steps = append(steps, action.Step{Label: act.Label, Param: act.Param})
			}
			action.ExecutePlan(b, steps, user)
		}

		// Recursive follow-up — the LLM can chain another <followup>.
		if parsed.FollowupSec > 0 {
			scheduleFollowup(b, user, parsed.FollowupSec)
		}
	}()
}
