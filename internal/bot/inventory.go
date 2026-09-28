// Package bot provides the core Minecraft bot implementation.
package bot

import "strings"

// FindItemSlotByName returns the slot holding an item whose name contains the
// given text.
//
// Slots are walked in index order rather than by ranging the map. Go randomises
// map iteration on purpose, so a range here returned a different slot each call
// for a bot holding the same item twice — and that is the ghost-item bug in its
// purest form: the bot drops what it thinks is one stack, the server updates
// only that slot, the other stack is still sitting there, and the next command
// to drop oak log takes that one too. The player ends up with what the bot
// dropped and the bot's own view of its inventory no longer matching either.
//
// Determinism is worth a linear scan of thirty-six slots.
func (b *Bot) FindItemSlotByName(name string) (uint32, bool) {
	inv := b.GetInventorySlots()
	names := b.GetItemNames()
	lowerName := strings.ToLower(name)

	// The hotbar and main inventory are 0-35. Hotbar first is deliberate: the
	// held slot is the one the player can see and act on, and a bot that keeps
	// reaching past the hotbar for a stack is a bot that is harder to follow.
	for slot := uint32(0); slot < 36; slot++ {
		item, held := inv[slot]
		if !held || item.Count <= 0 {
			continue
		}
		itemName := names[item.NetworkID]
		if strings.Contains(strings.ToLower(itemName), lowerName) {
			return slot, true
		}
	}
	return 0, false
}
