package action

import (
	"strings"

	"bedrock-ai/internal/bot"
)

// handleListCraftable returns all recipes bot can craft.
// param: "all" or "available" (default: available only)
func handleListCraftable(b *bot.Bot, param, user string) {
	showAll := strings.Contains(strings.ToLower(param), "all")

	// The snapshot accessors, not the raw fields. Aliasing b.InventoryMap and
	// iterating it reads as the bug it used to be in ListCraftableItems, where
	// the unlock came before the loop and the map detector killed the process.
	// Here the iteration happens to be inside the lock, but the accessors copy
	// first so that stops being load-bearing.
	inv := b.GetInventorySlots()
	names := b.GetItemNames()
	hasCraftingTable := false
	for _, stack := range inv {
		if stack.Count > 0 {
			name := names[stack.NetworkID]
			if strings.Contains(strings.ToLower(name), "crafting_table") {
				hasCraftingTable = true
				break
			}
		}
	}

	craftable := b.ListCraftableItems(hasCraftingTable)

	// Filter if not showing all
	if !showAll {
		filtered := craftable[:0]
		for _, item := range craftable {
			if item.CanCraft {
				filtered = append(filtered, item)
			}
		}
		craftable = filtered
	}

	// Build response message
	var msg strings.Builder
	if len(craftable) == 0 {
		msg.WriteString("Nggak ada yang bisa aku craft sekarang.")
		if !hasCraftingTable {
			msg.WriteString(" (Butuh crafting table dulu)")
		}
	} else {
		if hasCraftingTable {
			msg.WriteString("Yang bisa aku craft (3x3):\n")
		} else {
			msg.WriteString("Yang bisa aku craft (2x2, belum ada crafting table):\n")
		}

		count := 0
		for _, item := range craftable {
			if count >= bot.ListCraftableLimit {
				msg.WriteString("... (dan lainnya)")
				break
			}
			if item.CanCraft {
				msg.WriteString("✓ ")
			} else {
				msg.WriteString("✗ ")
			}
			msg.WriteString(item.Name)
			if !item.CanCraft && item.Missing != "" {
				msg.WriteString(" (butuh: ")
				msg.WriteString(item.Missing)
				msg.WriteString(")")
			}
			msg.WriteString("\n")
			count++
		}
	}

	b.SendChat(msg.String())
	b.Logger.Info("handleListCraftable", "total", len(craftable), "has_table", hasCraftingTable)
}
