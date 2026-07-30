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

func (b *Bot) DropItem(name string, count int) error {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	var targetSlot uint32
	var foundItem protocol.ItemStack
	found := false

	// Find the item by name
	for slot, item := range b.InventoryMap {
		if item.Count <= 0 {
			continue
		}
		itemName := b.ItemNames[item.NetworkID]
		if strings.Contains(strings.ToLower(itemName), strings.ToLower(name)) {
			targetSlot = slot
			foundItem = item
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("item %s not found in inventory", name)
	}

	if count <= 0 || count > int(foundItem.Count) {
		count = int(foundItem.Count)
	}

	// Create dropped item transaction
	dropItem := foundItem
	dropItem.Count = safecast.To[uint16](count)

	remaining := foundItem.Count - safecast.To[uint16](count)
	var newSlotItem protocol.ItemInstance
	if remaining > 0 {
		newSlotItem = protocol.ItemInstance{
			Stack: protocol.ItemStack{
				ItemType:       foundItem.ItemType,
				BlockRuntimeID: foundItem.BlockRuntimeID,
				Count:          remaining,
				NBTData:        foundItem.NBTData,
				CanBePlacedOn:  foundItem.CanBePlacedOn,
				CanBreak:       foundItem.CanBreak,
				HasNetworkID:   foundItem.HasNetworkID,
			},
		}
	}

	tx := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				WindowID:      protocol.WindowIDInventory,
				InventorySlot: targetSlot,
				OldItem:       protocol.ItemInstance{Stack: foundItem},
				NewItem:       newSlotItem,
			},
			{
				SourceType:    protocol.InventoryActionSourceWorld,
				SourceFlags:   1, // Drop item flag
				InventorySlot: 0,
				OldItem:       protocol.ItemInstance{},
				NewItem:       protocol.ItemInstance{Stack: dropItem},
			},
		},
		TransactionData: &protocol.NormalTransactionData{},
	}

	if err := b.Conn.WritePacket(tx); err != nil {
		return err
	}

	if remaining == 0 {
		delete(b.InventoryMap, targetSlot)
		// If we just emptied the slot the bot is holding, broadcast a
		// MobEquipment update so the hand visual clears immediately for other
		// clients (Bedrock won't echo this automatically when the drop is
		// initiated by the bot itself).
		if targetSlot == b.HeldSlot {
			_ = b.Conn.WritePacket(&packet.MobEquipment{
				EntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
				NewItem:         protocol.ItemInstance{},
				InventorySlot:   byte(targetSlot),
				HotBarSlot:      byte(targetSlot),
				WindowID:        byte(protocol.WindowIDInventory),
			})
		}
	} else {
		updated := foundItem
		updated.Count = remaining
		b.InventoryMap[targetSlot] = updated
		// Slot still has items but count changed — refresh visual.
		if targetSlot == b.HeldSlot {
			_ = b.Conn.WritePacket(&packet.MobEquipment{
				EntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
				NewItem:         protocol.ItemInstance{Stack: updated},
				InventorySlot:   byte(targetSlot),
				HotBarSlot:      byte(targetSlot),
				WindowID:        byte(protocol.WindowIDInventory),
			})
		}
	}
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

		botStatusText := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)
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
	if count > 64 {
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
	itemNetworkIDs := make(map[uint32]int32, len(picks))
	for _, pick := range picks {
		itemNetworkIDs[pick.slot] = b.InventoryMap[pick.slot].NetworkID
	}
	b.Mu.Unlock()

	if err := validatePersonalCraftRecipe(recipe); err != nil {
		return err
	}
	outputCount := int(recipe.Output.Count) * count
	if outputCount <= 0 || outputCount > 64 {
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
	time.Sleep(150 * time.Millisecond)

	gridInputs := make([]craftingGridInput, 0, len(picks))
	gridInputIndexes := make(map[byte]int, len(picks))
	predictedSourceIDs := make(map[uint32]int32, len(picks))
	stagedIngredients := make([]stagedCraftIngredient, 0, len(picks))
	for _, pick := range picks {
		gridSlot, err := craftingGridSlot(recipe, pick.ingredientIndex)
		if err != nil {
			return err
		}

		stackNetworkID := predictedSourceIDs[pick.slot]
		if stackNetworkID == 0 {
			b.Mu.Lock()
			stackNetworkID = b.StackNetworkIDs[pick.slot]
			b.Mu.Unlock()
		}
		if stackNetworkID == 0 {
			return fmt.Errorf("cannot craft: slot %d has invalid StackNetworkID (0)", pick.slot)
		}

		source := playerStackRequestSlot(pick.slot, stackNetworkID)
		take := buildTakeToCursorAction(source, pick.count)
		takeID, takeCh := b.beginStackRequest(0)
		takeResult, err := b.sendStackRequest(takeID, takeCh, []protocol.StackRequestAction{take}, itemName)
		if err != nil {
			if errors.Is(err, errStackRequestRejected) && len(stagedIngredients) > 0 {
				if restoreErr := b.restoreCraftingGrid(stagedIngredients, gridInputs, gridInputIndexes, itemName); restoreErr != nil {
					return fmt.Errorf("take ingredient from slot %d: %w; restore crafting grid: %v", pick.slot, err, restoreErr)
				}
			}
			return fmt.Errorf("take ingredient from slot %d: %w", pick.slot, err)
		}
		predictedSourceIDs[pick.slot] = takeResult.stackNetworkID(source.Container.ContainerID, source.Slot)
		if predictedSourceIDs[pick.slot] == 0 {
			predictedSourceIDs[pick.slot] = takeID
		}

		cursorStackID := takeResult.stackNetworkID(protocol.ContainerCursor, 0)
		if cursorStackID == 0 {
			cursorStackID = takeID
		}
		destinationStackID := int32(0)
		if index, exists := gridInputIndexes[gridSlot]; exists {
			destinationStackID = gridInputs[index].stackNetworkID
		}
		place := buildPlaceCursorToCraftingAction(cursorStackID, gridSlot, destinationStackID, pick.count)
		placeID, placeCh := b.beginStackRequest(0)
		placeResult, err := b.sendStackRequest(placeID, placeCh, []protocol.StackRequestAction{place}, itemName)
		if err != nil {
			if errors.Is(err, errStackRequestRejected) {
				if restoreErr := b.returnCursorToInventory(cursorStackID, pick.slot, pick.count, itemNetworkIDs[pick.slot], itemName); restoreErr != nil {
					return fmt.Errorf("place ingredient in crafting slot %d: %w; restore cursor: %v", gridSlot, err, restoreErr)
				}
				if restoreErr := b.restoreCraftingGrid(stagedIngredients, gridInputs, gridInputIndexes, itemName); restoreErr != nil {
					return fmt.Errorf("place ingredient in crafting slot %d: %w; restore crafting grid: %v", gridSlot, err, restoreErr)
				}
			}
			return fmt.Errorf("place ingredient in crafting slot %d: %w", gridSlot, err)
		}

		gridStackID := placeResult.stackNetworkID(protocol.ContainerCraftingInput, gridSlot)
		if gridStackID == 0 {
			gridStackID = placeID
		}
		if index, exists := gridInputIndexes[gridSlot]; exists {
			gridInputs[index].count += pick.count
			gridInputs[index].stackNetworkID = gridStackID
		} else {
			gridInputIndexes[gridSlot] = len(gridInputs)
			gridInputs = append(gridInputs, craftingGridInput{slot: gridSlot, count: pick.count, stackNetworkID: gridStackID})
		}
		stagedIngredients = append(stagedIngredients, stagedCraftIngredient{
			sourceSlot:    pick.slot,
			gridSlot:      gridSlot,
			count:         pick.count,
			itemNetworkID: itemNetworkIDs[pick.slot],
		})
	}

	b.Mu.Lock()
	outputSlot, hasOutputSlot := findFirstEmptyPlayerSlot(b.InventoryMap)
	b.Mu.Unlock()
	if !hasOutputSlot {
		if restoreErr := b.restoreCraftingGrid(stagedIngredients, gridInputs, gridInputIndexes, itemName); restoreErr != nil {
			return fmt.Errorf("inventory full; restore crafting grid: %v", restoreErr)
		}
		return fmt.Errorf("inventory full, cannot place crafted output")
	}
	requestID, resultCh := b.beginStackRequest(recipe.Output.NetworkID)
	actions := buildCraftActions(requestID, recipeNetID, recipe, count, gridInputs, outputSlot)
	if _, err := b.sendStackRequest(requestID, resultCh, actions, itemName); err != nil {
		if errors.Is(err, errStackRequestRejected) {
			if restoreErr := b.restoreCraftingGrid(stagedIngredients, gridInputs, gridInputIndexes, itemName); restoreErr != nil {
				return fmt.Errorf("craft %s: %w; restore crafting grid: %v", itemName, err, restoreErr)
			}
		}
		return fmt.Errorf("craft %s: %w", itemName, err)
	}

	b.Logger.Info("CraftItem accepted", "recipeNetID", recipeNetID, "item", itemName, "count", count)
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
		return byte(28 + ingredientIndex), nil
	}
	if recipe.Width <= 0 || recipe.Height <= 0 || recipe.Width > 2 || recipe.Height > 2 {
		return 0, fmt.Errorf("invalid personal crafting shape %dx%d", recipe.Width, recipe.Height)
	}
	if ingredientIndex < 0 || ingredientIndex >= int(recipe.Width*recipe.Height) {
		return 0, fmt.Errorf("ingredient %d is outside recipe shape", ingredientIndex)
	}
	if recipe.Width == 1 && recipe.Height == 1 {
		return 29, nil
	}
	row := ingredientIndex / int(recipe.Width)
	column := ingredientIndex % int(recipe.Width)
	if recipe.Width == 1 {
		column = 1
	}
	return byte(28 + row*2 + column), nil
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

func buildCraftActions(requestID int32, recipeNetID uint32, recipe RecipeInfo, count int, gridInputs []craftingGridInput, outputSlot uint32) []protocol.StackRequestAction {
	outputCount := int(recipe.Output.Count) * count
	resultItem := recipe.Output
	resultItem.Count = safecast.To[uint16](outputCount)

	actions := make([]protocol.StackRequestAction, 0, len(gridInputs)+3)
	actions = append(actions,
		&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: recipeNetID, NumberOfCrafts: byte(count)},
		&protocol.CraftResultsDeprecatedStackRequestAction{ResultItems: []protocol.ItemStack{resultItem}, TimesCrafted: byte(count)},
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
		Slot:           50,
		StackNetworkID: requestID,
	}
	place.Destination = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:      byte(outputSlot),
	}
	return append(actions, place)
}
