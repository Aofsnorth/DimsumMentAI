package bot

import (
	"strings"
	"time"

	"bedrock-ai/internal/event"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

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

// SetWhitelistPathForTest points the in-game whitelist at a test file. An
// empty path keeps the list in memory only, and resetting whitelistLoaded
// makes the next check load from the new path rather than remembering the
// old one's answer.
func (b *Bot) SetWhitelistPathForTest(path string) {
	b.whitelistMu.Lock()
	b.whitelistPath = path
	b.whitelistLoaded = false
	b.whitelistMu.Unlock()
}

// StatusSignatureForTest exposes the signature function so a test can assert
// that two reports the bot would collapse together really do share a signature,
// instead of asserting the collapse itself and passing by accident.
func StatusSignatureForTest(sig event.ActionStatus) string {
	return statusSignature(sig)
}

// AgeWhisperConversationForTest backdates the open whisper conversation so a
// test can assert the window closes it without sleeping for the real duration.
// The window itself stays unexported, for the same reason as above: it is a
// pacing decision, not bot surface.
func (b *Bot) AgeWhisperConversationForTest(d time.Duration) {
	b.Mu.Lock()
	b.whisperPartnerAt = b.whisperPartnerAt.Add(-d)
	b.Mu.Unlock()
}

// SetHeldSlotForTest points the bot at a hotbar slot so a test can state which
// item a placement or an equip would act on.
func (b *Bot) SetHeldSlotForTest(slot uint32) {
	b.Mu.Lock()
	b.HeldSlot = slot
	b.Mu.Unlock()
}

// SetInventorySlotForTest seeds the inventory the way the server would, so a
// test can assert what a send path puts on the wire from a realistic stack
// rather than an empty one.
func (b *Bot) SetInventorySlotForTest(slot uint32, stack protocol.ItemStack) {
	b.Mu.Lock()
	if b.InventoryMap == nil {
		b.InventoryMap = make(map[uint32]protocol.ItemStack)
	}
	b.InventoryMap[slot] = stack
	b.Mu.Unlock()
}

// SetStackNetworkIDForTest seeds the stack ID that goes with a slot.
func (b *Bot) SetStackNetworkIDForTest(slot uint32, id int32) {
	b.Mu.Lock()
	if b.StackNetworkIDs == nil {
		b.StackNetworkIDs = make(map[uint32]int32)
	}
	b.StackNetworkIDs[slot] = id
	b.Mu.Unlock()
}

// WhisperRouteForTest reports how SendReply would route a reply to this user:
// "whisper", "public", or "public" when the message would be dropped. It is the
// routing decision without the send, so a test can assert privacy without a
// connection.
func (b *Bot) WhisperRouteForTest(user string) string {
	if strings.TrimSpace(user) == "" {
		return "public"
	}
	if b.whisperMatches(user) {
		return "whisper"
	}
	return "public"
}
