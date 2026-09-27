// Package bot provides the core Minecraft bot implementation.
package bot

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/safecast"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// dropTargetSlotLocked picks which inventory slot to drop from for a request.
// The held slot wins when it matches — dropping from a random matching stack
// (map iteration order) leaves the held item rendered in the hand after the
// drop, which viewers read as a ghost item — otherwise the lowest numbered
// matching slot, so repeated drops are deterministic. Callers hold b.Mu.
func (b *Bot) dropTargetSlotLocked(name string) (uint32, protocol.ItemStack, bool) {
	if held, ok := b.InventoryMap[b.HeldSlot]; ok && held.Count > 0 {
		if itemMatchesName(b.ItemNames[held.NetworkID], name) {
			return b.HeldSlot, held, true
		}
	}
	for slot := uint32(0); slot < 36; slot++ {
		item, ok := b.InventoryMap[slot]
		if !ok || item.Count <= 0 {
			continue
		}
		if itemMatchesName(b.ItemNames[item.NetworkID], name) {
			return slot, item, true
		}
	}
	return 0, protocol.ItemStack{}, false
}

func itemMatchesName(itemName, want string) bool {
	return strings.Contains(strings.ToLower(itemName), strings.ToLower(want))
}

func (b *Bot) DropItem(name string, count int) error {
	b.Mu.Lock()
	targetSlot, foundItem, found := b.dropTargetSlotLocked(name)
	if !found {
		b.Mu.Unlock()
		return fmt.Errorf("item %q not found in inventory", name)
	}

	item := protocol.ItemInstance{StackNetworkID: b.StackNetworkIDs[targetSlot], Stack: foundItem}
	b.Mu.Unlock()

	action, dropped, err := buildDropStackAction(targetSlot, item, count)
	if err != nil {
		return fmt.Errorf("drop %q from inventory slot %d: %w", name, targetSlot, err)
	}

	// Face-direction swing for viewers, then the authoritative drop request.
	if err := b.Conn.WritePacket(buildDropSwing(b.Conn.GameData().EntityRuntimeID)); err != nil {
		return fmt.Errorf("drop %d %q: send drop swing: %w", dropped, name, err)
	}

	// Use the same request/response machinery as crafting so the server spawns
	// the item along the player's look direction and returns an authoritative
	// inventory update. This keeps drop direction/strength consistent and
	// avoids optimistic local mutation.
	requestID, resultCh := b.beginStackRequest(0)
	if _, err := b.sendStackRequest(requestID, resultCh, []protocol.StackRequestAction{action}, name); err != nil {
		return fmt.Errorf("drop %d %q from inventory slot %d: %w", dropped, name, targetSlot, err)
	}

	// The authoritative ItemStackResponse updates InventoryMap/StackNetworkIDs
	// via applyItemStackResponse and drives syncHeldEquipmentIfUpdated, so the
	// held-item visual clears without any local guessing.
	return nil
}

func (b *Bot) InjectAIEvent(msg string) {
	b.Logger.Info("AI Event injected", "msg", msg)
	if b.AiClient == nil {
		return
	}

	// Query Nvidia Client asynchronously with the system message
	go func() {
		hp, hunger, botCoords := b.GetStatusDetails()
		heldItem := b.GetHeldItem()
		invSummary := b.GetInventorySummary()

		b.Mu.Lock()
		mainPlayer := b.AiCfg.MainPlayer
		botName := b.Name
		b.Mu.Unlock()

		if mainPlayer == "" {
			return
		}

		playerCoordsStr := ""
		if pCoords, ok := b.GetPlayerCoords(mainPlayer); ok {
			playerCoordsStr = fmt.Sprintf("X:%.0f Y:%.0f Z:%.0f", pCoords.X(), pCoords.Y(), pCoords.Z())
		}

		botStatusText := fmt.Sprintf("HP: %d/%d, Hunger: %d/%d", hp, MaxHealth, hunger, MaxHunger)
		systemPrompt := b.AiClient.BuildSystemPrompt(
			botName,
			botCoords+" ("+botStatusText+")",
			playerCoordsStr,
			heldItem,
			invSummary,
		)

		reply, err := b.AiClient.Ask(mainPlayer, systemPrompt, msg)
		if err != nil {
			b.Logger.Error("Failed to ask Nvidia LLM for injected event", "error", err.Error())
			return
		}

		parsed := ai.Parse(reply)
		if parsed.CleanReply != "" {
			b.SendSafeChat(parsed.CleanReply)
		}

		if ExecuteActionFunc != nil {
			for _, act := range parsed.Actions {
				ExecuteActionFunc(b, act.Label, act.Param, mainPlayer)
			}
		}
	}()
}

var errStackRequestRejected = errors.New("server rejected item stack request")

type craftingGridInput struct {
	slot           byte
	count          int
	stackNetworkID int32
}

type stagedCraftIngredient struct {
	sourceSlot    uint32
	gridSlot      byte
	count         int
	itemNetworkID int32
}

// CraftItem follows the manual-grid sequence emitted by the vanilla client:
// inventory -> cursor, cursor -> crafting input, then craft/consume/place.
func (b *Bot) CraftItem(recipeNetID uint32, count int) error {
	b.craftMu.Lock()
	defer b.craftMu.Unlock()

	if count <= 0 {
		count = 1
	}
	if count > MaxStackSize {
		return fmt.Errorf("cannot craft %d times in one request", count)
	}

	b.Mu.Lock()
	recipe, ok := b.RecipesByNetID[recipeNetID]
	if !ok {
		b.Mu.Unlock()
		return fmt.Errorf("recipe %d not in cache (waiting for CraftingData)", recipeNetID)
	}
	picks, err := planIngredientConsumption(b.InventoryMap, b.ItemNames, recipe.Ingredients, count)
	if err != nil {
		b.Mu.Unlock()
		return err
	}
	itemName := b.ItemNames[recipe.Output.NetworkID]
	ingredientSources := snapshotIngredientSources(b.InventoryMap, picks)
	itemNetworkIDs := make(map[uint32]int32, len(picks))
	for _, pick := range picks {
		itemNetworkIDs[pick.slot] = b.InventoryMap[pick.slot].NetworkID
	}
	b.Mu.Unlock()

	if err := validatePersonalCraftRecipe(recipe); err != nil {
		return err
	}
	outputCount := int(recipe.Output.Count) * count
	if outputCount <= 0 || outputCount > MaxStackSize {
		return fmt.Errorf("crafted output count %d is unsupported", outputCount)
	}

	b.Logger.Info("CraftItem manual grid",
		"recipeNetID", recipeNetID,
		"item", itemName,
		"count", count,
		"ingredientCount", len(picks),
	)

	if err := b.Conn.WritePacket(&packet.Interact{
		ActionType:            packet.InteractActionOpenInventory,
		TargetEntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
	}); err != nil {
		return fmt.Errorf("open personal inventory: %w", err)
	}
	time.Sleep(InventoryOpenDelay)

	staging := &craftStaging{
		gridInputs:         make([]craftingGridInput, 0, len(picks)),
		gridInputIndexes:   make(map[byte]int, len(picks)),
		predictedSourceIDs: make(map[uint32]int32, len(picks)),
		stagedIngredients:  make([]stagedCraftIngredient, 0, len(picks)),
		itemNetworkIDs:     itemNetworkIDs,
		itemName:           itemName,
	}
	for _, pick := range picks {
		if err := b.stageCraftIngredient(recipe, pick, staging); err != nil {
			return err
		}
	}

	b.Mu.Lock()
	outputSlot, hasOutputSlot := findFirstEmptyPlayerSlot(b.InventoryMap)
	b.Mu.Unlock()
	if !hasOutputSlot {
		if restoreErr := b.restoreCraftingGrid(staging.stagedIngredients, staging.gridInputs, staging.gridInputIndexes, staging.itemName); restoreErr != nil {
			return fmt.Errorf("inventory full; restore crafting grid: %v", restoreErr)
		}
		return fmt.Errorf("inventory full, cannot place crafted output")
	}
	return b.finalizeCraft(recipe, recipeNetID, count, outputSlot, ingredientSources, staging)
}

// craftStaging accumulates per-ingredient grid state while CraftItem fills the
// crafting input. Extracting the staging loop body into stageCraftIngredient
// keeps CraftItem a thin orchestrator and lets a later rejection roll the
// partially-filled grid back via restoreCraftingGrid.
type craftStaging struct {
	gridInputs         []craftingGridInput
	gridInputIndexes   map[byte]int
	predictedSourceIDs map[uint32]int32
	stagedIngredients  []stagedCraftIngredient
	itemNetworkIDs     map[uint32]int32
	itemName           string
}

// stageCraftIngredient stages one ingredient (inventory -> cursor -> crafting
// input). It delegates the take and place phases to dedicated methods so each
// stays under the maintainability complexity gate; on rejection either phase
// rolls the partially-filled grid back via restoreCraftingGrid.
func (b *Bot) stageCraftIngredient(recipe RecipeInfo, pick ingredientPick, s *craftStaging) error {
	gridSlot, err := craftingGridSlot(recipe, pick.ingredientIndex)
	if err != nil {
		return err
	}
	cursorStackID, err := b.takeIngredientToCursor(pick, s)
	if err != nil {
		return err
	}
	return b.placeIngredientOnGrid(pick, s, gridSlot, cursorStackID)
}

// takeIngredientToCursor moves pick.count items from the ingredient's inventory
// slot onto the cursor and records the authoritative stack ID for later place
// requests. Returns the cursor stack ID, falling back to the take request ID
// when the server does not assign one.
func (b *Bot) takeIngredientToCursor(pick ingredientPick, s *craftStaging) (int32, error) {
	stackNetworkID := s.predictedSourceIDs[pick.slot]
	if stackNetworkID == 0 {
		b.Mu.Lock()
		stackNetworkID = b.StackNetworkIDs[pick.slot]
		b.Mu.Unlock()
	}
	if stackNetworkID == 0 {
		return 0, fmt.Errorf("cannot craft: slot %d has invalid StackNetworkID (0)", pick.slot)
	}

	source := playerStackRequestSlot(pick.slot, stackNetworkID)
	take := buildTakeToCursorAction(source, pick.count)
	takeID, takeCh := b.beginStackRequest(0)
	takeResult, err := b.sendStackRequest(takeID, takeCh, []protocol.StackRequestAction{take}, s.itemName)
	if err != nil {
		if errors.Is(err, errStackRequestRejected) && len(s.stagedIngredients) > 0 {
			if restoreErr := b.restoreCraftingGrid(s.stagedIngredients, s.gridInputs, s.gridInputIndexes, s.itemName); restoreErr != nil {
				return 0, fmt.Errorf("take ingredient from slot %d: %w; restore crafting grid: %v", pick.slot, err, restoreErr)
			}
		}
		return 0, fmt.Errorf("take ingredient from slot %d: %w", pick.slot, err)
	}
	s.predictedSourceIDs[pick.slot] = takeResult.stackNetworkID(source.Container.ContainerID, source.Slot)
	if s.predictedSourceIDs[pick.slot] == 0 {
		s.predictedSourceIDs[pick.slot] = takeID
	}

	cursorStackID := takeResult.stackNetworkID(protocol.ContainerCursor, 0)
	if cursorStackID == 0 {
		cursorStackID = takeID
	}
	return cursorStackID, nil
}

// placeIngredientOnGrid moves the cursor stack into the crafting input slot,
// merging with an existing grid stack when present, and records the staged
// ingredient so a later rejection can roll it back.
func (b *Bot) placeIngredientOnGrid(pick ingredientPick, s *craftStaging, gridSlot byte, cursorStackID int32) error {
	destinationStackID := int32(0)
	if index, exists := s.gridInputIndexes[gridSlot]; exists {
		destinationStackID = s.gridInputs[index].stackNetworkID
	}
	place := buildPlaceCursorToCraftingAction(cursorStackID, gridSlot, destinationStackID, pick.count)
	placeID, placeCh := b.beginStackRequest(0)
	placeResult, err := b.sendStackRequest(placeID, placeCh, []protocol.StackRequestAction{place}, s.itemName)
	if err != nil {
		if errors.Is(err, errStackRequestRejected) {
			if restoreErr := b.returnCursorToInventory(cursorStackID, pick.slot, pick.count, s.itemNetworkIDs[pick.slot], s.itemName); restoreErr != nil {
				return fmt.Errorf("place ingredient in crafting slot %d: %w; restore cursor: %v", gridSlot, err, restoreErr)
			}
			if restoreErr := b.restoreCraftingGrid(s.stagedIngredients, s.gridInputs, s.gridInputIndexes, s.itemName); restoreErr != nil {
				return fmt.Errorf("place ingredient in crafting slot %d: %w; restore crafting grid: %v", gridSlot, err, restoreErr)
			}
		}
		return fmt.Errorf("place ingredient in crafting slot %d: %w", gridSlot, err)
	}

	gridStackID := placeResult.stackNetworkID(protocol.ContainerCraftingInput, gridSlot)
	if gridStackID == 0 {
		gridStackID = placeID
	}
	if index, exists := s.gridInputIndexes[gridSlot]; exists {
		s.gridInputs[index].count += pick.count
		s.gridInputs[index].stackNetworkID = gridStackID
	} else {
		s.gridInputIndexes[gridSlot] = len(s.gridInputs)
		s.gridInputs = append(s.gridInputs, craftingGridInput{slot: gridSlot, count: pick.count, stackNetworkID: gridStackID})
	}
	s.stagedIngredients = append(s.stagedIngredients, stagedCraftIngredient{
		sourceSlot:    pick.slot,
		gridSlot:      gridSlot,
		count:         pick.count,
		itemNetworkID: s.itemNetworkIDs[pick.slot],
	})
	return nil
}

// finalizeCraft submits the craft request for the staged grid, reconciles the
// inventory snapshot on success, and restores the partially-filled grid on
// rejection. Extracted from CraftItem so the main function stays a thin
// validate → stage → finalize orchestrator.
func (b *Bot) finalizeCraft(recipe RecipeInfo, recipeNetID uint32, count int, outputSlot uint32, ingredientSources map[uint32]ingredientSourceSnapshot, s *craftStaging) error {
	requestID, resultCh := b.beginStackRequest(recipe.Output.NetworkID)
	actions, err := buildCraftActions(requestID, recipeNetID, recipe, count, s.gridInputs, outputSlot, b.ItemNames[recipe.Output.NetworkID])
	if err != nil {
		// The grid is already staged, so release it before reporting the failure
		// rather than leaving items stranded in the crafting input.
		if restoreErr := b.restoreCraftingGrid(s.stagedIngredients, s.gridInputs, s.gridInputIndexes, s.itemName); restoreErr != nil {
			return fmt.Errorf("craft %s: %w; restore crafting grid: %v", s.itemName, err, restoreErr)
		}
		return fmt.Errorf("craft %s: %w", s.itemName, err)
	}
	if _, err := b.sendStackRequest(requestID, resultCh, actions, s.itemName); err != nil {
		if errors.Is(err, errStackRequestRejected) {
			if restoreErr := b.restoreCraftingGrid(s.stagedIngredients, s.gridInputs, s.gridInputIndexes, s.itemName); restoreErr != nil {
				return fmt.Errorf("craft %s: %w; restore crafting grid: %v", s.itemName, err, restoreErr)
			}
		}
		return fmt.Errorf("craft %s: %w", s.itemName, err)
	}

	b.Mu.Lock()
	reconcileCraftIngredientCounts(b.InventoryMap, b.StackNetworkIDs, ingredientSources)
	b.Mu.Unlock()

	b.Logger.Info("CraftItem accepted",
		"recipeNetID", recipeNetID,
		"item", s.itemName,
		"count", count,
		"inventory", b.GetInventorySummary(),
	)
	return nil
}

// CraftItemOnTable crafts a 3×3 (or any crafting_table-requiring) recipe using
// the AutoCraft protocol action. The crafting_table window must already be open
// (via crafting.Manager.OpenCraftingTable). Unlike CraftItem, this does NOT
// manually fill the grid — the server pulls ingredients from inventory and
// crafting input automatically.
func (b *Bot) CraftItemOnTable(recipeNetID uint32, count int) error {
	b.craftMu.Lock()
	defer b.craftMu.Unlock()

	if count <= 0 {
		count = 1
	}
	if count > MaxStackSize {
		return fmt.Errorf("cannot craft %d times in one request", count)
	}

	b.Mu.Lock()
	recipe, ok := b.RecipesByNetID[recipeNetID]
	if !ok {
		b.Mu.Unlock()
		return fmt.Errorf("recipe %d not in cache (waiting for CraftingData)", recipeNetID)
	}
	itemName := b.ItemNames[recipe.Output.NetworkID]
	ingredientSources := snapshotIngredientSourcesFromRecipe(b.InventoryMap, b.ItemNames, recipe.Ingredients, count)
	b.Mu.Unlock()

	outputCount := int(recipe.Output.Count) * count
	if outputCount <= 0 || outputCount > MaxStackSize {
		return fmt.Errorf("crafted output count %d is unsupported", outputCount)
	}

	b.Logger.Info("CraftItemOnTable auto-craft",
		"recipeNetID", recipeNetID,
		"item", itemName,
		"count", count,
		"ingredientCount", len(recipe.Ingredients),
	)

	b.Mu.Lock()
	outputSlot, hasOutputSlot := findFirstEmptyPlayerSlot(b.InventoryMap)
	b.Mu.Unlock()
	if !hasOutputSlot {
		return fmt.Errorf("inventory full, cannot place crafted output")
	}

	resultItem := recipe.Output
	resultItem.Count = safecast.To[uint16](outputCount)
	resultStackItem, ok := stackRequestItemFromStack(resultItem, itemName)
	if !ok {
		return fmt.Errorf("cannot describe crafted output %q to the server", itemName)
	}

	actions := []protocol.StackRequestAction{
		&protocol.AutoCraftRecipeStackRequestAction{
			RecipeNetworkID: recipeNetID,
			NumberOfCrafts:  byte(count),
			Ingredients:     recipe.Ingredients,
		},
		&protocol.CraftResultsDeprecatedStackRequestAction{
			ResultItems:  []protocol.StackRequestItem{resultStackItem},
			TimesCrafted: byte(count),
		},
	}

	requestID, resultCh := b.beginStackRequest(recipe.Output.NetworkID)
	placeAction := &protocol.PlaceStackRequestAction{}
	placeAction.Count = byte(outputCount)
	placeAction.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCreatedOutput},
		Slot:           CreatedOutputSlot,
		StackNetworkID: requestID,
	}
	placeAction.Destination = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:      byte(outputSlot),
	}
	actions = append(actions, placeAction)

	if _, err := b.sendStackRequest(requestID, resultCh, actions, itemName); err != nil {
		return fmt.Errorf("auto-craft %s: %w", itemName, err)
	}

	b.Mu.Lock()
	reconcileCraftIngredientCounts(b.InventoryMap, b.StackNetworkIDs, ingredientSources)
	b.Mu.Unlock()

	b.Logger.Info("CraftItemOnTable accepted",
		"recipeNetID", recipeNetID,
		"item", itemName,
		"count", count,
		"inventory", b.GetInventorySummary(),
	)
	return nil
}

func validatePersonalCraftRecipe(recipe RecipeInfo) error {
	if recipe.Block != "" && recipe.Block != "crafting_table" {
		return fmt.Errorf("recipe requires unsupported crafting block %s", recipe.Block)
	}
	// ponytail: personal 2x2 grid only; open a crafting-table UI before supporting 3x3 recipes.
	if (!recipe.Shapeless && (recipe.Width > 2 || recipe.Height > 2)) || (recipe.Shapeless && len(recipe.Ingredients) > 4) {
		return fmt.Errorf("recipe requires an open crafting table")
	}
	return nil
}

func craftingGridSlot(recipe RecipeInfo, ingredientIndex int) (byte, error) {
	if recipe.Shapeless {
		if ingredientIndex < 0 || ingredientIndex >= 4 {
			return 0, fmt.Errorf("ingredient %d does not fit personal crafting grid", ingredientIndex)
		}
		return byte(CraftingGridBaseSlot + ingredientIndex), nil
	}
	if recipe.Width <= 0 || recipe.Height <= 0 || recipe.Width > 2 || recipe.Height > 2 {
		return 0, fmt.Errorf("invalid personal crafting shape %dx%d", recipe.Width, recipe.Height)
	}
	if ingredientIndex < 0 || ingredientIndex >= int(recipe.Width*recipe.Height) {
		return 0, fmt.Errorf("ingredient %d is outside recipe shape", ingredientIndex)
	}
	if recipe.Width == 1 && recipe.Height == 1 {
		return CraftingGrid1x1Slot, nil
	}
	row := ingredientIndex / int(recipe.Width)
	column := ingredientIndex % int(recipe.Width)
	if recipe.Width == 1 {
		column = 1
	}
	return byte(CraftingGridBaseSlot + row*2 + column), nil
}

func playerStackRequestSlot(slot uint32, stackNetworkID int32) protocol.StackRequestSlotInfo {
	containerID := byte(protocol.ContainerInventory)
	if slot < 9 {
		containerID = protocol.ContainerHotBar
	}
	return protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: containerID},
		Slot:           byte(slot),
		StackNetworkID: stackNetworkID,
	}
}

func buildTakeToCursorAction(source protocol.StackRequestSlotInfo, count int) *protocol.TakeStackRequestAction {
	action := &protocol.TakeStackRequestAction{}
	action.Count = byte(count)
	action.Source = source
	action.Destination = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCursor},
		Slot:      0,
	}
	return action
}

func buildPlaceCursorToCraftingAction(cursorStackID int32, gridSlot byte, destinationStackID int32, count int) *protocol.PlaceStackRequestAction {
	return buildPlaceFromCursorAction(cursorStackID, protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCraftingInput},
		Slot:           gridSlot,
		StackNetworkID: destinationStackID,
	}, count)
}

func buildPlaceFromCursorAction(cursorStackID int32, destination protocol.StackRequestSlotInfo, count int) *protocol.PlaceStackRequestAction {
	action := &protocol.PlaceStackRequestAction{}
	action.Count = byte(count)
	action.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCursor},
		Slot:           0,
		StackNetworkID: cursorStackID,
	}
	action.Destination = destination
	return action
}

func (b *Bot) returnCursorToInventory(cursorStackID int32, destinationSlot uint32, count int, itemNetworkID int32, itemName string) error {
	b.Mu.Lock()
	destinationStackID := b.StackNetworkIDs[destinationSlot]
	b.Mu.Unlock()
	action := buildPlaceFromCursorAction(cursorStackID, playerStackRequestSlot(destinationSlot, destinationStackID), count)
	requestID, resultCh := b.beginStackRequest(itemNetworkID)
	_, err := b.sendStackRequest(requestID, resultCh, []protocol.StackRequestAction{action}, itemName)
	return err
}

func (b *Bot) restoreCraftingGrid(staged []stagedCraftIngredient, gridInputs []craftingGridInput, indexes map[byte]int, itemName string) error {
	for i := len(staged) - 1; i >= 0; i-- {
		entry := staged[i]
		gridInput := &gridInputs[indexes[entry.gridSlot]]
		source := protocol.StackRequestSlotInfo{
			Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCraftingInput},
			Slot:           entry.gridSlot,
			StackNetworkID: gridInput.stackNetworkID,
		}
		takeID, takeCh := b.beginStackRequest(0)
		takeResult, err := b.sendStackRequest(takeID, takeCh, []protocol.StackRequestAction{buildTakeToCursorAction(source, entry.count)}, itemName)
		if err != nil {
			return err
		}
		gridInput.count -= entry.count
		gridInput.stackNetworkID = takeResult.stackNetworkID(protocol.ContainerCraftingInput, entry.gridSlot)
		cursorStackID := takeResult.stackNetworkID(protocol.ContainerCursor, 0)
		if cursorStackID == 0 {
			cursorStackID = takeID
		}
		if err := b.returnCursorToInventory(cursorStackID, entry.sourceSlot, entry.count, entry.itemNetworkID, itemName); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bot) beginStackRequest(outputNetID int32) (int32, chan craftResult) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.StackRequestID >= 0 {
		b.StackRequestID = -1
	}
	b.StackRequestID -= 2
	requestID := b.StackRequestID
	resultCh := make(chan craftResult, 1)
	b.pendingCrafts[requestID] = pendingCraft{ch: resultCh, outputNetID: outputNetID}
	return requestID, resultCh
}

func (b *Bot) sendStackRequest(requestID int32, resultCh chan craftResult, actions []protocol.StackRequestAction, itemName string) (craftResult, error) {
	request := protocol.ItemStackRequest{RequestID: requestID, Actions: actions, FilterCause: -1}
	if err := b.Conn.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{request}}); err != nil {
		b.Mu.Lock()
		delete(b.pendingCrafts, requestID)
		b.Mu.Unlock()
		return craftResult{}, fmt.Errorf("send item stack request: %w", err)
	}
	b.Logger.Info("CraftItem awaiting response", "item", itemName, "requestID", requestID, "actions", len(actions))

	select {
	case result := <-resultCh:
		if !result.accepted {
			return result, fmt.Errorf("%w (item: %s)", errStackRequestRejected, itemName)
		}
		return result, nil
	case <-b.Conn.Context().Done():
		b.Mu.Lock()
		delete(b.pendingCrafts, requestID)
		b.Mu.Unlock()
		return craftResult{}, fmt.Errorf("connection closed while waiting for item stack response (item: %s)", itemName)
	case <-time.After(5 * time.Second):
		return craftResult{}, fmt.Errorf("server did not respond to item stack request within 5s (item: %s)", itemName)
	}
}

// buildCraftActions assembles the stack request actions for one craft. Craft
// results are addressed by namespaced item identifier rather than network ID, so
// the output item must be resolvable to a name; otherwise the server would
// receive an empty identifier and reject the request without a clear reason.
func buildCraftActions(requestID int32, recipeNetID uint32, recipe RecipeInfo, count int, gridInputs []craftingGridInput, outputSlot uint32, outputName string) ([]protocol.StackRequestAction, error) {
	outputCount := int(recipe.Output.Count) * count
	resultItem := recipe.Output
	resultItem.Count = safecast.To[uint16](outputCount)
	resultStackItem, ok := stackRequestItemFromStack(resultItem, outputName)
	if !ok {
		return nil, fmt.Errorf("cannot determine the item name of recipe output %+v", recipe.Output)
	}

	actions := make([]protocol.StackRequestAction, 0, len(gridInputs)+3)
	actions = append(actions,
		&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: recipeNetID, NumberOfCrafts: byte(count)},
		&protocol.CraftResultsDeprecatedStackRequestAction{ResultItems: []protocol.StackRequestItem{resultStackItem}, TimesCrafted: byte(count)},
	)
	for _, input := range gridInputs {
		actions = append(actions, &protocol.ConsumeStackRequestAction{
			DestroyStackRequestAction: protocol.DestroyStackRequestAction{
				Count: byte(input.count),
				Source: protocol.StackRequestSlotInfo{
					Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCraftingInput},
					Slot:           input.slot,
					StackNetworkID: input.stackNetworkID,
				},
			},
		})
	}
	place := &protocol.PlaceStackRequestAction{}
	place.Count = byte(outputCount)
	place.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCreatedOutput},
		Slot:           CreatedOutputSlot,
		StackNetworkID: requestID,
	}
	place.Destination = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:      byte(outputSlot),
	}
	return append(actions, place), nil
}
