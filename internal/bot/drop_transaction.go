package bot

import (
	"errors"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// ClampDropCount is the shared drop-count rule: a non-positive request drops
// the whole stack, a request larger than the stack drops everything there is.
func ClampDropCount(stackCount uint16, requestedCount int) uint16 {
	if requestedCount > 0 && requestedCount < int(stackCount) {
		return uint16(requestedCount)
	}
	return stackCount
}

// BuildDropStackAction builds the vanilla server-authoritative drop action.
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
func BuildDropStackAction(slot uint32, item protocol.ItemInstance, requestedCount int) (*protocol.DropStackRequestAction, uint16, error) {
	if item.Stack.Count == 0 {
		return nil, 0, errors.New("item stack is empty")
	}
	if item.StackNetworkID == 0 {
		return nil, 0, errors.New("authoritative stack network ID is missing")
	}

	dropped := ClampDropCount(item.Stack.Count, requestedCount)

	action := &protocol.DropStackRequestAction{
		Count:    byte(dropped),
		Source:   PlayerStackRequestSlot(slot, item.StackNetworkID),
		Randomly: false,
	}
	return action, dropped, nil
}

// BuildDropCursorAction builds the drop of count items held on the cursor.
//
// Geyser answers the cursor form reliably (it maps to a click outside the
// window) while a drop straight from a hotbar/inventory slot can go unanswered,
// so Geyser hosts are driven take-to-cursor then drop-from-cursor, the same two
// steps the vanilla inventory screen performs.
func BuildDropCursorAction(cursorStackID int32, count int) (*protocol.DropStackRequestAction, error) {
	if count <= 0 || count > 255 {
		return nil, errors.New("drop count out of range")
	}
	if cursorStackID == 0 {
		return nil, errors.New("cursor stack network ID is missing")
	}
	return &protocol.DropStackRequestAction{
		Count: byte(count),
		Source: protocol.StackRequestSlotInfo{
			Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCursor},
			Slot:           0,
			StackNetworkID: cursorStackID,
		},
	}, nil
}

// BuildDropSwing builds the arm animation shown when tossing an item.
func BuildDropSwing(entityRuntimeID uint64) *packet.Animate {
	return &packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: entityRuntimeID,
		SwingSource:     packet.AnimateSwingSourceDropItem,
	}
}

// BuildDropTransaction builds one Q-press: a NORMAL InventoryTransaction whose
// two actions hand `drop` items out of the held slot into the world.
//
// This is the only drop shape a Geyser front-end implements. Its
// PlayerAction translator handles just RESPAWN, STOP_SLEEP and
// DIMENSION_CHANGE_SUCCESS, so a PlayerAction DropItem is read and discarded,
// and a DropStackRequestAction is the inventory-screen drop a host never
// processes while no inventory is open. Geyser's InventoryTransaction
// translator reads this shape directly: the container action's slot is matched
// against the Java held slot, and the world action's item count decides
// between drop-one (1) and drop-the-stack (>1).
//
// stack is the server's own view of the slot (identity and count); `drop` is
// how many of them leave. The old/new pair keeps the transaction balanced for
// hosts that validate the actions instead of just executing them.
func BuildDropTransaction(heldSlot uint32, stackNetworkID int32, stack protocol.ItemStack, drop uint16) *packet.InventoryTransaction {
	oldCount := stack.Count
	after := int32(oldCount) - int32(drop)
	if after < 0 {
		after = 0
	}

	worldStack := stack
	worldStack.Count = drop
	var newItem protocol.ItemInstance
	if after > 0 {
		remaining := stack
		remaining.Count = uint16(after)
		newItem = protocol.ItemInstance{StackNetworkID: stackNetworkID, Stack: remaining}
	}

	return &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				WindowID:      protocol.Option(int8(protocol.WindowIDInventory)),
				InventorySlot: heldSlot,
				OldItem:       protocol.ItemInstance{StackNetworkID: stackNetworkID, Stack: stack},
				NewItem:       newItem,
			},
			{
				SourceType:  protocol.InventoryActionSourceWorld,
				SourceFlags: protocol.Option(uint32(1)), // InventorySource.Flag.DROP_ITEM
				OldItem:     protocol.ItemInstance{},
				NewItem:     protocol.ItemInstance{StackNetworkID: stackNetworkID, Stack: worldStack},
			},
		},
		TransactionData: &protocol.NormalTransactionData{},
	}
}
