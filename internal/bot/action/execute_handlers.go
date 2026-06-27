package action

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
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

func handleCraft(b *bot.Bot, param, user string) {
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
			b.ReportActionStatus(user, event.ActionStatus{Action: "craft", Item: itemName, Success: false, Error: "resep tidak diketahui"})
			return
		}
		if !recipeOK {
			// Server's CraftingData hasn't populated yet for this network ID;
			// shouldn't happen in practice but guard anyway.
			b.Logger.Warn("ExecuteAction: recipe net ID not in RecipesByNetID cache", "item", itemName, "id", recipeID)
			return
		}

		needsBench := recipeNeedsCraftingBench(recipe)
		if needsBench {
			b.Logger.Debug("Craft requires bench, ensuring crafting_table", "item", itemName, "block", recipe.Block)
			tablePos, ensured := b.InventoryMgr.Crafting().EnsureCraftingTable(ctx)
			if !ensured {
				b.ReportActionStatus(user, event.ActionStatus{Action: "craft", Item: itemName, Success: false, Error: "gak punya crafting table"})
				return
			}
			if err := b.InventoryMgr.Crafting().OpenCraftingTable(ctx, tablePos); err != nil {
				b.Logger.Warn("OpenCraftingTable failed", "err", err)
				b.ReportActionStatus(user, event.ActionStatus{Action: "craft", Item: itemName, Success: false, Error: "gagal buka crafting table"})
				return
			}
			defer b.InventoryMgr.Crafting().CloseWindow()
		} else {
			b.Logger.Debug("Inventory recipe (no bench needed)", "item", itemName)
		}

		// The count parameter is the number of OUTPUT ITEMS the player wants,
		// not the number of craft operations. Convert it to the number of craft
		// operations using the recipe's output count.
		outputPerCraft := int(recipe.Output.Count)
		crafts := computeCrafts(count, outputPerCraft)
		b.Logger.Debug("Executing craft action", "item", itemName, "recipeID", recipeID, "desired_count", count, "crafts", crafts)
		if err := b.CraftItem(recipeID, crafts); err != nil {
			b.Logger.Warn("CraftItem failed", "err", err, "item", itemName)
			b.ReportActionStatus(user, event.ActionStatus{Action: "craft", Item: itemName, Count: count, Success: false, Error: err.Error()})
			return
		}
		// Report actual output produced (crafts * outputPerCraft, capped at 64).
		actualOutput := outputPerCraft * crafts
		if actualOutput > 64 {
			actualOutput = 64
		}
		b.ReportActionStatus(user, event.ActionStatus{Action: "craft", Item: itemName, Count: actualOutput, Success: true})
	}()
}

// recipeNeedsCraftingBench determines whether a recipe truly requires a 3×3
// crafting table. Many 2×2 recipes (oak_planks, sticks, crafting_table) can be
// made in the player's personal 2×2 inventory grid even if the server tags them
// with Block="crafting_table". We use the recipe shape/dimensions as the
// ground truth.
func recipeNeedsCraftingBench(recipe bot.RecipeInfo) bool {
	if recipe.Block == "" {
		return false
	}

	// Non-crafting-table blocks (furnace, stonecutter, cartography_table,
	// blast_furnace, etc.) require their own special interface. We don't
	// attempt to craft those in the 2×2 inventory grid.
	if recipe.Block != "crafting_table" {
		return true
	}

	// Block == "crafting_table". If the recipe actually fits in a 2×2 grid
	// (inventory recipes such as oak_planks, sticks, crafting_table) we can
	// bypass the bench. Some servers tag these recipes with
	// Block="crafting_table" even though the vanilla client allows them in
	// the personal crafting grid.
	if recipe.Shapeless {
		return len(recipe.Ingredients) > 4
	}
	return recipe.Width > 2 || recipe.Height > 2
}

// computeCrafts converts a desired number of output items into the number of
// craft operations needed, given how many items the recipe produces per craft.
func computeCrafts(desiredCount, outputPerCraft int) int {
	if desiredCount <= 0 {
		return 1
	}
	if outputPerCraft <= 0 {
		outputPerCraft = 1
	}
	crafts := (desiredCount + outputPerCraft - 1) / outputPerCraft
	if crafts <= 0 {
		return 1
	}
	return crafts
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

func handleDrop(b *bot.Bot, param, user string) {
	go func() {
		if strings.TrimSpace(param) == "" {
			return
		}
		parts := strings.Split(param, ",")
		itemName := normalizeItemName(parts[0])
		count := 0
		if len(parts) >= 2 {
			_, _ = fmt.Sscanf(parts[1], "%d", &count)
		}
		if itemName == "" {
			return
		}

		// --- Aim at the player before dropping ------------------------------
		// Bedrock's drop direction comes from the last PlayerAuthInput yaw/pitch.
		// Without aiming first, the item flies wherever the idle look loop left
		// the bot facing — sometimes backward, sometimes at the bot's feet
		// (where it gets instantly re-collected). We replicate the same
		// aim → sync → pitch-override → drop → step-back flow used by
		// GiveItem in chest/actions.go.

		target := user
		if target == "" {
			// Fall back to whoever the bot is currently following.
			b.Mu.Lock()
			target = b.TargetPlayerName
			b.Mu.Unlock()
		}

		var playerPos mgl32.Vec3
		var foundPlayer bool
		if target != "" {
			_, pPos, ok := b.FindPlayer(target)
			if ok {
				playerPos = pPos
				foundPlayer = true
			}
		}

		if foundPlayer {
			botPos := b.GetCoords()
			dist := float32(math.Sqrt(float64(
				(playerPos.X()-botPos.X())*(playerPos.X()-botPos.X()) +
					(playerPos.Z()-botPos.Z())*(playerPos.Z()-botPos.Z()))))

			// Stand within ~1.3 blocks so the tossed item lands inside the
			// player's pickup range. Bedrock's base drop velocity is only
			// ~0.3 m/s — the item can't travel far on its own.
			if dist > 2.0 {
				reached := b.NavigateToBlock(
					int32(math.Floor(float64(playerPos.X()))),
					int32(math.Floor(float64(playerPos.Y()))),
					int32(math.Floor(float64(playerPos.Z()))),
					1.3,
				)
				if !reached {
					b.Logger.Warn("handleDrop: could not reach player, dropping in place", "target", target)
				}
			}

			// Force-stop so the look loop stops interpolating yaw toward path
			// direction and instead aims at the player.
			b.StopMovement()

			// Re-fetch position after stop.
			botPos = b.GetCoords()
			targetHead := playerPos.Add(mgl32.Vec3{0, 1.62, 0})

			// Pin the look target at the player's head for 3s so the movement
			// loop continuously interpolates toward this point.
			b.LookAt(targetHead)

			// Compute the yaw the look loop will converge to and wait for the
			// next PlayerAuthInput tick to transmit it.
			dx := targetHead.X() - botPos.X()
			dz := targetHead.Z() - botPos.Z()
			yaw := float32(math.Atan2(float64(dz), float64(dx))*180/math.Pi) - 90
			for yaw < 0 {
				yaw += 360
			}
			b.WaitForYawSync(yaw, 800*time.Millisecond)

			// Force-set both body yaw AND head yaw to the exact target, plus
			// a slight upward pitch so the item arcs forward into the player's
			// pickup radius. SetLookAngles pins the body Yaw (which Bedrock
			// uses for drop direction) instead of leaving it lagging behind
			// HeadYaw through eased interpolation.
			b.SetLookAngles(yaw, -28)
			time.Sleep(120 * time.Millisecond)
		}

		// --- Drop the item --------------------------------------------------
		if err := b.InventoryMgr.DropItem(itemName, count); err != nil {
			b.Logger.Warn("handleDrop: DropItem failed", "item", itemName, "error", err)
			return
		}

		if foundPlayer {
			// Brief pause so the drop transaction lands and the entity is on
			// the ground before we step back. Without this, the bot's
			// immediate backward step can re-collect the item.
			time.Sleep(150 * time.Millisecond)

			// Step back a short distance so the dropped item ends up outside
			// the bot's pickup radius. We move opposite to the player direction.
			botPos := b.GetCoords()
			dx := playerPos.X() - botPos.X()
			dz := playerPos.Z() - botPos.Z()
			hLen := float32(math.Sqrt(float64(dx*dx + dz*dz)))
			if hLen > 0.001 {
				backPos := mgl32.Vec3{
					botPos.X() - (dx/hLen)*1.2,
					botPos.Y(),
					botPos.Z() - (dz/hLen)*1.2,
				}
				b.NavigateTo(backPos)
				time.Sleep(500 * time.Millisecond)
				b.StopMovement()
			}
		}

		b.Logger.Debug("handleDrop complete", "item", itemName, "count", count, "target", target)
	}()
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
