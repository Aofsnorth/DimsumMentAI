package bot

import (
	"errors"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func buildDropPackets(entityRuntimeID uint64, slot uint32, item protocol.ItemInstance, requestedCount int) (*packet.InventoryTransaction, *packet.Animate, uint16, error) {
	if item.Stack.Count == 0 {
		return nil, nil, 0, errors.New("item stack is empty")
	}
	if item.StackNetworkID == 0 {
		return nil, nil, 0, errors.New("authoritative stack network ID is missing")
	}

	dropped := item.Stack.Count
	if requestedCount > 0 && requestedCount < int(item.Stack.Count) {
		dropped = uint16(requestedCount)
	}

	dropItem := item
	dropItem.Stack.Count = dropped
	remainingItem := item
	remainingItem.Stack.Count -= dropped
	if remainingItem.Stack.Count == 0 {
		remainingItem = protocol.ItemInstance{}
	}

	transaction := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				WindowID:      protocol.WindowIDInventory,
				InventorySlot: slot,
				OldItem:       item,
				NewItem:       remainingItem,
			},
			{
				SourceType:    protocol.InventoryActionSourceWorld,
				SourceFlags:   1,
				InventorySlot: 0,
				OldItem:       protocol.ItemInstance{},
				NewItem:       dropItem,
			},
		},
		TransactionData: &protocol.NormalTransactionData{},
	}
	swing := &packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: entityRuntimeID,
		SwingSource:     packet.AnimateSwingSourceDropItem,
	}
	return transaction, swing, dropped, nil
}
