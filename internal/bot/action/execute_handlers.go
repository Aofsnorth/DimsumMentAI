package action

import (
	"context"
	"fmt"
	"math"
	"strings"

	"bedrock-ai/internal/bot"
)

func handleAttack(b *bot.Bot, param, user string) {
	b.Mu.Lock()
	targetID := uint64(0)
	closestDist := float32(math.MaxFloat32)
	botPos := b.Pos

	for username, id := range b.PlayerEntityIDs {
		if strings.EqualFold(username, param) || (param == "" && strings.EqualFold(username, user)) {
			targetID = id
			break
		}
	}

	if targetID == 0 {
		for id, actor := range b.Actors {
			if param == "" || strings.Contains(strings.ToLower(actor.Name), strings.ToLower(param)) || strings.Contains(strings.ToLower(actor.Type), strings.ToLower(param)) {
				dx := actor.Position.X() - botPos.X()
				dy := actor.Position.Y() - botPos.Y()
				dz := actor.Position.Z() - botPos.Z()
				dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
				if dist < closestDist {
					closestDist = dist
					targetID = id
				}
			}
		}
	}
	b.Mu.Unlock()

	if targetID != 0 {
		b.CombatMgr.EngageTarget(targetID)
	} else {
		b.Logger.Warn("ExecuteAction: no target found to attack", "param", param)
	}
}

func handleCraft(b *bot.Bot, param string) {
	if strings.TrimSpace(param) == "" {
		return
	}
	parts := strings.Split(param, ",")
	itemName := normalizeItemName(parts[0])
	count := 1
	if len(parts) >= 2 {
		_, _ = fmt.Sscanf(parts[1], "%d", &count)
	}

	b.Mu.Lock()
	recipeID, ok := b.Recipes[itemName]
	if !ok {
		recipeID, ok = b.Recipes["minecraft:"+itemName]
	}
	b.Mu.Unlock()

	if ok {
		b.Logger.Debug("Executing craft action", "item", itemName, "recipeID", recipeID, "count", count)
		_ = b.CraftItem(recipeID, count)
	} else {
		b.Logger.Warn("ExecuteAction: recipe not found for item", "item", itemName)
		var guessed uint32
		if _, err := fmt.Sscanf(itemName, "%d", &guessed); err == nil && guessed != 0 {
			_ = b.CraftItem(guessed, count)
		}
	}
}

func handleTake(b *bot.Bot, param, user string) {
	go func() {
		if strings.TrimSpace(param) == "" {
			return
		}
		parts := strings.Split(param, ",")
		itemName := normalizeItemName(parts[0])
		count := int32(0)
		if len(parts) >= 2 {
			var parsed int
			if _, err := fmt.Sscanf(parts[1], "%d", &parsed); err == nil {
				count = int32(parsed)
			}
		}
		success := b.InventoryMgr.Chest().GiveItem(context.Background(), itemName, user, count)
		b.Logger.Debug("take action complete", "success", success, "item", itemName)
	}()
}

func handleGive(b *bot.Bot, param, user string) {
	go func() {
		if strings.TrimSpace(param) == "" {
			return
		}
		parts := strings.Split(param, ",")
		itemName := normalizeItemName(parts[0])
		count := int32(0)
		if len(parts) >= 2 {
			var parsed int
			if _, err := fmt.Sscanf(parts[1], "%d", &parsed); err == nil {
				count = int32(parsed)
			}
		}
		success := b.InventoryMgr.Chest().GiveItem(context.Background(), itemName, user, count)
		b.Logger.Debug("give action complete", "success", success, "item", itemName)
	}()
}

func handleDrop(b *bot.Bot, param string) {
	if strings.TrimSpace(param) == "" {
		return
	}
	parts := strings.Split(param, ",")
	itemName := normalizeItemName(parts[0])
	count := 0
	if len(parts) >= 2 {
		_, _ = fmt.Sscanf(parts[1], "%d", &count)
	}
	if itemName != "" {
		_ = b.InventoryMgr.DropItem(itemName, count)
	}
}

func normalizeItemName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.TrimPrefix(name, "minecraft:")
	switch name {
	case "craftingtable", "craft_table", "workbench":
		return "crafting_table"
	case "wood":
		return "oak_log"
	case "plank", "planks":
		return "oak_planks"
	}
	return name
}
