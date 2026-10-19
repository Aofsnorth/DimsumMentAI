// Package chat handles player chat messages and AI-driven bot responses.
package chat

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/chat/reaction"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/perception"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"

	"github.com/go-gl/mathgl/mgl32"
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

	// If source is missing, extract sender from message prefix using bot's known player tracking
	if evt.SourceName == "" {
		if sender, cleanMsg, ok := handler.ExtractChatSenderAndMessage(msg, b.FindKnownPlayer); ok {
			evt.SourceName = sender
			msg = cleanMsg
			evt.Message = cleanMsg
		}
	} else {
		// Clean any prefix/rank from SourceName if it's like "MEMBER Synterix" or "[VIP] Synterix"
		if canonical, ok := b.FindKnownPlayer(evt.SourceName); ok {
			evt.SourceName = canonical
		}
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

	// A whisper opens a private conversation, and the reply has to go back the
	// same way. Recording it here — before any filter — is deliberate: a message
	// that is not tagged, or from somebody who is not linked, still means the
	// channel is private, and answering the next one in public would be the leak.
	if evt.Whisper {
		b.NoteWhisperFrom(evt.SourceName)
	} else if b.WhisperPartner() != "" {
		// They answered in public, so the conversation is public again.
		b.ClearWhisperWith(evt.SourceName)
	}

	if strings.HasPrefix(msg, "!") {
		HandleAdminCommand(b, msg, evt.SourceName)
		return
	}

	if strings.HasPrefix(strings.ToLower(msg), "plan:") {
		handlePlanCommand(b, evt, msg)
		return
	}

	// A recording brief arrives as a chat message, exactly like a plan does. It
	// is handled before the AI client check below because starting a recording
	// must work even on a bot that cannot chat — that configuration is exactly
	// the one where somebody drops the bot in a world and walks away.
	if handled := handleEpisodeCommand(b, evt, msg); handled {
		return
	}

	// The in-game access grant runs before the intent matchers and the AI: a
	// whitelist request is a configuration change, not a conversation.
	if handleWhitelistCommand(b, evt, msg) {
		return
	}

	// A staged routine ("lompat, jalan 1 block, muter sebanyak 5 kali") is
	// sequenced in code: the model narrates it, but the arithmetic of stages
	// and counts is not its job to re-emit tag by tag.
	if handleStagedCommand(b, evt, msg) {
		return
	}

	// The guardian toggle ("kalo ada mob jahat, bunuh aja ya") is a behaviour
	// switch on the threat detector, not a conversation: the model can describe
	// the reflex but cannot reach inside it.
	if handleGuardCommand(b, evt, msg) {
		return
	}

	// noteHumanPresence goes before the AI check on purpose. A person standing
	// there has the bot's attention whether or not the bot can answer, and the
	// episode waits either way.
	noteHumanPresence(b, evt.SourceName)

	// Opting out of following, in either the command form or the plain-language
	// one a player actually types.
	//
	// This sits before the AI-client check on purpose: stopping is not a
	// conversation. A bot configured without a model could not be told to stop
	// following at all, and the player had no way to know why.
	//
	// The log caught the other half of this being ignored: the player typed
	// "Berhenti ikutin aku" — stop following me — and the bot followed for the
	// next four minutes. A follow the player cannot stop is not a follow, it is a
	// leash, and the plain-language form is the one that will arrive.
	if isStopFollowingIntent(msg) {
		// Stop has to cancel the running job, not just the legs. The gather loop
		// is driven by a context, and handing it context.Background() — as the
		// action handlers used to — makes it uncancellable: the bot says
		// "okay, stopping", then keeps re-navigating to the next tree.
		b.Stop()
		b.DisableImplicitFollow(evt.SourceName)
		b.Logger.Info("chat: following stopped by request", slog.String("from", evt.SourceName))
		replyViaAI(b, evt.SourceName, msg,
			"You stopped following the player and are no longer moving after them.")
		return
	}

	if isStopAttackIntent(msg) {
		if b.CombatMgr != nil {
			b.CombatMgr.Disengage()
		}
		b.Logger.Info("chat: combat stopped by request", slog.String("from", evt.SourceName))
		replyViaAI(b, evt.SourceName, msg,
			"You stopped attacking and are no longer engaging in combat.")
		return
	}

	if b.AiClient == nil {
		b.Logger.Info("chat ignored: AI client not configured")
		return
	}

	allowed, _ := b.Throttler.Filter(evt.SourceName, msg)
	if !allowed {
		b.Logger.Info("chat ignored: throttled or duplicate", slog.String("msg", msg))
		return
	}

	b.Logger.Info("chat processing: querying AI", slog.String("from", evt.SourceName))

	// Read-and-decide time, before the model is even asked.
	//
	// The placement matters. A person reads a message and decides it is worth
	// answering before they start composing, and the model call below stands in
	// for composing. Putting the delay in front means the total reply time is
	// "a person noticed" plus "a person wrote", which is how it actually
	// decomposes. Putting it afterwards would leave the model latency in front
	// where it is still perfectly correlated with message complexity, which is
	// the machine-readable part of the timing that gives the bot away.
	//
	// Commands have already returned above, so this never delays an episode
	// command, and the throttle has already rejected duplicates, so a message
	// that arrives twice does not get to be slow twice.
	if wait := reaction.Delay(msg); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			b.Logger.Info("chat: giving up on the reply delay, bot is shutting down")
			return
		}
	}

	systemPrompt := buildChatContext(b, evt.SourceName, msg, botName)
	reply, err := b.AiClient.Ask(evt.SourceName, systemPrompt, msg)
	if err != nil {
		b.Logger.Error("Failed to ask Nvidia LLM", "error", err.Error())
		b.Throttler.Rollback(evt.SourceName, msg)
		return
	}

	b.Logger.Debug("Nvidia LLM raw response received", "raw", reply)
	if tookTask := dispatchChatResponse(b, evt.SourceName, msg, systemPrompt, reply); !tookTask {
		noteImplicitFollow(b, evt.SourceName)
	}
}

// noteImplicitFollow drifts the bot toward whoever it was just talking to.
//
// "Come here" is the explicit version and it has always worked. The implicit
// version is the same social reflex without the instruction: a player types to
// the bot, the bot answers, and it is standing six blocks away doing nothing.
// A player reading that concludes they have been ignored, and the natural next
// message is the explicit one — which is the whole loop this closes.
//
// It is a follow and not a walk-to, so the bot keeps up if the player keeps
// moving, and it is deliberately silent: a bot that announces "mengikuti kamu"
// every time somebody says hello is worse than one that just walks over.
//
// Opt out with !nofollow, which the caller above honours before reaching here.
func noteImplicitFollow(b *bot.Bot, who string) {
	if who == "" || !isOperator(b, who) || !b.ImplicitFollowAllowed(who) {
		// Only the player the bot answers to pulls it across the world. Any
		// other player's message would otherwise drag the bot away mid-task.
		return
	}

	// An explicit instruction already in force wins. If the player said "go
	// mine some stone", a chat message arriving mid-task must not cancel it.
	if b.IsBusy() {
		return
	}

	_, pos, ok := b.FindPlayer(who)
	if !ok {
		return
	}

	// Standing on top of them is not company, it is collision.
	if pos.Sub(b.GetCoords()).Len() < 2 {
		return
	}

	b.FollowPlayer(who)
}

// shouldProcessChat applies filters for bot echoes, player whitelist, tagging,
// and distance before a message is sent to the LLM.
func shouldProcessChat(b *bot.Bot, evt event.ChatEvent, msg, botName string) bool {
	// An echo is the server handing back a line the bot just sent, stamped with
	// the name the server knows it by. That is the moment to learn the name: the
	// PlayerList entry that would carry it is matched by UUID and entity ID, and a
	// server that sends neither leaves the bot still believing in its configured
	// name. Without this, the bot never recognises its own reply and treats a
	// player's message as its own.
	if b.IsBotEcho(msg) {
		b.NoteServerAssignedName(evt.SourceName)
		b.Logger.Info("chat ignored: bot echo detected")
		return false
	}
	if b.IsOwnChatSource(evt.SourceName) {
		b.Logger.Info("chat ignored: own message", slog.String("source", evt.SourceName))
		return false
	}

	if b.AiCfg.RespondOnlyToLinkedPlayer {
		mainPlayers := b.AiCfg.GetMainPlayers()
		if len(mainPlayers) > 0 &&
			!b.AiCfg.IsMainPlayer(evt.SourceName) &&
			!b.IsWhitelistedPlayer(evt.SourceName) {
			b.Logger.Info("chat ignored: not linked main player",
				slog.String("source", evt.SourceName),
				slog.String("main_players", strings.Join(mainPlayers, ", ")),
			)
			return false
		}
	}

	isTagged := isTaggedInBot(msg, b)

	// A whisper is already addressed to this bot by name — the server put the
	// bot's own name in the recipient field and only delivered the line to it.
	// Requiring the name to appear again inside the message means asking somebody
	// to write "Luna" into a DM they already sent to Luna, which is the reply a
	// player stops sending.
	if b.AiCfg.RespondOnlyWhenTagged && !evt.Whisper {
		if !isTagged {
			b.Logger.Info("chat ignored: bot not tagged in message",
				slog.String("msg", msg),
				slog.String("bot_name", botName),
			)
			return false
		}
	}

	if len(b.AiCfg.GetMainPlayers()) == 0 {
		pCoords, ok := b.GetPlayerCoords(evt.SourceName)
		if !ok {
			if !isTagged {
				b.Logger.Info("chat ignored: player position unknown", slog.String("player", evt.SourceName))
				return false
			}
			b.Logger.Debug("player position unknown for tagged chat, proceeding", slog.String("player", evt.SourceName))
		} else {
			botCoords := b.GetCoords()
			dx := pCoords.X() - botCoords.X()
			dy := pCoords.Y() - botCoords.Y()
			dz := pCoords.Z() - botCoords.Z()
			dist := float32(mathSqrt(float64(dx*dx + dy*dy + dz*dz)))
			maxDist := float32(10.0)
			if isTagged {
				maxDist = 64.0
			}
			if dist > maxDist {
				b.Logger.Info("chat ignored: player too far",
					slog.String("player", evt.SourceName),
					slog.Float64("distance", float64(dist)),
					slog.Float64("max_distance", float64(maxDist)),
				)
				return false
			}
		}
	}

	return true
}

// isTaggedInBot reports whether the message explicitly mentions the bot. It checks
// the bot's active username, original configured name, and any configured aliases.
func isTaggedInBot(msg string, b *bot.Bot) bool {
	if b == nil {
		return false
	}
	b.Mu.Lock()
	activeName := b.Name
	cfgName := b.ConfiguredName
	tagAliases := append([]string{}, b.TagAliases...)
	aiAliases := append([]string{}, b.AiCfg.TagAliases...)
	onlyAliases := b.AiCfg.OnlyTagAliases
	b.Mu.Unlock()

	if onlyAliases {
		extra := make([]string, 0, len(tagAliases)+len(aiAliases))
		extra = append(extra, tagAliases...)
		extra = append(extra, aiAliases...)
		return isTaggedInMessage(msg, "", extra...)
	}

	extra := make([]string, 0, 2+len(tagAliases)+len(aiAliases))
	if cfgName != "" {
		extra = append(extra, cfgName)
	}
	extra = append(extra, tagAliases...)
	extra = append(extra, aiAliases...)

	return isTaggedInMessage(msg, activeName, extra...)
}

// isTaggedInMessage reports whether the message explicitly mentions the bot.
// It checks botName as well as any extra candidate tags/aliases.
func isTaggedInMessage(msg, botName string, extraTags ...string) bool {
	msgLower := strings.ToLower(msg)
	allTags := make([]string, 0, len(extraTags)+2)
	if botName != "" {
		allTags = append(allTags, botName)
	}
	allTags = append(allTags, extraTags...)

	for _, tag := range allTags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		tagLower := strings.ToLower(tag)

		// Check tag directly (with @ and word-boundary)
		if matchWordOrTag(msgLower, tagLower) {
			return true
		}

		// Also check without leading '.' (Bedrock/Geyser gamertag prefix)
		if strings.HasPrefix(tagLower, ".") && len(tagLower) > 1 {
			if matchWordOrTag(msgLower, strings.TrimPrefix(tagLower, ".")) {
				return true
			}
		}

		// Also check with leading '.'
		if !strings.HasPrefix(tagLower, ".") {
			if matchWordOrTag(msgLower, "."+tagLower) {
				return true
			}
		}
	}
	return false
}

func matchWordOrTag(msgLower, tagLower string) bool {
	if tagLower == "" {
		return false
	}
	// Direct @tag match with boundary (e.g. @Luna, @Luna!, @.Shiga)
	atTag := "@" + tagLower
	idx := 0
	for {
		pos := strings.Index(msgLower[idx:], atTag)
		if pos < 0 {
			break
		}
		absPos := idx + pos
		endPos := absPos + len(atTag)
		idx = endPos

		leftOK := absPos == 0 || isBoundaryRune(rune(msgLower[absPos-1]))
		rightOK := endPos == len(msgLower) || isBoundaryRune(rune(msgLower[endPos]))
		if leftOK && rightOK {
			return true
		}
	}

	// Exact whole message match
	if msgLower == tagLower {
		return true
	}
	// Word boundary match for bare tag
	idx = 0
	for {
		pos := strings.Index(msgLower[idx:], tagLower)
		if pos < 0 {
			break
		}
		absPos := idx + pos
		endPos := absPos + len(tagLower)
		idx = endPos

		leftOK := absPos == 0 || isBoundaryRune(rune(msgLower[absPos-1]))
		rightOK := endPos == len(msgLower) || isBoundaryRune(rune(msgLower[endPos]))

		if leftOK && rightOK {
			return true
		}
	}
	return false
}

func isBoundaryRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == '.' || r == '!' || r == '?' ||
		r == ':' || r == ';' || r == '@' || r == '>' || r == '<' || r == '(' || r == ')' ||
		r == '"' || r == '\''
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
	// Planning is the strongest thing a chat line can ask for: it schedules
	// movement, mining and building. Every other state-changing chat command
	// (episode, guard, whitelist, staged routines) is operator-gated; this one
	// was not, so a stranger could start a plan when the linked-player gate was
	// disabled or unset.
	if !isOperator(b, evt.SourceName) {
		b.Logger.Warn("plan: command ignored: not the configured operator",
			"from", evt.SourceName)
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
	nearbyPlayers := NearbyPlayersSummary(b, 24.0, 8)
	// Grounded block sight: without it the model answers "no button here"
	// while staring at one. Line-of-sight only, so no xray.
	visibleBlocks := perception.BlocksSummary(b, 12.0, 6)
	botStatusText := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)
	systemPrompt := b.AiClient.BuildSystemPrompt(
		botName,
		botCoords+" ("+botStatusText+")",
		playerCoordsStr,
		heldItem,
		invSummary,
	)

	// Inform model of its display name and aliases so it recognizes when players address it by configured name
	b.Mu.Lock()
	cfgName := b.ConfiguredName
	aliases := append([]string{}, b.TagAliases...)
	b.Mu.Unlock()
	if cfgName != "" && !strings.EqualFold(cfgName, botName) {
		systemPrompt += fmt.Sprintf("\n- In addition to \"%s\", players also call you \"%s\" (or \"@%s\"). That name also refers to YOU.", botName, cfgName, cfgName)
	}
	if len(aliases) > 0 {
		systemPrompt += fmt.Sprintf("\n- You also respond to tags/nicknames: %s.", strings.Join(aliases, ", "))
	}

	systemPrompt += "\n\n[GROUNDED PERCEPTION] Visible mobs (line-of-sight, non-item): " + visibleMobs + "."
	systemPrompt += "\nVisible blocks nearby (line-of-sight, clickable ones can be used with interact): " + visibleBlocks + "."
	// Players are the one thing this context used to be blind to entirely, and
	// the blind spot produced a confident lie: the bot told the player "there is
	// nobody near me" while three players stood in the entity census.
	systemPrompt += "\n\n[NEARBY PLAYERS] Real players close enough to walk over to: " + nearbyPlayers +
		". A player is a player, whatever they are standing next to; an armor stand with a" +
		" name on it is a hologram or an NPC, not a player. \"no line of sight\" means a wall" +
		" is in the way — you know they are there but cannot see or click them yet."

	// Append current plan/todo state so the LLM is always aware of any
	// in-progress multi-step task, even when a new chat message arrives
	// mid-plan.
	if b.Planner != nil {
		todoStr := b.Planner.TodoRenderForPrompt()
		if todoStr != "" {
			systemPrompt += "\n\n" + todoStr
		}
	}

	b.Mu.Lock()
	autonomyContext := b.AutonomyContextFunc
	b.Mu.Unlock()
	if autonomyContext != nil {
		systemPrompt += "\n\n[SHARED AGENT INTENT]\n" + autonomyContext()
	}

	// Append curated long-term memories (MinePal-style Active Memory).
	systemPrompt = appendMemoryContext(b, systemPrompt)

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
		parts = append(parts, fmt.Sprintf("%s (%.0fm)", describeActor(*info), origin.Sub(info.Position).Len()))
		if len(parts) >= limit {
			break
		}
	}
	return strings.Join(parts, ", ")
}

// describeActor renders one actor for a prompt, keeping the display name.
//
// The previous form printed the type whenever the type and the name differed,
// which is to say for every actor that has a nickname — and a nickname is the
// entire useful part. A join portal, a hologram text line and pixel-art filler
// are all minecraft:armor_stand, so the bot's own "in sight" list was several
// copies of the word "armor_stand" with the one piece of information a player
// would have used to tell them apart thrown away.
func describeActor(info entity.Info) string {
	typ := entity.NormalizeName(info.Type)
	name := entity.NormalizeName(info.Name)
	if name == "" || name == typ {
		return typ
	}
	return fmt.Sprintf("%s (%s)", name, typ)
}

// nearbyEyeHeight and nearbyTargetHeight are the two ends of the sight ray used
// for the player line, matching the eye and torso heights entity.IsVisibleMob
// uses for mobs so the two lists cannot disagree about who is behind a wall.
const (
	nearbyEyeHeight    = 1.62
	nearbyTargetHeight = 1.2
)

// NearbyPlayersSummary lists the real players around the bot, nearest first.
//
// Players were missing from the chat context altogether, and not by accident of
// formatting: handleAddPlayer files them in PlayerPositions and never touches
// b.Actors, while entity.IsMob rejects the "player" type outright. So the
// grounded mob list could only ever contain non-players, and the model was
// asked to answer "who is near me" from a list that structurally could not
// contain a player.
//
// Proximity, not visibility, is the question here — the bot needs to know it can
// walk over to someone — so line of sight is reported per player rather than
// used to filter, because a player behind a wall is still someone the player
// asking can go and see.
func NearbyPlayersSummary(b *bot.Bot, maxDistance float32, limit int) string {
	type person struct {
		info entity.Info
		dist float32
	}

	b.Mu.Lock()
	origin := b.Pos
	seen := make(map[uint64]entity.Info, len(b.PlayerUsernames))
	for id, name := range b.PlayerUsernames {
		pos, ok := b.PlayerPositions[id]
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		seen[id] = entity.Info{ID: id, Type: "minecraft:player", Name: name, Position: pos}
	}
	b.Mu.Unlock()

	var people []person
	for _, info := range seen {
		// Filtered outside the lock: IsOwnChatSource takes b.Mu itself, and a
		// sync.Mutex is not reentrant.
		if b.IsOwnChatSource(info.Name) {
			continue
		}
		dist := origin.Sub(info.Position).Len()
		if maxDistance > 0 && dist > maxDistance {
			continue
		}
		people = append(people, person{info: info, dist: dist})
	}
	if len(people) == 0 {
		return "none"
	}
	sort.SliceStable(people, func(a, c int) bool { return people[a].dist < people[c].dist })

	parts := make([]string, 0, min(limit, len(people)))
	for _, p := range people {
		entry := fmt.Sprintf("%s (%.0fm)", p.info.Name, p.dist)
		if !entity.HasLineOfSight(b.WorldModel, b,
			origin.Add(mgl32.Vec3{0, nearbyEyeHeight, 0}),
			p.info.Position.Add(mgl32.Vec3{0, nearbyTargetHeight, 0})) {
			entry += ", no line of sight"
		}
		parts = append(parts, entry)
		if len(parts) >= limit {
			break
		}
	}
	return strings.Join(parts, ", ")
}
