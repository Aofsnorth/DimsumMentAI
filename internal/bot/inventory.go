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

	tx := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				WindowID:      0,
				InventorySlot: targetSlot,
				OldItem:       protocol.ItemInstance{Stack: foundItem},
				NewItem: protocol.ItemInstance{
					Stack: protocol.ItemStack{
						ItemType:       foundItem.ItemType,
						BlockRuntimeID: foundItem.BlockRuntimeID,
						Count:          foundItem.Count - uint16(count),
						NBTData:        foundItem.NBTData,
						CanBePlacedOn:  foundItem.CanBePlacedOn,
						CanBreak:       foundItem.CanBreak,
						HasNetworkID:   foundItem.HasNetworkID,
					},
				},
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

	return b.Conn.WritePacket(tx)
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

func (b *Bot) CraftItem(recipeNetID uint32, count int) error {
	b.Mu.Lock()
	b.StackRequestID++
	requestID := b.StackRequestID
	b.Mu.Unlock()

	req := protocol.ItemStackRequest{
		RequestID: requestID,
		Actions: []protocol.StackRequestAction{
			&protocol.AutoCraftRecipeStackRequestAction{
				RecipeNetworkID: recipeNetID,
				TimesCrafted:    byte(count),
				NumberOfCrafts:  byte(count),
			},
		},
	}
	pk := &packet.ItemStackRequest{
		Requests: []protocol.ItemStackRequest{req},
	}
	return b.Conn.WritePacket(pk)
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
