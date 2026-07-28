// Package action provides helper handlers and normalisation utilities for
// the action dispatch in execute.go.
package action

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"

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
		b.Logger.Debug("Executing craft action", "item", itemName, "desired_count", count)
		actual, err := craftChain(ctx, b, user, itemName, count, 0)
		if err != nil {
			b.Logger.Warn("CraftItem failed", "err", err, "item", itemName)
			b.ReportActionStatus(user, event.ActionStatus{Action: "craft", Item: itemName, Count: count, Success: false, Error: err.Error()})
			return
		}
		b.ReportActionStatus(user, event.ActionStatus{Action: "craft", Item: itemName, Count: actual, Success: true})
	}()
}

// maxCraftDepth bounds chain-crafting recursion (e.g. oak_log -> oak_planks ->
// stick is depth 2) so a malformed recipe graph can never loop forever.
const maxCraftDepth = 4

// ingredientFallbacks maps a generic/tag ingredient keyword to concrete
// craftable items, tried in order, so chain-crafting can satisfy e.g.
// a "planks" requirement by making oak_planks from oak_log.
//
// Specific plank variants (warped_planks, crimson_planks) are normalised to
// the "planks" key via normalizeIngredientKey so they resolve here too.
var ingredientFallbacks = map[string][]string{
	"planks": {"oak_planks", "spruce_planks", "birch_planks", "jungle_planks", "acacia_planks", "dark_oak_planks", "mangrove_planks", "cherry_planks"},
}

// craftChain crafts `count` of itemName, first chain-crafting any missing
// ingredients that themselves have known recipes. It returns the number of
// output items actually produced.
func craftChain(ctx context.Context, b *bot.Bot, user, itemName string, count, depth int) (int, error) {
	if depth > maxCraftDepth {
		return 0, fmt.Errorf("rantai craft terlalu dalam untuk %s", itemName)
	}

	recipeID, recipe, ok := pickBestRecipe(b, itemName)
	if !ok {
		return 0, fmt.Errorf("resep tidak diketahui: %s", itemName)
	}

	// 1. Ensure missing ingredients by chain-crafting them first.
	if err := ensureCraftIngredients(ctx, b, user, recipe, count, depth); err != nil {
		return 0, err
	}

	// 2. Bench handling.
	if recipeNeedsCraftingBench(recipe) {
		b.Logger.Debug("Craft requires bench, ensuring crafting_table", "item", itemName, "block", recipe.Block)
		tablePos, ensured := b.InventoryMgr.Crafting().EnsureCraftingTable(ctx)
		if !ensured {
			return 0, fmt.Errorf("gak punya crafting table")
		}
		if err := b.InventoryMgr.Crafting().OpenCraftingTable(ctx, tablePos); err != nil {
			b.Logger.Warn("OpenCraftingTable failed", "err", err)
			return 0, fmt.Errorf("gagal buka crafting table")
		}
		defer b.InventoryMgr.Crafting().CloseWindow()
	} else {
		b.Logger.Debug("Inventory recipe (no bench needed)", "item", itemName)
	}

	// 3. Craft. `count` is desired OUTPUT items; convert to craft operations.
	outputPerCraft := int(recipe.Output.Count)
	crafts := computeCrafts(count, outputPerCraft)
	b.Logger.Debug("chain craft step", "item", itemName, "recipeID", recipeID, "crafts", crafts, "depth", depth)
	if err := b.CraftItem(recipeID, crafts); err != nil {
		return 0, err
	}
	actual := outputPerCraft * crafts
	if actual > 64 {
		actual = 64
	}
	return actual, nil
}

// pickBestRecipe selects the recipe network ID for itemName whose ingredients
// the bot can most readily satisfy. Many items (e.g. "stick") have one recipe
// per wood variant; the server-sent Recipes map keeps only the last one, so we
// scan all candidates and prefer one the bot can either satisfy directly from
// inventory or via a known plank fallback. Returns the chosen ID, its recipe
// info, and ok=false when no recipe is known at all.
func pickBestRecipe(b *bot.Bot, itemName string) (uint32, bot.RecipeInfo, bool) {
	candidates := b.GetRecipeCandidates(itemName)
	if len(candidates) == 0 {
		return 0, bot.RecipeInfo{}, false
	}
	byNetID := b.GetRecipesByNetID()

	var fallbackID uint32
	var fallbackInfo bot.RecipeInfo
	haveFallback := false
	bestScore := -1
	var bestID uint32
	var bestInfo bot.RecipeInfo

	for _, id := range candidates {
		info, ok := byNetID[id]
		if !ok {
			continue
		}
		if !haveFallback {
			fallbackID, fallbackInfo, haveFallback = id, info, true
		}
		score := recipeSatisfactionScore(b, info)
		if score > bestScore {
			bestScore = score
			bestID, bestInfo = id, info
		}
	}

	if bestScore >= 0 {
		return bestID, bestInfo, true
	}
	if haveFallback {
		return fallbackID, fallbackInfo, true
	}
	return 0, bot.RecipeInfo{}, false
}

// recipeSatisfactionScore rates how ready the bot is to craft a recipe:
//   2 = every ingredient is already in inventory
//   1 = every missing ingredient is a plank variant the bot can chain-craft
//   0 = otherwise (still craftable in principle, lowest preference)
func recipeSatisfactionScore(b *bot.Bot, recipe bot.RecipeInfo) int {
	allHave := true
	allHaveOrPlank := true
	for _, ing := range recipe.Ingredients {
		name := b.IngredientName(ing)
		if name == "" {
			continue
		}
		need := int(ing.Count)
		checkName := name
		if strings.HasSuffix(strings.ToLower(name), "_planks") {
			checkName = "planks"
		}
		if b.CountItemLike(checkName) >= need {
			continue
		}
		allHave = false
		// A missing plank ingredient is fine as long as the bot has some log
		// or planks it can convert.
		if checkName == "planks" && (b.CountItemLike("planks") > 0 || b.CountItemLike("log") > 0) {
			continue
		}
		allHaveOrPlank = false
	}
	switch {
	case allHave:
		return 2
	case allHaveOrPlank:
		return 1
	default:
		return 0
	}
}

// ensureCraftIngredients chain-crafts any recipe ingredient the bot does not
// already have enough of, so multi-tier items (stick <- planks <- log) craft
// from raw materials in one request.
func ensureCraftIngredients(ctx context.Context, b *bot.Bot, user string, recipe bot.RecipeInfo, crafts, depth int) error {
	for _, ing := range recipe.Ingredients {
		name := b.IngredientName(ing)
		if name == "" {
			continue
		}
		need := int(ing.Count) * crafts
		// For plank-variant ingredients (warped_planks, oak_planks, etc.) any
		// plank type satisfies the recipe. Count all planks together so we
		// don't try to craft a specific variant the bot can't make.
		checkName := name
		if strings.HasSuffix(strings.ToLower(name), "_planks") {
			checkName = "planks"
		}
		if need <= 0 || b.CountItemLike(checkName) >= need {
			continue
		}
		b.Logger.Debug("chain craft: ingredient missing, crafting it", "ingredient", name, "need", need)
		if err := craftIngredient(ctx, b, user, name, need, depth); err != nil {
			return err
		}
	}
	return nil
}

// craftIngredient crafts `need` of an ingredient, resolving generic/tag
// ingredients (e.g. "planks") to a concrete craftable variant the bot can
// actually make.
func craftIngredient(ctx context.Context, b *bot.Bot, user, name string, need, depth int) error {
	candidates := []string{name}
	if fb, ok := ingredientFallbacks[normalizeIngredientKey(name)]; ok {
		candidates = fb
	}

	var lastErr error
	for _, c := range candidates {
		if _, ok := lookupRecipe(b, c); !ok {
			continue
		}
		if _, err := craftChain(ctx, b, user, c, need, depth+1); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("tidak punya bahan untuk %s", name)
	}
	return lastErr
}

// lookupRecipe resolves a recipe network ID by item name.
func lookupRecipe(b *bot.Bot, name string) (uint32, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	id, ok := b.Recipes[name]
	if !ok {
		id, ok = b.Recipes["minecraft:"+name]
	}
	return id, ok
}

// normalizeIngredientKey lowercases and strips the minecraft: prefix so a tag
// like "minecraft:planks" maps to the ingredientFallbacks key "planks".
// Specific plank variants (warped_planks, crimson_planks, etc.) are collapsed
// to "planks" so the fallback list covers them.
func normalizeIngredientKey(name string) string {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "minecraft:"))
	if strings.HasSuffix(name, "_planks") {
		return "planks"
	}
	return name
}

// recipeNeedsCraftingBench determines whether a recipe truly requires a 3×3
// crafting table. Many 2×2 recipes (oak_planks, sticks, crafting_table) can be
// made in the player's personal 2×2 inventory grid even if the server tags them
// with Block="crafting_table". We use the recipe shape/dimensions as the
// ground truth.
//
// Note: dragonfly's handleAutoCraft requires craft.Block()=="crafting_table"
// for AutoCraftRecipe regardless of grid size, and a crafting_table tag covers
// BOTH the 3×3 table and the 2×2 inventory grid, so we never need to open a
// table window for AutoCraft — the recipe network ID carries that association.
func recipeNeedsCraftingBench(recipe bot.RecipeInfo) bool {
	if recipe.Block == "" {
		return false
	}

	// Non-crafting-table blocks (furnace, stonecutter, cartography_table,
	// blast_furnace, etc.) require their own special interface.
	if recipe.Block != "crafting_table" {
		return true
	}

	// Block == "crafting_table". If the recipe fits in a 2×2 grid it can be
	// auto-crafted against the inventory grid without opening a table.
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
				count = safecast.To[int32](parsed)
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
				count = safecast.To[int32](parsed)
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
			// Give the server time to process the drop transaction and spawn
			// the item BEFORE we rotate/move. Navigating immediately swings the
			// body yaw toward backPos within one tick; if the server applies
			// that yaw when spawning the drop, the item flies backward instead
			// of toward the player.
			time.Sleep(450 * time.Millisecond)

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
			// Release the forced upward look so the head returns to a neutral
			// gaze instead of staying stuck pointing up after the toss.
			b.ResetLook()
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

var itemAliases = map[string]string{
	"craftingtable":  "crafting_table",
	"craft_table":    "crafting_table",
	"workbench":      "crafting_table",
	"wood":           "oak_log",
	"kayu":           "oak_log",
	"log":            "oak_log",
	"logs":           "oak_log",
	"plank":          "oak_planks",
	"planks":         "oak_planks",
	"papan":          "oak_planks",
	"tanah":          "dirt",
	"batu":           "stone",
	"pasir":          "sand",
	"gandum":         "wheat",
	"wheat_crop":     "wheat",
	"wortel":         "carrot",
	"kentang":        "potato",
	"sapi":           "cow",
	"cow_animal":     "cow",
	"domba":          "sheep",
	"sheep_animal":   "sheep",
	"babi":           "pig",
	"pig_animal":     "pig",
	"ayam":           "chicken",
	"chicken_animal": "chicken",
	"serigala":       "wolf",
	"dog":            "wolf",
	"kucing":         "cat",
}

func normalizeItemName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.TrimPrefix(name, "minecraft:")
	if alias, ok := itemAliases[name]; ok {
		return alias
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
