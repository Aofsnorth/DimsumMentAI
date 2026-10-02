package bot

import (
	"fmt"
	"log/slog"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/event"
)

// statusRepeatWindow is how long an identical status report stays suppressed.
// Long enough that a retry which is genuinely going somewhere can report again,
// short enough that the player is not left waiting on a bot that has given up.
const statusRepeatWindow = 5 * time.Second

// quietSuccessActions are the repeatable jobs whose successful completion is not
// news.
//
// The bot was announcing every one of them. Four actions produced four lines of
// chat — "Kayunya kekumpul lagi, stok makin aman buat crafting", "Kayu oak
// ketemu lagi, buat anytime" — and a player watching that is not watching a
// character, they are watching a build server log. Nobody says "got 10 more
// logs" out loud after every tree; they say it when the pile is worth mentioning
// or when somebody asked.
//
// Crafting and building are deliberately absent: making a specific thing you
// set out to make is the kind of result a person does report, and it is the
// signal the player is actually waiting for.
var quietSuccessActions = map[string]bool{
	"gather": true, "mine": true, "automine": true, "harvest": true,
	"fish": true, "loot": true, "scan": true, "clear": true,
	"feed": true, "sleep": true, "store": true, "emote": true,
	// The wood chopper reports under its own label, not "gather" — which is
	// why the first version of this table let every tree through.
	"chop": true,
	// Everything else here is a side effect of doing work rather than a result
	// the player asked for.
	"bone_meal": true, "materials": true, "undo": true, "returnhome": true,
	"explore": true, "exploredir": true, "readsign": true, "shelter": true,
	// Arrival is the destination, not news: the player watches the bot walk
	// there. Announcing "sudah sampai" states the obvious, and a premature
	// one (chat latency lands before the walk ends) reads as a lie.
	"goto": true, "gotoblock": true, "standon": true, "come": true,
	//
	// Deliberately absent: status, inventory, todo, lookat. Those are admin
	// commands the player typed, and going silent in reply to a direct question
	// is the one failure this table must not cause.
}

// ShouldNarrateStatus reports whether an action result is worth saying out loud.
//
// It is a policy about the bot sounding like a player, not about correctness —
// the result is reported to the plan and the event bus either way. Only the
// chat line is suppressed.
//
// A failure is always narrated: something did not work, and the player is the
// one who has to decide what to do about it. A success is narrated unless it is
// routine work, where the absence of a message is the natural signal.
func ShouldNarrateStatus(status event.ActionStatus) bool {
	if !status.Success || status.Error != "" {
		return true
	}
	return !quietSuccessActions[status.Action]
}

// statusSignature is what makes two status reports "the same report". The count
// is deliberately left out: a bot stuck retrying one node emits the same action,
// item and error over and over, and the count never changes while it is stuck.
func statusSignature(status event.ActionStatus) string {
	return status.Action + "\x00" + status.Item + "\x00" + status.Error
}

// shouldSpeakStatus reports whether a report is different enough from the recent
// ones to be worth saying out loud.
//
// Retrying one failed action used to mean a new line of chat every attempt, and
// because each of those is a separate call to the model, the session history
// filled with near-identical prompts until the answers themselves started to
// degrade. Nothing a player does with the first eight identical sentences is
// better than not receiving them.
func (b *Bot) shouldSpeakStatus(sig string) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	if b.RecentStatusReports == nil {
		b.RecentStatusReports = make(map[string]time.Time)
	}
	now := time.Now()
	for key, at := range b.RecentStatusReports {
		if now.Sub(at) > statusRepeatWindow {
			delete(b.RecentStatusReports, key)
		}
	}
	if at, ok := b.RecentStatusReports[sig]; ok && now.Sub(at) < statusRepeatWindow {
		return false
	}
	b.RecentStatusReports[sig] = now
	return true
}

// ReportActionStatus asks the LLM to generate a natural chat message for an
// action result. The LLM is instructed not to emit action tags and to use
// friendly item names. This replaces hardcoded status messages like
// "Selesai craft X".
func (b *Bot) ReportActionStatus(user string, status event.ActionStatus) {
	// The verdict goes to the plan executor before anything else, and it goes
	// whether or not there is anyone to narrate to.
	//
	// This function used to be chat-only, which meant a subsystem that reported
	// through it — the whole interact family — told the player what happened and
	// told the planner nothing. The step then waited out its ninety-second
	// timeout and failed on work that had worked. Publishing first is what makes
	// those verbs usable at all.
	if PublishActionStatusFunc != nil {
		PublishActionStatusFunc(b, status)
	}
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
	// The chat line is optional even when the result is worth recording. A
	// routine success nobody asked about is silence, because that is what a
	// person does: they do not announce every tree.
	if !ShouldNarrateStatus(status) {
		// INFO, not Debug, and deliberately so.
		//
		// This line was written at Debug and therefore never appeared in a single
		// live run — the bot runs at INFO — which meant the feature could not be
		// verified against anything but its unit tests. A suppression nobody can
		// observe is a suppression nobody can trust, and the honest way to say
		// "the bot went quiet here" is to write it down where the operator can
		// read it. It is one line per routine action, which is exactly as often as
		// the bot does routine work.
		b.Logger.Info("status narration skipped for routine work",
			slog.String("action", status.Action),
			slog.String("item", status.Item),
			slog.Int("count", status.Count))
		return
	}
	if !b.shouldSpeakStatus(statusSignature(status)) {
		b.Logger.Debug("status report suppressed as a repeat of the recent one",
			slog.String("action", status.Action),
			slog.String("item", status.Item),
			slog.Bool("success", status.Success))
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

	prompt := BuildStatusPrompt(status)

	reply, err := b.AiClient.Ask(user, systemPrompt, prompt)
	if err != nil {
		b.Logger.Error("action status LLM call failed", slog.String("error", err.Error()), slog.String("user", user))
		return
	}
	parsed := ai.Parse(reply)
	message := parsed.CleanReply

	// The server already decided whether this action worked, and that verdict is
	// server-derived — it comes from the handler that watched the actual world
	// change. The model only writes the sentence around it. Sending the sentence
	// unchecked means the player reads the model, not the server: the prompt
	// contains the word "failed" and the model is free to write "Berhasil! Aku nemu
	// 12 diamond", and there is nothing here that notices.
	//
	// So the narration is compared against the verdict it is narrating, in both
	// directions. Claiming a success the server refused is a lie. Denying a
	// success the server confirmed is a different kind of damage, and the one a
	// filter can cause: it makes the bot contradict real results and teaches the
	// player to stop believing it.
	//
	// Which of the two happens is a setting, not a constant. The default is
	// shadow: every contradiction is counted and logged and the model still says
	// what it said, because the marker lists have never been run against real
	// model output and enforcing on an unmeasured filter risks trading a
	// possible lie for a certain one. ai.NarrationStats() is what turns that
	// guess into a number, and enforcing is then a decision made with evidence.
	outcome := ai.Outcome{
		Action:  status.Action,
		Item:    status.Item,
		Count:   status.Count,
		Success: status.Success,
		Error:   status.Error,
	}
	if message != "" && ai.IsNarrationLeak(message) {
		// The model answered with its working-out instead of the line. There is
		// no sentence here to check against the server's verdict and no sentence
		// worth reading aloud, so it is replaced rather than measured — in every
		// filter mode, including shadow, because shipping the model's notes to a
		// player in order to count them is not a trade worth making.
		b.Logger.Warn("status narration was the model's scratchpad, not a reply",
			slog.String("action", status.Action),
			slog.String("reply", message))
		message = ai.FallbackStatusMessage(outcome)
	}
	if message != "" && ai.IsSelfCorrection(message) {
		// The model narrated its own revision — it named the item, changed its
		// mind mid-sentence, and sent both. The log shows it as
		// "Logs/Cardboard... eh, Log sebanyak 10 biji", which is grammatical,
		// the right length, and agrees with the server; only the hesitation
		// gives it away. It is replaced with the plain sentence for the same
		// reason a scratchpad is: there is no line here worth reading aloud.
		b.Logger.Warn("status narration carried its own abandoned first draft",
			slog.String("action", status.Action),
			slog.String("reply", message))
		message = ai.FallbackStatusMessage(outcome)
	}
	if message != "" && ai.IsScriptCorrupted(message) {
		// The reply came back carrying text from another script, or an internal
		// field name where a word belongs: "10 batang kayu berhasil_subscription
		// 砍到手啦 wkwk". It is the right length, the right tone, and it agrees
		// with the server, so nothing else here objects — but the words in it are
		// not words a player should read.
		b.Logger.Warn("status narration carried foreign script or an internal identifier",
			slog.String("action", status.Action),
			slog.String("reply", message))
		message = ai.FallbackStatusMessage(outcome)
	}
	if message != "" {
		matched := ai.StatusFaithful(message, outcome)
		ai.RecordNarrationVerdict(true, matched, outcome)
		if !matched {
			b.Logger.Warn("status narration contradicted the server result",
				slog.String("action", status.Action),
				slog.Bool("server_success", status.Success),
				slog.String("filter_mode", ai.CurrentFilterMode().String()),
				slog.String("reply", message))
			if ai.CurrentFilterMode() == ai.ModeEnforce {
				// The player still gets a sentence — just one that matches what
				// happened. Dropping the reply entirely would be its own failure.
				message = ai.FallbackStatusMessage(outcome)
			}
		}
	}

	if message != "" {
		b.Logger.Info("action status reply sending", slog.String("reply", message))
		b.SendSafeChat(message)
	}
}

func BuildStatusPrompt(status event.ActionStatus) string {
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
