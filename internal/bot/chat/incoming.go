package chat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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

	b.Mu.Lock()
	b.LastChatPartner = evt.SourceName
	b.Mu.Unlock()

	// 4. Handle Debug/Admin commands prefixed with '!'
	if strings.HasPrefix(msg, "!") {
		HandleAdminCommand(b, msg, evt.SourceName)
		return
	}

	// 4b. Handle explicit "plan:" prefix — player wants the agentic planner
	// to break down and execute a multi-step task.
	if strings.HasPrefix(strings.ToLower(msg), "plan:") {
		request := strings.TrimSpace(msg[5:])
		if request == "" {
			return
		}
		if b.Planner != nil {
			b.Logger.Info("plan: command triggered", "user", evt.SourceName, "request", request)
			b.ReportActionStatus(evt.SourceName, event.ActionStatus{Action: "plan", Success: true})
			b.Planner.RunFromChat(evt.SourceName, request)
		}
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

	// Append current plan/todo state so the LLM is always aware of any
	// in-progress multi-step task, even when a new chat message arrives
	// mid-plan.
	if b.Planner != nil {
		todoStr := b.Planner.TodoRenderForPrompt()
		if todoStr != "" {
			systemPrompt += "\n\n" + todoStr
		}
	}

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
		// Send the initial acknowledgment FIRST (e.g. "Oke aku cek dulu")
		// before doing the follow-up LLM call. Previously this was swallowed
		// because handleSilentResponse returned before the reply was sent.
		if parsed.CleanReply != "" {
			b.Logger.Info("chat reply sending (pre-silent)", slog.String("reply", parsed.CleanReply))
			b.SendSafeChat(parsed.CleanReply)
		}
		// If the LLM also emitted a <followup> delay, wait before doing
		// the silent action follow-up. This makes the conversation feel
		// natural: "Oke aku cek dulu" → 2s pause → "Aku punya kayu 4..."
		if parsed.FollowupSec > 0 {
			go func() {
				time.Sleep(time.Duration(parsed.FollowupSec) * time.Second)
				handleSilentResponse(b, evt.SourceName, systemPrompt, parsed.Actions)
			}()
		} else {
			handleSilentResponse(b, evt.SourceName, systemPrompt, parsed.Actions)
		}
		return
	}

	// 10. Send the main reply
	if parsed.CleanReply != "" {
		b.Logger.Info("chat reply sending", slog.String("reply", parsed.CleanReply))
		b.SendSafeChat(parsed.CleanReply)
	} else {
		b.Logger.Info("chat: AI returned no visible reply text")
	}

	// 10b. If the LLM emitted a <plan> block, route it through the agentic
	// planner (plan → execute → observe → re-evaluate loop) instead of the
	// flat sequential executor.
	if len(parsed.PlanSteps) > 0 && b.Planner != nil {
		b.Logger.Info("plan detected from LLM, routing through planner",
			"steps", len(parsed.PlanSteps), "user", evt.SourceName)
		b.Planner.Run(parsed.CleanReply, evt.SourceName, parsed.PlanSteps)
		return
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
	action.ExecutePlan(b, steps, evt.SourceName)

	// 12. If the LLM emitted a <followup>N</followup> tag, schedule a
	// delayed self-message. The bot will re-query the LLM after N seconds
	// with fresh context, letting it continue the conversation without the
	// player needing to trigger again. This is the "loop msg" mechanism.
	if parsed.FollowupSec > 0 {
		scheduleFollowup(b, evt.SourceName, parsed.FollowupSec)
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
