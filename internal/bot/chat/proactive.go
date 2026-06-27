package chat

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
)

// StartProactiveLoop launches a background goroutine that periodically gives
// the bot a chance to initiate conversation autonomously. Every
// ProactiveIntervalSec seconds (configurable, 0 = disabled), the bot gathers
// context about its surroundings and asks the LLM whether it wants to say
// something. The LLM can respond with chat text, actions, <followup> for
// chained messages, or <silent/> to stay quiet.
//
// This is the "AGI" layer — the bot is not purely reactive. It can:
//   - Greet players who come nearby
//   - Comment on events (night falling, found resources, took damage)
//   - Warn about danger (low HP, hostile mobs nearby)
//   - Suggest activities ("aku lihat ada iron ore deket sini, mau aku tambang?")
//   - Just chat naturally when bored
func StartProactiveLoop(ctx context.Context, b *bot.Bot) {
	intervalSec := b.AiCfg.ProactiveIntervalSec
	if intervalSec <= 0 {
		b.Logger.Debug("proactive loop disabled (interval=0)")
		return
	}
	if b.AiClient == nil {
		b.Logger.Debug("proactive loop disabled (no AI client)")
		return
	}

	chance := b.AiCfg.ProactiveChance
	if chance <= 0 {
		chance = 0.3 // default: 30% chance each tick to avoid spamming
	}

	interval := time.Duration(intervalSec) * time.Second

	b.Logger.Info("proactive conversation loop started",
		slog.Int("interval_sec", intervalSec),
		slog.Float64("chance", chance),
	)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Add random jitter on first tick to avoid all bots syncing.
		initialDelay := time.Duration(rand.Intn(intervalSec)) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(initialDelay):
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Roll the dice — don't query LLM every single tick.
				if rand.Float64() > chance {
					continue
				}
				proactiveTick(ctx, b)
			}
		}
	}()
}

// proactiveTick gathers context and asks the LLM if it wants to say something
// proactively. The LLM's reply is processed the same way as a normal chat
// response (chat text + actions + followups), except the player didn't
// initiate anything.
func proactiveTick(ctx context.Context, b *bot.Bot) {
	// Don't proactive-talk if a plan is running — the planner is already
	// doing agentic LLM calls and the bot is busy.
	if b.Planner != nil && b.Planner.IsRunning() {
		return
	}

	// Gather nearby players.
	nearbyPlayers := getNearbyPlayers(b)
	if len(nearbyPlayers) == 0 {
		// No one nearby — only proactive-talk if MainPlayer is set
		// (the bot might want to comment to itself or about the world).
		if b.AiCfg.MainPlayer == "" {
			return
		}
		nearbyPlayers = []string{b.AiCfg.MainPlayer}
	}

	// Pick the closest player as the "target" of the proactive message.
	targetPlayer := nearbyPlayers[0]

	hp, hunger, botCoords := b.GetStatusDetails()
	heldItem := b.GetHeldItem()
	invSummary := b.GetInventorySummary()
	playerCoordsStr := ""
	if pCoords, ok := b.GetPlayerCoords(targetPlayer); ok {
		playerCoordsStr = fmt.Sprintf("X:%.0f Y:%.0f Z:%.0f", pCoords.X(), pCoords.Y(), pCoords.Z())
	}

	b.Mu.Lock()
	botName := b.Name
	// Gather nearby actors (mobs, items) for context.
	nearbyActorSummary := getNearbyActorSummary(b)
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

	// Build the proactive prompt — ask the LLM to decide whether to speak.
	proactivePrompt := fmt.Sprintf(
		`[PROACTIVE TICK] Waktu: %s. Pemain terdekat: %s. Aktor/mob terdekat: %s.
Kamu lagi nggak diajak ngobrol oleh siapapun. Apakah kamu mau mulai ngobrol atau ngelakuin sesuatu sendiri?

Pilihan:
1. Jawab dengan chat natural + opsional <action> tag kalau mau ngelakuin sesuatu.
2. Jawab <silent/> kalau kamu lagi gak mau ngomong apa-apa.
3. Jawab dengan <followup>N</followup> kalau mau delay pesan kamu (misal mau cek inventory dulu terus lapor).

JANGAN paksa diri untuk ngomong kalau gak ada yang menarik. Kadang diam lebih baik.`,
		time.Now().Format("15:04"),
		strings.Join(nearbyPlayers, ", "),
		nearbyActorSummary,
	)

	reply, err := b.AiClient.Ask(targetPlayer, systemPrompt, proactivePrompt)
	if err != nil {
		b.Logger.Debug("proactive LLM call failed", "error", err)
		return
	}

	parsed := ai.Parse(reply)

	// Check for <silent/> — LLM decided not to speak.
	if isSilentResponse(reply) {
		b.Logger.Debug("proactive tick: LLM chose to stay silent")
		return
	}

	// Send chat if any.
	if parsed.CleanReply != "" {
		b.Logger.Info("proactive reply sending", slog.String("reply", parsed.CleanReply))
		b.SendSafeChat(parsed.CleanReply)
	}

	// Execute any actions.
	if len(parsed.Actions) > 0 {
		steps := make([]action.Step, 0, len(parsed.Actions))
		for _, act := range parsed.Actions {
			steps = append(steps, action.Step{Label: act.Label, Param: act.Param})
		}
		action.ExecutePlan(b, steps, targetPlayer)
	}

	// Handle plan if emitted.
	if len(parsed.PlanSteps) > 0 && b.Planner != nil {
		b.Logger.Info("proactive plan detected, routing through planner",
			"steps", len(parsed.PlanSteps))
		b.Planner.Run(parsed.CleanReply, targetPlayer, parsed.PlanSteps)
		return
	}

	// Schedule follow-up if requested.
	if parsed.FollowupSec > 0 {
		scheduleFollowup(b, targetPlayer, parsed.FollowupSec)
	}
}

// isSilentResponse checks whether the LLM reply contains a <silent/> tag,
// indicating the bot chose not to say anything during a proactive tick.
func isSilentResponse(reply string) bool {
	lower := strings.ToLower(reply)
	if strings.Contains(lower, "<silent") {
		return true
	}
	return parsedEmpty(reply)
}

func parsedEmpty(reply string) bool {
	// If after stripping all known tags the reply is empty, treat as silent.
	r := ai.Parse(reply)
	return r.CleanReply == "" && len(r.Actions) == 0 && len(r.PlanSteps) == 0 && r.FollowupSec == 0
}

// getNearbyPlayers returns usernames of players within 30 blocks of the bot.
func getNearbyPlayers(b *bot.Bot) []string {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	botPos := b.Pos
	var nearby []string
	for name, id := range b.PlayerEntityIDs {
		if strings.EqualFold(name, b.Name) {
			continue
		}
		if pos, ok := b.PlayerPositions[id]; ok {
			dx := pos.X() - botPos.X()
			dy := pos.Y() - botPos.Y()
			dz := pos.Z() - botPos.Z()
			dist := dx*dx + dy*dy + dz*dz
			if dist < 30*30 {
				nearby = append(nearby, name)
			}
		}
	}
	return nearby
}

// getNearbyActorSummary returns a brief description of nearby non-player
// actors (mobs, item drops) for the proactive context. Caller must hold b.Mu.
func getNearbyActorSummary(b *bot.Bot) string {
	botPos := b.Pos
	var mobs []string
	var items []string
	count := 0
	for _, act := range b.Actors {
		if count >= 20 {
			break
		}
		dx := act.Position.X() - botPos.X()
		dy := act.Position.Y() - botPos.Y()
		dz := act.Position.Z() - botPos.Z()
		dist := dx*dx + dy*dy + dz*dz
		if dist > 20*20 {
			continue
		}
		count++
		name := act.Name
		if strings.Contains(name, "minecraft:") {
			name = strings.TrimPrefix(name, "minecraft:")
		}
		if act.Type == "minecraft:item" {
			items = append(items, name)
		} else {
			mobs = append(mobs, name)
		}
	}
	var parts []string
	if len(mobs) > 0 {
		parts = append(parts, "Mobs: "+strings.Join(mobs[:min(5, len(mobs))], ", "))
	}
	if len(items) > 0 {
		parts = append(parts, "Items: "+strings.Join(items[:min(5, len(items))], ", "))
	}
	if len(parts) == 0 {
		return "tidak ada"
	}
	return strings.Join(parts, ". ")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
