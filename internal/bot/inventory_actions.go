// Package bot provides the core Minecraft bot implementation.
package bot

import (
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

// CraftItem sends the vanilla recipe-book auto-craft action sequence and waits
// for the authoritative ItemStackResponse before returning.
func (b *Bot) CraftItem(recipeNetID uint32, count int) error {
	b.craftMu.Lock()
	defer b.craftMu.Unlock()

	if count <= 0 {
		count = 1
	}

	b.Mu.Lock()
	recipe, ok := b.RecipesByNetID[recipeNetID]
	if !ok {
		b.Mu.Unlock()
		return fmt.Errorf("recipe %d not in cache (waiting for CraftingData)", recipeNetID)
	}

	// Resolve which inventory slots will be consumed so we can emit the
	// matching Consume actions below. Also serves as an early validation.
	picks, err := planIngredientConsumption(b.InventoryMap, b.ItemNames, recipe.Ingredients, count)
	if err != nil {
		b.Mu.Unlock()
		return err
	}

	outputSlot, ok := findFirstEmptyPlayerSlot(b.InventoryMap)
	if !ok {
		b.Mu.Unlock()
		return fmt.Errorf("inventory full, cannot place crafted output")
	}

	if b.StackRequestID >= 0 {
		b.StackRequestID = -1
	}
	b.StackRequestID -= 2
	requestID := b.StackRequestID

	// Register a pending craft channel so applyItemStackResponse can notify
	// us when the server accepts or rejects this request. Also pass the
	// recipe output's NetworkID so the response handler can tag newly-created
	// inventory slots with the correct item type.
	resultCh := make(chan craftResult, 1)
	b.pendingCrafts[requestID] = pendingCraft{
		ch:          resultCh,
		outputNetID: recipe.Output.NetworkID,
	}
	outputNetID := recipe.Output.NetworkID
	itemName := b.ItemNames[outputNetID]
	stackNetworkIDs := make(map[uint32]int32, len(picks))
	for _, pick := range picks {
		stackNetID := b.StackNetworkIDs[pick.slot]
		if stackNetID == 0 {
			b.Mu.Unlock()
			return fmt.Errorf("cannot craft: slot %d has invalid StackNetworkID (0) - inventory not synced", pick.slot)
		}
		stackNetworkIDs[pick.slot] = stackNetID
		b.Logger.Debug("CraftItem ingredient pick",
			"slot", pick.slot,
			"count", pick.count,
			"stackNetworkID", stackNetID,
		)
	}

	// Snapshot itemNames before releasing b.Mu so we can log outside the
	// lock without racing the PacketLoop's inventory updates.
	itemNames := make(map[int32]string, len(b.ItemNames))
	for k, v := range b.ItemNames {
		itemNames[k] = v
	}
	b.Mu.Unlock()

	// Log outside b.Mu lock to avoid blocking PacketLoop from processing
	// the ItemStackResponse. The response handler (applyItemStackResponse)
	// needs b.Mu - if we hold it during verbose logging, the server's
	// response arrives but can't be applied, causing a 5s timeout.
	b.Logger.Info("CraftItem request",
		"recipeNetID", recipeNetID,
		"item", itemName,
		"count", count,
		"recipeBlock", recipe.Block,
		"recipeShapeless", recipe.Shapeless,
		"recipeWidth", recipe.Width,
		"recipeHeight", recipe.Height,
		"recipeOutputNetID", recipe.Output.NetworkID,
		"recipeOutputCount", recipe.Output.Count,
		"outputSlot", outputSlot,
		"ingredientCount", len(recipe.Ingredients),
	)
	for i, ing := range recipe.Ingredients {
		var ingNetID int32
		var ingName string
		if dd, ok := ing.Descriptor.(*protocol.DefaultItemDescriptor); ok {
			ingNetID = int32(dd.NetworkID)
			ingName = itemNames[ingNetID]
		}
		b.Logger.Info("CraftItem ingredient",
			"index", i,
			"netID", ingNetID,
			"name", ingName,
			"count", ing.Count,
		)
	}

	actions := buildCraftActions(recipeNetID, recipe, count, picks, stackNetworkIDs, outputSlot)

	request := protocol.ItemStackRequest{
		RequestID:   requestID,
		Actions:     actions,
		FilterCause: -1,
	}

	// Debug log the full request details
	b.Logger.Debug("CraftItem sending request",
		"requestID", requestID,
		"recipeNetID", recipeNetID,
		"item", itemName,
		"count", count,
		"actionsCount", len(actions),
		"picks", len(picks),
	)

	if err := b.Conn.WritePacket(&packet.ItemStackRequest{
		Requests: []protocol.ItemStackRequest{request},
	}); err != nil {
		b.Mu.Lock()
		delete(b.pendingCrafts, requestID)
		b.Mu.Unlock()
		return fmt.Errorf("send craft request: %w", err)
	}

	// Log awaiting state so the user sees the bot is alive during the
	// response wait (typically 100-500ms, can spike to several seconds on
	// busy servers). Without this the bot looks frozen between the
	// request and either the accepted/timeout log.
	b.Logger.Info("CraftItem awaiting response",
		"recipeNetID", recipeNetID,
		"item", itemName,
		"requestID", requestID,
	)

	// Wait for the server's ItemStackResponse. The response handler
	// (applyItemStackResponse) will send a craftResult to resultCh. If the
	// server doesn't respond within 5 seconds, treat it as a failure.
	select {
	case result := <-resultCh:
		if !result.accepted {
			b.Logger.Warn("CraftItem rejected",
				"recipeNetID", recipeNetID,
				"item", itemName,
				"requestID", requestID,
			)
			return fmt.Errorf("server rejected craft request (item: %s)", itemName)
		}
		// Craft accepted. The response handler already updated InventoryMap
		// with the correct item type (using outputNetID for new slots).
		// Logged at INFO so the chain-craft caller can see the recipe's
		// specific variant was accepted (e.g. "oak_planks accepted" vs
		// the silent bare request log above).
		b.Logger.Info("CraftItem accepted",
			"recipeNetID", recipeNetID,
			"item", itemName,
			"count", count,
		)
		return nil
	case <-b.Conn.Context().Done():
		b.Mu.Lock()
		delete(b.pendingCrafts, requestID)
		b.Mu.Unlock()
		return fmt.Errorf("connection closed while waiting for craft response (item: %s)", itemName)
	case <-time.After(5 * time.Second):
		b.Mu.Lock()
		delete(b.pendingCrafts, requestID)
		b.Mu.Unlock()
		b.Logger.Warn("CraftItem timeout",
			"recipeNetID", recipeNetID,
			"item", itemName,
			"requestID", requestID,
			"timeout", "5s",
		)
		return fmt.Errorf("server did not respond to craft request within 5s (item: %s)", itemName)
	}
}

func buildCraftActions(recipeNetID uint32, recipe RecipeInfo, count int, picks []ingredientPick, stackNetworkIDs map[uint32]int32, outputSlot uint32) []protocol.StackRequestAction {
	if count <= 0 {
		count = 1
	}
	outputCount := int(recipe.Output.Count) * count
	if outputCount <= 0 {
		outputCount = count
	}
	if outputCount > 64 {
		outputCount = 64
	}

	actions := make([]protocol.StackRequestAction, 0, len(picks)+3)
	actions = append(actions, &protocol.CraftRecipeStackRequestAction{
		RecipeNetworkID: recipeNetID,
		NumberOfCrafts:  byte(count),
	})
	actions = append(actions, &protocol.CraftResultsDeprecatedStackRequestAction{
		ResultItems:  []protocol.ItemStack{recipe.Output},
		TimesCrafted: byte(count),
	})
	for _, pick := range picks {
		actions = append(actions, &protocol.ConsumeStackRequestAction{
			DestroyStackRequestAction: protocol.DestroyStackRequestAction{
				Count: byte(pick.count),
				Source: protocol.StackRequestSlotInfo{
					Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
					Slot:           byte(pick.slot),
					StackNetworkID: stackNetworkIDs[pick.slot],
				},
			},
		})
	}
	place := &protocol.PlaceStackRequestAction{}
	place.Count = byte(outputCount)
	place.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCreatedOutput},
		Slot:           50,
		StackNetworkID: -1,
	}
	place.Destination = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:           byte(outputSlot),
		StackNetworkID: -1,
	}
	actions = append(actions, place)
	return actions
}
