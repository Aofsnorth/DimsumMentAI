package bot

import (
	"fmt"
	"log/slog"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/event"
)

// ReportActionStatus asks the LLM to generate a natural chat message for an
// action result. The LLM is instructed not to emit action tags and to use
// friendly item names. This replaces hardcoded status messages like
// "Selesai craft X".
func (b *Bot) ReportActionStatus(user string, status event.ActionStatus) {
	if b.AiClient == nil {
		return
	}
	if user == "" {
		b.Mu.Lock()
		user = b.LastChatPartner
		b.Mu.Unlock()
	}
	if user == "" {
		user = b.AiCfg.MainPlayer
	}
	if user == "" {
		return
	}

	// Run in a caller-owned goroutine. Every status prompt includes a unique
	// nonce so the LLM's session history cannot answer a stale earlier status
	// and produce duplicate, contradictory follow-up messages.
	go reportActionStatusAsync(b, user, status)
}

func reportActionStatusAsync(b *Bot, user string, status event.ActionStatus) {
	hp, hunger, coords := b.GetStatusDetails()
	heldItem := b.GetHeldItem()
	invSummary := b.GetInventorySummary()
	playerCoords := ""
	if pc, ok := b.GetPlayerCoords(user); ok {
		playerCoords = fmt.Sprintf("X:%.0f Y:%.0f Z:%.0f", pc.X(), pc.Y(), pc.Z())
	}
	botStatus := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)

	systemPrompt := b.AiClient.BuildSystemPrompt(
		b.Name,
		coords+" ("+botStatus+")",
		playerCoords,
		heldItem,
		invSummary,
	)
	systemPrompt += "\n\n[STATUS REPLY RULES]\n" +
		"You are generating a short status update after the bot just performed an action for the player.\n" +
		"- Reply in natural Indonesian, following the player's tone; do not force slang like cuy or translate internal action labels into speech.\n" +
		"- Report concrete outcomes, not generic success announcements. Never turn waypoints into found or collected items.\n" +
		"- DO NOT use action tags (<action>, <plan>, <followup>, etc.).\n" +
		"- DO NOT use raw item IDs like 'oak_planks'; use friendly names like 'Oak Planks'.\n" +
		"- Keep it under 25 words unless the result needs explanation.\n" +
		"- Answer ONLY the latest ACTION RESULT nonce below; ignore older status prompts in this conversation.\n" +
		"- If the action failed, briefly explain why and offer to help or suggest what to do next.\n" +
		"- If it succeeded, just say what happened naturally."

	prompt := buildStatusPrompt(status)

	reply, err := b.AiClient.Ask(user, systemPrompt, prompt)
	if err != nil {
		b.Logger.Error("action status LLM call failed", slog.String("error", err.Error()), slog.String("user", user))
		return
	}
	parsed := ai.Parse(reply)
	if parsed.CleanReply != "" {
		b.Logger.Info("action status reply sending", slog.String("reply", parsed.CleanReply))
		b.SendSafeChat(parsed.CleanReply)
	}
}

func buildStatusPrompt(status event.ActionStatus) string {
	item := FormatItemName(status.Item)
	nonce := time.Now().UnixNano()
	if !status.Success || status.Error != "" {
		if status.Error == "" {
			status.Error = "hasil belum terkonfirmasi"
		}
		return fmt.Sprintf("ACTION RESULT #%d: %s failed. Item: %s, count: %d, reason: %s. Generate a natural status reply.", nonce, status.Action, item, status.Count, status.Error)
	}
	if status.Action == "explore" || status.Action == "exploredir" {
		return fmt.Sprintf("ACTION RESULT #%d: Exploration ended; %d navigation waypoints reached. No item discovery or collection is established by this result. Say briefly that you have looked around; do not announce a loot count or repeat internal mode names.", nonce, status.Count)
	}
	if status.Count > 0 {
		return fmt.Sprintf("ACTION RESULT #%d: %s succeeded. Item: %s, count: %d. Generate a natural status reply.", nonce, status.Action, item, status.Count)
	}
	return fmt.Sprintf("ACTION RESULT #%d: %s succeeded. Item: %s. Generate a natural status reply.", nonce, status.Action, item)
}
