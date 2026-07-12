// Package bot provides the core Minecraft bot implementation.
package bot

import "strings"

func (b *Bot) FindItemSlotByName(name string) (uint32, bool) {
	inv := b.GetInventorySlots()
	names := b.GetItemNames()
	lowerName := strings.ToLower(name)

	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		itemName := names[item.NetworkID]
		if strings.Contains(strings.ToLower(itemName), lowerName) {
			return slot, true
		}
	}
	return 0, false
}
