package bot

import (
	"fmt"
	"strings"

	"bedrock-ai/internal/ai"

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
	dropItem.Count = uint16(count)

	remaining := foundItem.Count - uint16(count)
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
	} else {
		updated := foundItem
		updated.Count = remaining
		b.InventoryMap[targetSlot] = updated
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

	consumePlan, err := planIngredientConsumption(b.InventoryMap, recipe.Ingredients, count)
	if err != nil {
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
	b.Mu.Unlock()

	actions := make([]protocol.StackRequestAction, 0, 2+len(consumePlan))

	actions = append(actions, &protocol.AutoCraftRecipeStackRequestAction{
		RecipeNetworkID: recipeNetID,
		NumberOfCrafts:  byte(count),
		TimesCrafted:    byte(count),
		Ingredients:     recipe.Ingredients,
	})

	for _, c := range consumePlan {
		consume := &protocol.ConsumeStackRequestAction{}
		consume.Count = byte(c.count * count)
		consume.Source = protocol.StackRequestSlotInfo{
			Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
			Slot:           byte(c.slot),
			StackNetworkID: 0,
		}
		actions = append(actions, consume)
	}

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
	return b.Conn.WritePacket(pk)
}

type ingredientPick struct {
	slot  uint32
	count int
}

// planIngredientConsumption resolves each recipe ingredient to inventory slots
// containing matching items and computes per-slot consume counts. Returns an
// error if any ingredient cannot be satisfied for `times` repetitions.
func planIngredientConsumption(inv map[uint32]protocol.ItemStack, ingredients []protocol.ItemDescriptorCount, times int) ([]ingredientPick, error) {
	// Track per-slot remaining count as we consume so multiple ingredients
	// can share a slot without overcounting.
	remaining := make(map[uint32]int, len(inv))
	for slot, item := range inv {
		remaining[slot] = int(item.Count)
	}

	picks := make([]ingredientPick, 0, len(ingredients))
	for _, ing := range ingredients {
		need := int(ing.Count) * times
		if need <= 0 {
			continue
		}
		desc, ok := ing.Descriptor.(*protocol.DefaultItemDescriptor)
		if !ok || desc.NetworkID == 0 {
			// Skip non-default descriptors (item tags, MoLang) — those need a
			// tag→item lookup we don't have. Vanilla server still accepts the
			// chain without explicit Consume for these, and dragonfly ignores
			// Consume entirely.
			continue
		}
		networkID := int32(desc.NetworkID)
		for slot, item := range inv {
			if remaining[slot] <= 0 {
				continue
			}
			if item.NetworkID != networkID {
				continue
			}
			take := remaining[slot]
			if take > need {
				take = need
			}
			picks = append(picks, ingredientPick{slot: slot, count: take})
			remaining[slot] -= take
			need -= take
			if need <= 0 {
				break
			}
		}
		if need > 0 {
			return nil, fmt.Errorf("not enough ingredient (netID=%d) for %d crafts", desc.NetworkID, times)
		}
	}
	return picks, nil
}

// findFirstEmptyPlayerSlot returns the lowest player-inventory slot (0-35) that
// is empty in the bot's local inventory map.
func findFirstEmptyPlayerSlot(inv map[uint32]protocol.ItemStack) (uint32, bool) {
	for slot := uint32(0); slot < 36; slot++ {
		item, occupied := inv[slot]
		if !occupied || item.Count == 0 {
			return slot, true
		}
	}
	return 0, false
}

func (b *Bot) GetRecipes() map[string]uint32 {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	// Create a shallow copy to be thread-safe
	copyMap := make(map[string]uint32, len(b.Recipes))
	for k, v := range b.Recipes {
		copyMap[k] = v
	}
	return copyMap
}
