package gathering

import (
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func inventoryCountMatching(inv map[uint32]protocol.ItemStack, names map[int32]string, requested string) int {
	total := 0
	for _, item := range inv {
		if item.Count == 0 {
			continue
		}
		name := names[item.NetworkID]
		if itemNameMatches(name, requested) {
			total += int(item.Count)
		}
	}
	return total
}

func itemNameMatches(itemName, requested string) bool {
	itemName = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(itemName), "minecraft:"))
	requested = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(requested), "minecraft:"))
	if itemName == "" || requested == "" {
		return requested == ""
	}
	return itemName == requested || strings.Contains(itemName, requested)
}
