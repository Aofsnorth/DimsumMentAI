// Test-support accessors for the chat package.
//
// The fallback matcher is the last thing between a player saying "ke tempat ku"
// and the bot walking there, and it is unexported because it is a detail of the
// chat pipeline. These wrappers are how a black-box test in tests/bot/chat can
// ask the question the pipeline asks without a live connection or a model.

package chat

import (
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/event"
)

// MovementActionsForTest exposes the movement-intent matcher.
func MovementActionsForTest(msg string) []action.Step {
	return fallbackMovementActions(msg)
}

// IsStopFollowingIntentForTest exposes the opt-out matcher.
func IsStopFollowingIntentForTest(msg string) bool {
	return isStopFollowingIntent(msg)
}

// IsStopAttackIntentForTest exposes the combat opt-out matcher.
func IsStopAttackIntentForTest(msg string) bool {
	return isStopAttackIntent(msg)
}

// IsAffirmativeReplyForTest exposes the reply matcher that decides whether a
// chatty LLM committed to an action without emitting an <action> tag.
//
// It is the difference between "the model agreed" and "the substring 'ya '
// appears somewhere", which is not the same question. A refusal has to lose.
func IsAffirmativeReplyForTest(reply string) bool { return isAffirmativeReply(reply) }

// InferActionIntentForTest exposes the synthesised-action path end to end, so a
// test can assert on the parameter it produces rather than only on whether a
// step came back.
func InferActionIntentForTest(msg, reply string) []action.Step {
	return inferActionIntent(msg, reply)
}

// IsTaggedInMessageForTest exposes the tag matcher for test assertions.
func IsTaggedInMessageForTest(msg, botName string, extraTags ...string) bool {
	return isTaggedInMessage(msg, botName, extraTags...)
}

// IsTaggedInBotForTest exposes the bot-aware tag matcher for test assertions.
func IsTaggedInBotForTest(msg string, b *bot.Bot) bool {
	return isTaggedInBot(msg, b)
}

// ShouldProcessChatForTest exposes shouldProcessChat for test assertions.
func ShouldProcessChatForTest(b *bot.Bot, evt event.ChatEvent, msg, botName string) bool {
	return shouldProcessChat(b, evt, msg, botName)
}

// HandleWhitelistCommandForTest exposes the in-game whitelist command, so a
// test can drive the grant end to end — including the main-player guard —
// without a live connection. A nil AiClient makes the reply a no-op, which is
// what a test wants: the state change is what is under test.
func HandleWhitelistCommandForTest(b *bot.Bot, evt event.ChatEvent, msg string) bool {
	return handleWhitelistCommand(b, evt, msg)
}

// ParseWhitelistIntentForTest exposes the whitelist phrase parser.
func ParseWhitelistIntentForTest(msg string) (name string, remove bool, ok bool) {
	return parseWhitelistIntent(msg)
}

// ParseStagedCommandForTest exposes the staged-routine parser, so a test can
// assert the exact step sequence and counts without executing anything.
func ParseStagedCommandForTest(msg string) (steps []action.Step, summary string, ok bool) {
	return parseStagedCommand(msg)
}

// HandleGuardCommandForTest exposes the in-game guardian toggle, so a test can
// drive the enable/disable end to end — including the main-player guard —
// without a live connection. A nil AiClient makes the reply a no-op, which is
// what a test wants: the state change is what is under test.
func HandleGuardCommandForTest(b *bot.Bot, evt event.ChatEvent, msg string) bool {
	return handleGuardCommand(b, evt, msg)
}

// ParseGuardIntentForTest exposes the guard phrase parser.
func ParseGuardIntentForTest(msg string) (enable bool, ok bool) {
	return parseGuardIntent(msg)
}

// StateReplyTextForTest exposes the model's half of a state-change reply, so a
// test can assert what would be sent without a live connection.
func StateReplyTextForTest(b *bot.Bot, user, playerMsg, change string) string {
	return stateReplyText(b, user, playerMsg, change)
}
