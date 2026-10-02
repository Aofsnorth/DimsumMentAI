// Test-support accessors for the chat package.
//
// The fallback matcher is the last thing between a player saying "ke tempat ku"
// and the bot walking there, and it is unexported because it is a detail of the
// chat pipeline. These wrappers are how a black-box test in tests/bot/chat can
// ask the question the pipeline asks without a live connection or a model.

package chat

import "bedrock-ai/internal/bot/action"

// MovementActionsForTest exposes the movement-intent matcher.
func MovementActionsForTest(msg string) []action.Step {
	return fallbackMovementActions(msg)
}

// IsStopFollowingIntentForTest exposes the opt-out matcher.
func IsStopFollowingIntentForTest(msg string) bool {
	return isStopFollowingIntent(msg)
}
