package chat

import (
	"fmt"
	"log/slog"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
)

// stateReplyRules tells the model what kind of sentence this is, which is the
// one thing a generic chat prompt cannot say: the bot has already changed what
// it is doing, and nothing in the reply runs.
const stateReplyRules = `
[STATE CHANGE REPLY]
You have just changed what you are doing, in response to the player's last
message. Say so in one short sentence, in the language and tone you already use.
- Report ONLY the change named below. Do not mention any other state, task, item or mob.
- DO NOT emit <action>, <plan> or <followup> tags. Nothing written here is executed.
- DO NOT ask a follow-up question and DO NOT offer to start new work.
- DO NOT repeat the player's own words back verbatim.
- If the change genuinely needs no words, answer exactly <silent/>.
`

// replyViaAI asks the model to word the acknowledgement of a state change the
// bot has already applied, and sends whatever it wrote.
//
// It used to be a fixed sentence per branch ("Siap, aku berhenti ikutin."),
// which is the same line every time a player asks for the same thing, and which
// says nothing about what the bot thinks it is doing now. The state change
// itself stays in code — that must be deterministic — but the sentence about it
// belongs to the model.
//
// Nothing is sent when the model fails or declines. The change is already
// visible in the world: a bot that stopped following is standing still, and a
// canned reassurance it did not mean would be the worse outcome.
func replyViaAI(b *bot.Bot, user, playerMsg, change string) {
	msg := stateReplyText(b, user, playerMsg, change)
	if msg == "" {
		b.Logger.Info("state change not narrated",
			slog.String("user", user),
			slog.String("change", change),
		)
		return
	}
	b.Logger.Info("state change reply sending", slog.String("reply", msg))
	b.SendReply(user, msg)
}

// stateReplyText is the model's half of replyViaAI: it returns the sentence to
// send, or "" when there is nothing worth sending.
func stateReplyText(b *bot.Bot, user, playerMsg, change string) string {
	if b.AiClient == nil {
		b.Logger.Info("state change reply skipped: AI client not configured",
			slog.String("user", user), slog.String("change", change))
		return ""
	}

	systemPrompt := buildChatContext(b, user, playerMsg, b.Name) + stateReplyRules
	prompt := fmt.Sprintf(
		"[STATE CHANGE] %s\nThe player said: %q\nReply now, in one short sentence.",
		change, playerMsg)

	reply, err := b.AiClient.Ask(user, systemPrompt, prompt)
	if err != nil {
		b.Logger.Error("state change reply LLM call failed",
			slog.String("error", err.Error()), slog.String("user", user))
		return ""
	}
	if isSilentResponse(reply) {
		return ""
	}

	// Parse rather than sending the raw string: the model may still emit an
	// action tag, and this path deliberately executes nothing. Parse strips the
	// tags, so the tag cannot become a task and cannot reach the player as text.
	msg := ai.Parse(reply).CleanReply
	if msg == "" {
		return ""
	}

	// The same three defects the status path refuses to ship. None of them are
	// wrong facts here, but all three are text a player should not read.
	if ai.IsNarrationLeak(msg) || ai.IsSelfCorrection(msg) || ai.IsScriptCorrupted(msg) {
		b.Logger.Warn("state change reply was unusable, dropped",
			slog.String("change", change), slog.String("reply", msg))
		return ""
	}
	return msg
}
