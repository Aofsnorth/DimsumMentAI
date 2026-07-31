package bot

import (
	"errors"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// buildDropStackAction builds the vanilla server-authoritative drop action.
//
// On inventory-authoritative Bedrock servers (the same servers that require
// ItemStackRequest for crafting), a legacy world-drop InventoryTransaction is
// handled inconsistently — the server may spawn the item with a default or
// arbitrary velocity instead of along the player's look direction. The vanilla
// client instead issues a DropStackRequestAction inside an ItemStackRequest, so
// the server spawns the drop from the player's current facing. Matching that
// makes the drop direction and throw strength consistent.
//
// count is clamped to the stack size; a non-positive count drops the whole
// stack. The returned dropped count reflects what was actually requested.
func buildDropStackAction(slot uint32, item protocol.ItemInstance, requestedCount int) (*protocol.DropStackRequestAction, uint16, error) {
	if item.Stack.Count == 0 {
		return nil, 0, errors.New("item stack is empty")
	}
	if item.StackNetworkID == 0 {
		return nil, 0, errors.New("authoritative stack network ID is missing")
	}

	dropped := item.Stack.Count
	if requestedCount > 0 && requestedCount < int(item.Stack.Count) {
		dropped = uint16(requestedCount)
	}

	action := &protocol.DropStackRequestAction{
		Count:    byte(dropped),
		Source:   playerStackRequestSlot(slot, item.StackNetworkID),
		Randomly: false,
	}
	return action, dropped, nil
}

// buildDropSwing builds the arm animation shown when tossing an item.
func buildDropSwing(entityRuntimeID uint64) *packet.Animate {
	return &packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: entityRuntimeID,
		SwingSource:     packet.AnimateSwingSourceDropItem,
	}
}
