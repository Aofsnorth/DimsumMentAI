// Package action provides helper handlers and normalisation utilities for
// the action dispatch in execute.go.
package action

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
)

const visibleTargetDistance = 32

func init() {
	actionHandlers["pvp"] = handlePVP
}

func handleAttack(b *bot.Bot, param, user string) {
	target, ok := selectAttackTarget(b, param, user)
	if !ok {
		b.Logger.Warn("ExecuteAction: no visible non-item mob found to attack", "param", param)
		return
	}
	b.CombatMgr.EngageTarget(target.ID)
}

func selectAttackTarget(b *bot.Bot, param, user string) (*entity.Info, bool) {
	b.Mu.Lock()
	origin := b.Pos
	actors := copyActorsLocked(b.Actors)
	excluded := excludedPlayerIDsLocked(b, user)
	b.Mu.Unlock()

	return entity.NearestVisibleMob(b.WorldModel, b, origin, actors, visibleTargetDistance, param, excluded)
}

func handlePVP(b *bot.Bot, param, user string) {
	target := param
	if target == "" {
		target = user
	}
	if target == "" || strings.EqualFold(target, b.Name) {
		b.Logger.Warn("ExecuteAction: no PVP target found", "param", param)
		return
	}
	if !b.CombatMgr.EngagePlayer(target) {
		b.Logger.Warn("ExecuteAction: PVP player target not found", "target", target)
	}
}

func copyActorsLocked(actors map[uint64]*entity.Info) map[uint64]*entity.Info {
	snapshot := make(map[uint64]*entity.Info, len(actors))
	for id, info := range actors {
		if info == nil {
			continue
		}
		copied := *info
		snapshot[id] = &copied
	}
	return snapshot
}

func excludedPlayerIDsLocked(b *bot.Bot, user string) map[uint64]struct{} {
	excluded := make(map[uint64]struct{}, len(b.PlayerEntityIDs)+1)
	for _, id := range b.PlayerEntityIDs {
		excluded[id] = struct{}{}
	}
	if user != "" {
		if id, _, ok := b.FindPlayer(user); ok {
			excluded[id] = struct{}{}
		}
	}
	return excluded
}

func handleCraft(b *bot.Bot, param, user string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.Logger.Error("craft action panicked", "panic", r)
			}
		}()
		executeCraftAction(b, param, user)
	}()
}

// executeCraftAction runs a direct chat craft asynchronously from the caller's
// perspective and reports the final outcome once.
func executeCraftAction(b *bot.Bot, param, user string) event.ActionStatus {
	return runCraftAction(b, param, user, true)
}

// executeCraftActionSilent runs a planner-owned craft synchronously without a
// per-step chat report. The planner emits one aggregate report after all steps.
func executeCraftActionSilent(b *bot.Bot, param, user string) event.ActionStatus {
	return runCraftAction(b, param, user, false)
}

func runCraftAction(b *bot.Bot, param, user string, report bool) event.ActionStatus {
	status := event.ActionStatus{Action: "craft"}
	if strings.TrimSpace(param) == "" {
		status.Error = "item craft kosong"
		return status
	}
	parts := strings.Split(param, ",")
	itemName := normalizeItemName(parts[0])
	count := 1
	if len(parts) >= 2 {
		_, _ = fmt.Sscanf(parts[1], "%d", &count)
	}
	status.Item = itemName
	status.Count = count

	b.Logger.Debug("Executing craft action", "item", itemName, "desired_count", count)
	actual, err := craftChain(context.Background(), b, user, itemName, count, 0)
	if err != nil {
		b.Logger.Warn("CraftItem failed", "err", err, "item", itemName)
		status.Error = err.Error()
		if report {
			b.ReportActionStatus(user, status)
		}
		return status
	}
	status.Count = actual
	status.Success = true
	if report {
		b.ReportActionStatus(user, status)
	}
	return status
}

// maxCraftDepth bounds chain-crafting recursion (e.g. oak_log -> oak_planks ->
// stick is depth 2) so a malformed recipe graph can never loop forever.
const maxCraftDepth = 4

type materialGatherFunc func(context.Context, *bot.Bot, string, int) error
type craftItemFunc func(*bot.Bot, uint32, int) error

type craftChainState struct {
	activeItems    map[string]struct{}
	gatherAttempts map[string]struct{}
	gather         materialGatherFunc
	craft          craftItemFunc
}

func newCraftChainState() *craftChainState {
	return &craftChainState{
		activeItems:    make(map[string]struct{}),
		gatherAttempts: make(map[string]struct{}),
		gather:         gatherMaterialSynchronously,
		craft: func(b *bot.Bot, recipeID uint32, crafts int) error {
			return b.CraftItem(recipeID, crafts)
		},
	}
}

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
	return craftChainWithState(ctx, b, user, itemName, count, depth, newCraftChainState(), true)
}

func craftChainWithState(ctx context.Context, b *bot.Bot, user, itemName string, count, depth int, state *craftChainState, allowGather bool) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("craft %s dibatalkan: %w", itemName, err)
	}
	if depth > maxCraftDepth {
		return 0, fmt.Errorf("rantai craft terlalu dalam untuk %s", itemName)
	}

	itemKey := canonicalMaterialName(itemName)
	if _, active := state.activeItems[itemKey]; active {
		return 0, fmt.Errorf("siklus resep terdeteksi saat craft %s", itemName)
	}
	state.activeItems[itemKey] = struct{}{}
	defer delete(state.activeItems, itemKey)

	recipeID, recipe, ok := pickBestRecipe(b, itemName)
	if !ok {
		return 0, fmt.Errorf("resep tidak diketahui: %s", itemName)
	}

	// 2. Convert desired output into craft operations, then satisfy ingredients
	// for exactly that many repetitions. Rejection falls back only for recipes
	// that truly exceed the 2×2 personal grid; planks and sticks never use a
	// crafting-table window.
	outputPerCraft := int(recipe.Output.Count)
	crafts := computeCrafts(count, outputPerCraft)
	if err := ensureCraftIngredients(ctx, b, user, recipe, crafts, depth, state, allowGather); err != nil {
		return 0, err
	}
	b.Logger.Info("chain craft step",
		"item", itemName,
		"recipeID", recipeID,
		"crafts", crafts,
		"depth", depth,
	)
	if err := state.craft(b, recipeID, crafts); err != nil {
		return 0, err
	}
	actual := outputPerCraft * crafts
	if actual > bot.MaxStackSize {
		actual = bot.MaxStackSize
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
//
//	2 = every ingredient is already in inventory
//	1 = every missing ingredient is a plank variant the bot can chain-craft
//	0 = otherwise (still craftable in principle, lowest preference)
func recipeSatisfactionScore(b *bot.Bot, recipe bot.RecipeInfo) int {
	allHave := true
	allHaveOrPlank := true
	for _, ing := range recipe.Ingredients {
		name := b.IngredientName(ing)
		if name == "" {
			continue
		}
		need := int(ing.Count)
		if b.CountItemLike(name) >= need {
			continue
		}
		allHave = false
		if normalizeIngredientKey(name) == "planks" && (b.CountItemLike("planks") > 0 || b.CountItemLike("log") > 0) {
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

type ingredientRequirement struct {
	name  string
	count int
}

// ensureCraftIngredients tries every chain-craft alternative before gathering.
// Only leaf ingredients with no known recipe are gathered, and inventory is
// re-counted after each synchronous operation.
func ensureCraftIngredients(ctx context.Context, b *bot.Bot, user string, recipe bot.RecipeInfo, crafts, depth int, state *craftChainState, allowGather bool) error {
	for _, requirement := range recipeIngredientRequirements(b, recipe, crafts) {
		available := b.CountItemLike(requirement.name)
		if available >= requirement.count {
			continue
		}

		missing := requirement.count - available
		b.Logger.Debug("chain craft: ingredient missing, trying craft alternatives", "ingredient", requirement.name, "missing", missing)
		craftErr := craftIngredient(ctx, b, user, requirement.name, missing, depth, state, allowGather)

		available = b.CountItemLike(requirement.name)
		if available >= requirement.count {
			continue
		}
		if hasCraftAlternative(b, requirement.name) {
			if craftErr == nil {
				craftErr = fmt.Errorf("inventory tidak bertambah")
			}
			return fmt.Errorf(
				"bahan %s masih kurang setelah alternatif craft dicoba (punya %d, perlu %d): %w",
				requirement.name,
				available,
				requirement.count,
				craftErr,
			)
		}
		if !allowGather {
			return fmt.Errorf("bahan mentah %s kurang: punya %d, perlu %d", requirement.name, available, requirement.count)
		}
		if err := autoGatherRawMaterial(ctx, b, state, requirement.name, requirement.count); err != nil {
			return err
		}
	}
	return nil
}

func recipeIngredientRequirements(b *bot.Bot, recipe bot.RecipeInfo, crafts int) []ingredientRequirement {
	requirements := make([]ingredientRequirement, 0, len(recipe.Ingredients))
	indexes := make(map[string]int, len(recipe.Ingredients))
	for _, ingredient := range recipe.Ingredients {
		name := b.IngredientName(ingredient)
		count := int(ingredient.Count) * crafts
		if name == "" || count <= 0 {
			continue
		}
		key := canonicalMaterialName(name)
		if index, exists := indexes[key]; exists {
			requirements[index].count += count
			continue
		}
		indexes[key] = len(requirements)
		requirements = append(requirements, ingredientRequirement{name: name, count: count})
	}
	return requirements
}

// craftIngredient crafts `need` of an ingredient, resolving generic/tag
// ingredients (e.g. "planks") to a concrete craftable variant the bot can
// actually make.
func craftIngredient(ctx context.Context, b *bot.Bot, user, name string, need, depth int, state *craftChainState, allowGather bool) error {
	candidates := append([]string(nil), resolveIngredientCandidates(name)...)
	if len(candidates) > 1 {
		sortCandidatesByAvailability(b, candidates)
	}

	lastErr := tryCraftIngredientCandidates(ctx, b, user, need, depth, state, candidates, false)
	if lastErr == nil || !allowGather {
		return lastErr
	}
	return tryCraftIngredientCandidates(ctx, b, user, need, depth, state, candidates, true)
}

func tryCraftIngredientCandidates(ctx context.Context, b *bot.Bot, user string, need, depth int, state *craftChainState, candidates []string, allowGather bool) error {
	var lastErr error
	for _, candidate := range candidates {
		if _, ok := lookupRecipe(b, candidate); !ok {
			continue
		}
		if _, err := craftChainWithState(ctx, b, user, candidate, need, depth+1, state, allowGather); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("tidak ada alternatif craft")
	}
	return lastErr
}

func hasCraftAlternative(b *bot.Bot, name string) bool {
	for _, candidate := range resolveIngredientCandidates(name) {
		if _, ok := lookupRecipe(b, candidate); ok {
			return true
		}
	}
	return false
}

func autoGatherRawMaterial(ctx context.Context, b *bot.Bot, state *craftChainState, name string, required int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("auto-gather %s dibatalkan: %w", name, err)
	}

	available := b.CountItemLike(name)
	missing := required - available
	if missing <= 0 {
		return nil
	}

	key := canonicalMaterialName(name)
	if _, attempted := state.gatherAttempts[key]; attempted {
		return fmt.Errorf("auto-gather %s tidak diulang; percobaan sebelumnya belum memenuhi kebutuhan %d", name, required)
	}
	state.gatherAttempts[key] = struct{}{}

	b.Logger.Info("chain craft: gathering raw ingredient", "ingredient", name, "missing", missing)
	if err := state.gather(ctx, b, name, missing); err != nil {
		return fmt.Errorf("auto-gather %s gagal: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("auto-gather %s dibatalkan: %w", name, err)
	}

	available = b.CountItemLike(name)
	if available < required {
		return fmt.Errorf("auto-gather %s belum cukup: punya %d, perlu %d", name, available, required)
	}
	return nil
}

func gatherMaterialSynchronously(ctx context.Context, b *bot.Bot, name string, missing int) error {
	if b.Gatherer == nil {
		return fmt.Errorf("pengumpul resource belum siap")
	}
	if b.Gatherer.IsGathering() {
		return fmt.Errorf("pengumpulan resource lain sedang berjalan")
	}
	if isWoodLike(name) {
		b.Gatherer.GatherWoodType(ctx, name, missing)
	} else {
		b.Gatherer.GatherBlock(ctx, name, missing)
	}
	return nil
}

func canonicalMaterialName(name string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "minecraft:"))
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

// resolveIngredientCandidates returns the list of item names to try when
// satisfying an ingredient. For generic Bedrock ingredient names
// (e.g. "planks", "minecraft:planks") it returns the wood-variant fallback
// list so the crafter can satisfy "any planks" using whatever wood the bot
// actually has. For specific variant names (e.g. "oak_planks",
// "minecraft:oak_planks") it returns just that name — a named variant must
// not silently fall through to a different wood type on failure, because
// that would blame the wrong wood in the final error.
//
// Note: prior to this helper the comparison was
// `strings.EqualFold(name, "planks")`, which always failed for the
// `minecraft:planks` form that Bedrock actually sends in CraftingData — so
// chain-crafting sticks failed with "tidak punya bahan untuk minecraft:planks"
// even when the bot had oak_log.
func resolveIngredientCandidates(name string) []string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimPrefix(n, "minecraft:")
	if n == "planks" {
		return ingredientFallbacks["planks"]
	}
	return []string{name}
}

func candidateMaterialAvailable(b *bot.Bot, candidate string) bool {
	candidate = strings.TrimPrefix(strings.ToLower(candidate), "minecraft:")
	if !strings.HasSuffix(candidate, "_planks") {
		return true
	}
	logName := strings.TrimSuffix(candidate, "_planks") + "_log"
	return b.CountItemLike(logName) > 0
}

// sortCandidatesByAvailability reorders candidates to prioritize those whose
// base materials (e.g. logs for planks) are actually in the bot's inventory.
// This prevents the bot from trying to craft cherry_planks when it only has
// oak_log.
func sortCandidatesByAvailability(b *bot.Bot, candidates []string) {
	type candidate struct {
		name  string
		score int
	}
	scored := make([]candidate, 0, len(candidates))

	for _, c := range candidates {
		score := 0
		// For planks, check if we have the corresponding log
		if strings.HasSuffix(c, "_planks") {
			logType := strings.TrimSuffix(c, "_planks") + "_log"
			if b.CountItemLike(logType) > 0 {
				score = 2 // Has exact log type
			} else if b.CountItemLike("log") > 0 {
				score = 1 // Has some log, might work
			}
		}
		scored = append(scored, candidate{name: c, score: score})
	}

	// Sort by score descending (higher score = more available)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Copy back to original slice
	for i, sc := range scored {
		candidates[i] = sc.name
	}
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
		parts := strings.Split(strings.TrimSpace(param), ",")
		itemName := normalizeItemName(parts[0])
		count := 0
		if len(parts) >= 2 {
			_, _ = fmt.Sscanf(parts[1], "%d", &count)
		}

		// If param empty OR contains "hand"/"held"/"tangan"/"dipegang", drop held item
		if itemName == "" ||
			strings.Contains(strings.ToLower(itemName), "hand") ||
			strings.Contains(strings.ToLower(itemName), "held") ||
			strings.Contains(strings.ToLower(itemName), "tangan") ||
			strings.Contains(strings.ToLower(itemName), "dipegang") {
			b.Mu.Lock()
			heldSlot := b.HeldSlot
			heldItem := b.InventoryMap[heldSlot]
			heldItemName := b.ItemNames[heldItem.NetworkID]
			b.Mu.Unlock()

			if heldItem.Count == 0 || heldItemName == "" {
				b.Logger.Warn("handleDrop: no item in hand")
				return
			}
			itemName = heldItemName
			b.Logger.Info("handleDrop: dropping held item", "item", heldItemName, "slot", heldSlot)
		} else if itemName == "" {
			// Neither specific item nor held item — nothing to drop
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
			targetHead := playerPos.Add(mgl32.Vec3{0, bot.PlayerEyeHeight, 0})

			// Pin the look target at the player's head for 3s so the movement
			// loop continuously interpolates toward this point.
			dx := targetHead.X() - botPos.X()
			dz := targetHead.Z() - botPos.Z()
			yaw := float32(math.Atan2(float64(dz), float64(dx))*(180.0/math.Pi)) - bot.YawOffsetDegrees
			for yaw < 0 {
				yaw += bot.FullCircleDegrees
			}
			b.Logger.Debug("handleDrop: computed target yaw", "yaw", yaw, "dx", dx, "dz", dz)

			// Force-set both body yaw AND head yaw to the exact target FIRST, plus
			// a slight upward pitch so the item arcs forward into the player's
			// pickup radius. SetLookAngles pins the body Yaw (which Bedrock
			// uses for drop direction) instead of leaving it lagging behind
			// HeadYaw through eased interpolation.
			b.SetLookAngles(yaw, bot.DefaultDropPitch)
			b.WaitForYawSync(yaw, bot.YawSyncTimeout)
			b.Logger.Debug("handleDrop: angles set and synced, waiting for stabilization")
			time.Sleep(bot.AngleStabilizationDelay)
		} else {
			b.Mu.Lock()
			currentYaw := b.Yaw
			b.Mu.Unlock()
			b.SetLookAngles(currentYaw, bot.DefaultDropPitch)
			b.WaitForYawSync(currentYaw, bot.YawSyncTimeout)
			time.Sleep(bot.AngleStabilizationDelay)
		}

		// --- Drop the item --------------------------------------------------
		b.Logger.Debug("handleDrop: executing drop", "item", itemName, "count", count)
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
					botPos.X() - (dx/hLen)*bot.BackStepDistance,
					botPos.Y(),
					botPos.Z() - (dz/hLen)*bot.BackStepDistance,
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
