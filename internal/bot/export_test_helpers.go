package bot

import "bedrock-ai/internal/event"

// Test-support accessors.
//
// The repeat-suppression window for status reports is unexported and guarded by
// the bot's mutex on purpose: it is a pacing decision, not part of the bot's
// surface. A black-box test in tests/bot needs to ask "would this report be
// spoken right now" without being able to hand the bot a fake clock or rewrite
// the window, so the gate is exposed and the window is not.
func (b *Bot) ShouldSpeakStatusForTest(sig string) bool {
	return b.shouldSpeakStatus(sig)
}

// StatusSignatureForTest exposes the signature function so a test can assert
// that two reports the bot would collapse together really do share a signature,
// instead of asserting the collapse itself and passing by accident.
func StatusSignatureForTest(sig event.ActionStatus) string {
	return statusSignature(sig)
}
