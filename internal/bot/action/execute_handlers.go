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

	go func() {
		ctx := context.Background()

		b.Mu.Lock()
		recipeID, ok := b.Recipes[itemName]
		if !ok {
			recipeID, ok = b.Recipes["minecraft:"+itemName]
		}
		recipe, recipeOK := b.RecipesByNetID[recipeID]
		b.Mu.Unlock()

		if !ok {
			b.Logger.Warn("ExecuteAction: recipe not found for item", "item", itemName)
			b.SendChat("Maaf, aku belum tau resep buat " + itemName + ".")
			return
		}
		if !recipeOK {
			// Server's CraftingData hasn't populated yet for this network ID;
			// shouldn't happen in practice but guard anyway.
			b.Logger.Warn("ExecuteAction: recipe net ID not in RecipesByNetID cache", "item", itemName, "id", recipeID)
			return
		}

		// 2×2 inventory recipes (recipe.Block == "") work without a bench.
		// 3×3 recipes require an open crafting_table window or strict servers
		// silently drop the StackRequest.
		if recipe.Block != "" {
			b.Logger.Debug("Craft requires bench, ensuring crafting_table", "item", itemName, "block", recipe.Block)
			tablePos, ensured := b.InventoryMgr.Crafting().EnsureCraftingTable(ctx)
			if !ensured {
				return // EnsureCraftingTable already messaged the user.
			}
			if err := b.InventoryMgr.Crafting().OpenCraftingTable(ctx, tablePos); err != nil {
				b.Logger.Warn("OpenCraftingTable failed", "err", err)
				b.SendChat("Aku gagal buka crafting_table.")
				return
			}
			defer b.InventoryMgr.Crafting().CloseWindow()
		} else {
			b.Logger.Debug("Inventory recipe (no bench needed)", "item", itemName)
		}

		b.Logger.Debug("Executing craft action", "item", itemName, "recipeID", recipeID, "count", count)
		if err := b.CraftItem(recipeID, count); err != nil {
			b.Logger.Warn("CraftItem failed", "err", err, "item", itemName)
			b.SendChat("Gagal craft " + itemName + ": " + err.Error())
			return
		}
		b.SendChat(fmt.Sprintf("Selesai craft %d %s!", count, itemName))
	}()
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

// isWoodLike reports whether an item name refers to a log/wood block that
// should be harvested via the tree-committed chopper rather than the
// per-block scanner. Recognizes vanilla wood variants and Indonesian aliases
// post-normalization.
func isWoodLike(itemName string) bool {
	n := strings.ToLower(itemName)
	if strings.Contains(n, "log") || strings.Contains(n, "wood") {
		return true
	}
	switch n {
	case "kayu", "oak", "birch", "spruce", "jungle", "acacia", "dark_oak", "mangrove", "cherry", "crimson_stem", "warped_stem":
		return true
	}
	return false
}

func normalizeItemName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.TrimPrefix(name, "minecraft:")
	switch name {
	case "craftingtable", "craft_table", "workbench":
		return "crafting_table"
	case "wood", "kayu", "log", "logs":
		return "oak_log"
	case "plank", "planks", "papan":
		return "oak_planks"
	case "tanah":
		return "dirt"
	case "batu":
		return "stone"
	case "pasir":
		return "sand"
	case "gandum", "wheat_crop":
		return "wheat"
	case "wortel":
		return "carrot"
	case "kentang":
		return "potato"
	case "sapi", "cow_animal":
		return "cow"
	case "domba", "sheep_animal":
		return "sheep"
	case "babi", "pig_animal":
		return "pig"
	case "ayam", "chicken_animal":
		return "chicken"
	case "serigala", "dog":
		return "wolf"
	case "kucing":
		return "cat"
	}
	return name
}

func normalizeCropType(param string) string {
	parts := strings.Split(param, ",")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	crop := strings.ToLower(strings.TrimSpace(parts[0]))
	switch crop {
	case "gandum", "wheat_crop":
		return "wheat"
	case "wortel":
		return "carrot"
	case "kentang":
		return "potato"
	case "bit", "beet":
		return "beetroot"
	case "labu":
		return "pumpkin"
	case "semangka":
		return "melon"
	case "tebu", "sugarcane":
		return "sugar_cane"
	default:
		return crop
	}
}
