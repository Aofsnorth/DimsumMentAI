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
				WindowID:      int32(protocol.WindowIDInventory),
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

// CraftItem sends an ItemStackRequest with the full vanilla-style action chain
// (AutoCraftRecipe + Consume per ingredient + Place output → empty slot).
// Sending only AutoCraftRecipe without the surrounding chain causes strict
// servers (vanilla/NetherGames) to disconnect the client.
//
// RequestID follows vanilla convention: negative, decrement by 2.
func (b *Bot) CraftItem(recipeNetID uint32, count int) error {
	if count <= 0 {
		count = 1
	}

	b.Mu.Lock()
	recipe, ok := b.RecipesByNetID[recipeNetID]
	if !ok {
		b.Mu.Unlock()
		return fmt.Errorf("recipe %d not in cache (waiting for CraftingData)", recipeNetID)
	}

	// Validate that we have enough ingredients for the requested crafts. We
	// don't send Consume actions to the server (auto-craft handles consumption
	// server-side), but we still check locally so we can fail early with a
	// clear error instead of a server rejection.
	if _, err := planIngredientConsumption(b.InventoryMap, b.ItemNames, recipe.Ingredients, count); err != nil {
		b.Mu.Unlock()
		return err
	}

	outputSlot, ok := findFirstEmptyPlayerSlot(b.InventoryMap)
	if !ok {
		b.Mu.Unlock()
		return fmt.Errorf("inventory full, cannot place crafted output")
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
			ingName = b.ItemNames[ingNetID]
		}
		b.Logger.Info("CraftItem ingredient",
			"index", i,
			"netID", ingNetID,
			"name", ingName,
			"count", ing.Count,
		)
	}
	b.Mu.Unlock()

	// For AutoCraftRecipe (shift-click recipe book style), the server handles
	// ingredient consumption internally. The client only sends:
	//   1. AutoCraftRecipe action (recipe ID, times crafted, ingredients)
	//   2. Place action (move output from CreatedOutput to inventory slot)
	//
	// Sending Consume actions alongside AutoCraftRecipe causes the vanilla BDS
	// server to reject with status=7 (InvalidCraftRequest) because it doesn't
	// expect explicit consumption for auto-craft.
	actions := make([]protocol.StackRequestAction, 0, 2)

	actions = append(actions, &protocol.AutoCraftRecipeStackRequestAction{
		RecipeNetworkID: recipeNetID,
		NumberOfCrafts:  byte(count),
		TimesCrafted:    byte(count),
		Ingredients:     recipe.Ingredients,
	})

	outputCount := int(recipe.Output.Count) * count
	if outputCount <= 0 {
		outputCount = count
	}
	if outputCount > 64 {
		outputCount = 64
	}
	place := &protocol.PlaceStackRequestAction{}
	place.Count = byte(outputCount)
	place.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCreatedOutput},
		Slot:           50,
		StackNetworkID: requestID,
	}
	place.Destination = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:           byte(outputSlot),
		StackNetworkID: 0,
	}
	actions = append(actions, place)

	pk := &packet.ItemStackRequest{
		Requests: []protocol.ItemStackRequest{{
			RequestID: requestID,
			Actions:   actions,
		}},
	}
	if err := b.Conn.WritePacket(pk); err != nil {
		b.Mu.Lock()
		delete(b.pendingCrafts, requestID)
		b.Mu.Unlock()
		return fmt.Errorf("write craft request: %w", err)
	}

	// Wait for the server's ItemStackResponse. The response handler
	// (applyItemStackResponse) will send a craftResult to resultCh. If the
	// server doesn't respond within 5 seconds, treat it as a failure.
	select {
	case result := <-resultCh:
		if !result.accepted {
			return fmt.Errorf("server rejected craft request (item: %s)", itemName)
		}
		// Craft accepted. The response handler already updated InventoryMap
		// with the correct item type (using outputNetID for new slots).
		return nil
	case <-time.After(5 * time.Second):
		b.Mu.Lock()
		delete(b.pendingCrafts, requestID)
		b.Mu.Unlock()
		return fmt.Errorf("server did not respond to craft request within 5s (item: %s)", itemName)
	}
}
