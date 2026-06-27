package player

import (
	"fmt"
	"log/slog"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	// playerInvSlotCount is the number of slots in the combined hotbar + main
	// inventory window (WindowIDInventory). A full sync always has this many
	// entries.
	playerInvSlotCount = 36
)

// isPlayerInventoryContainer reports whether a ContainerID refers to a slot in
// the player's own inventory family. The server may push updates via any of
// these IDs depending on what triggered the change (pickup, /give, drop into
// open inventory UI, etc.).
func isPlayerInventoryContainer(id byte) bool {
	switch id {
	case protocol.ContainerInventory,
		protocol.ContainerHotBar,
		protocol.ContainerCombinedHotBarAndInventory,
		protocol.ContainerOffhand,
		protocol.ContainerArmor:
		return true
	}
	return false
}

func isPlayerInventoryContent(p *packet.InventoryContent) bool {
	if p.WindowID == protocol.WindowIDInventory {
		return true
	}
	return isPlayerInventoryContainer(p.Container.ContainerID)
}

func isPlayerInventorySlot(p *packet.InventorySlot) bool {
	if p.WindowID == protocol.WindowIDInventory {
		return true
	}
	if c, ok := p.Container.Value(); ok {
		return isPlayerInventoryContainer(c.ContainerID)
	}
	return false
}

// isPlayerInventoryTransaction reports whether an InventoryTransaction action
// updates the bot's own inventory (hotbar + main inventory, armor, or offhand).
func isPlayerInventoryTransaction(action protocol.InventoryAction) bool {
	if action.SourceType != protocol.InventoryActionSourceContainer {
		return false
	}
	switch action.WindowID {
	case protocol.WindowIDInventory, protocol.WindowIDArmour, protocol.WindowIDOffHand:
		return true
	}
	return false
}

// containerSlotOffset returns the global inventory slot offset for a given
// container ID. Bedrock sends inventory updates per-container, where each
// container's slot array starts at 0 — but the bot's InventoryMap uses global
// slot indices matching the full 41-slot player inventory:
//
//	0-8   HotBar
//	9-35  Main Inventory
//	36-39 Armor
//	40    Offhand
//
// Without this offset, a ContainerInventory update (27 main-inventory slots
// indexed 0-26) would overwrite HotBar slots 0-8 instead of mapping to global
// slots 9-35.
func containerSlotOffset(containerID byte) uint32 {
	switch containerID {
	case protocol.ContainerHotBar:
		return 0 // slots 0-8
	case protocol.ContainerInventory:
		return 9 // slots 9-35
	case protocol.ContainerArmor:
		return 36 // slots 36-39
	case protocol.ContainerOffhand:
		return 40 // slot 40
	case protocol.ContainerCombinedHotBarAndInventory:
		return 0 // slots 0-35 (already global)
	default:
		return 0
	}
}

// applyInventoryContent merges an InventoryContent update into the bot's
// InventoryMap WITHOUT wiping slots that belong to other containers. A full
// sync (WindowIDInventory + exactly 36 items) refreshes slots 0-35. Partial
// syncs use the container ID to determine which slot range to touch.
//
// Some servers send a partial update with WindowIDInventory and fewer than 36
// items. Previously this was treated as a full sync and wiped the main
// inventory; now we fall back to the container ID so only the relevant slots
// are cleared.
func applyInventoryContent(b *bot.Bot, p *packet.InventoryContent) {
	containerID := p.Container.ContainerID

	b.Mu.Lock()
	defer b.Mu.Unlock()

	var coveredSlots map[uint32]struct{}
	var offset uint32

	if p.WindowID == protocol.WindowIDInventory {
		// WindowIDInventory can mean a full sync (36 items) or a partial
		// sync of one of the player inventory sub-containers. Use the
		// content length to determine which slots are being sent so we
		// don't wipe slots that weren't included in the packet.
		switch len(p.Content) {
		case playerInvSlotCount:
			coveredSlots = slotRange(0, playerInvSlotCount)
			offset = 0
		case 9:
			coveredSlots = slotRange(0, 9)
			offset = 0
		case 27:
			coveredSlots = slotRange(9, 36)
			offset = 9
		case 4:
			coveredSlots = slotRange(36, 40)
			offset = 36
		case 1:
			coveredSlots = slotRange(40, 41)
			offset = 40
		default:
			// Unknown partial size — fall back to the container ID.
			coveredSlots = coveredSlotSet(containerID)
			offset = containerSlotOffset(containerID)
		}
	} else {
		coveredSlots = coveredSlotSet(containerID)
		offset = containerSlotOffset(containerID)
	}

	isFullInventory := len(p.Content) == playerInvSlotCount && p.WindowID == protocol.WindowIDInventory

	// Clear only the slots this container owns, then re-populate from the
	// content array. This prevents stale items from lingering when the
	// server sends a partial update (e.g. hotbar-only after a /give).
	for slot := range coveredSlots {
		delete(b.InventoryMap, slot)
		delete(b.StackNetworkIDs, slot)
	}

	for i, item := range p.Content {
		globalSlot := offset + uint32(i)
		if item.Stack.Count > 0 && item.Stack.NetworkID != 0 {
			b.InventoryMap[globalSlot] = item.Stack
			if item.StackNetworkID != 0 {
				b.StackNetworkIDs[globalSlot] = item.StackNetworkID
			}
		}
	}

	b.Logger.Info("inventory content synced",
		slog.Uint64("window_id", uint64(p.WindowID)),
		slog.Uint64("container_id", uint64(containerID)),
		slog.Bool("full_sync", isFullInventory),
		slog.Int("items", len(p.Content)),
		slog.Uint64("offset", uint64(offset)),
		slog.Int("total_tracked", len(b.InventoryMap)),
	)
}

// applyInventorySlot applies a single-slot InventorySlot update with the
// correct global slot offset for the container.
func applyInventorySlot(b *bot.Bot, p *packet.InventorySlot) {
	containerID := byte(0)
	if c, ok := p.Container.Value(); ok {
		containerID = c.ContainerID
	}
	offset := containerSlotOffset(containerID)
	globalSlot := offset + p.Slot

	b.Mu.Lock()
	defer b.Mu.Unlock()

	if p.NewItem.Stack.Count > 0 && p.NewItem.Stack.NetworkID != 0 {
		b.InventoryMap[globalSlot] = p.NewItem.Stack
		if p.NewItem.StackNetworkID != 0 {
			b.StackNetworkIDs[globalSlot] = p.NewItem.StackNetworkID
		}
	} else {
		delete(b.InventoryMap, globalSlot)
		delete(b.StackNetworkIDs, globalSlot)
	}

	b.Logger.Info("inventory slot synced",
		slog.Uint64("window_id", uint64(p.WindowID)),
		slog.Uint64("container_id", uint64(containerID)),
		slog.Uint64("local_slot", uint64(p.Slot)),
		slog.Uint64("global_slot", uint64(globalSlot)),
		slog.Int("count", int(p.NewItem.Stack.Count)),
		slog.Int("network_id", int(p.NewItem.Stack.NetworkID)),
		slog.Int("total_tracked", len(b.InventoryMap)),
	)
}

// coveredSlotSet returns the set of global slot indices that a container
// owns. Used to clear stale entries before applying a fresh InventoryContent
// update for that container.
func coveredSlotSet(containerID byte) map[uint32]struct{} {
	switch containerID {
	case protocol.ContainerHotBar:
		return slotRange(0, 9) // 9 hotbar slots
	case protocol.ContainerInventory:
		return slotRange(9, 36) // 27 main inventory slots
	case protocol.ContainerArmor:
		return slotRange(36, 40) // 4 armor slots
	case protocol.ContainerOffhand:
		return slotRange(40, 41) // 1 offhand slot
	case protocol.ContainerCombinedHotBarAndInventory:
		return slotRange(0, 36) // 36 combined slots
	default:
		return nil
	}
}

func slotRange(start, end uint32) map[uint32]struct{} {
	m := make(map[uint32]struct{}, int(end-start))
	for i := start; i < end; i++ {
		m[i] = struct{}{}
	}
	return m
}

// transactionSlotToGlobal maps a slot from an InventoryTransaction container
// action to the bot's global inventory slot index. InventoryTransaction uses
// the same 0-35 hotbar+inventory layout as WindowIDInventory, but also
// supports armor (WindowIDArmour) and offhand (WindowIDOffHand).
func transactionSlotToGlobal(action protocol.InventoryAction) (uint32, bool) {
	switch action.WindowID {
	case protocol.WindowIDInventory:
		if action.InventorySlot >= playerInvSlotCount {
			return 0, false
		}
		return action.InventorySlot, true
	case protocol.WindowIDArmour:
		if action.InventorySlot >= 4 {
			return 0, false
		}
		return 36 + action.InventorySlot, true
	case protocol.WindowIDOffHand:
		if action.InventorySlot != 0 {
			return 0, false
		}
		return 40, true
	default:
		return 0, false
	}
}

// applyInventoryTransaction processes server-sent InventoryTransaction
// packets. These are used by many Bedrock servers (PMMP, Nukkit, vanilla) to
// push inventory changes that don't fit into InventorySlot/InventoryContent,
// most importantly item pickups from the ground. Each container action tells
// us the destination slot and the new stack after the transaction.
func applyInventoryTransaction(b *bot.Bot, p *packet.InventoryTransaction) {
	// We only care about NormalTransactionData (or nil, which defaults to
	// normal). Other transaction types (UseItem, ReleaseItem, etc.) describe
	// player interactions and don't directly update persistent inventory slots.
	_, isNormal := p.TransactionData.(*protocol.NormalTransactionData)
	if !isNormal && p.TransactionData != nil {
		b.Logger.Debug("ignoring non-normal inventory transaction",
			slog.String("type", fmt.Sprintf("%T", p.TransactionData)),
		)
		return
	}

	b.Mu.Lock()
	defer b.Mu.Unlock()

	updated := 0
	var updatedSlots []uint32
	for _, action := range p.Actions {
		// Always log the action details so we can diagnose servers that use
		// unexpected source/window combinations for item pickups.
		b.Logger.Debug("inventory transaction action",
			slog.Int("source_type", int(action.SourceType)),
			slog.Int("window_id", int(action.WindowID)),
			slog.Uint64("slot", uint64(action.InventorySlot)),
			slog.Int("old_count", int(action.OldItem.Stack.Count)),
			slog.Int("old_net_id", int(action.OldItem.Stack.NetworkID)),
			slog.Int("new_count", int(action.NewItem.Stack.Count)),
			slog.Int("new_net_id", int(action.NewItem.Stack.NetworkID)),
		)

		if !isPlayerInventoryTransaction(action) {
			continue
		}
		globalSlot, ok := transactionSlotToGlobal(action)
		if !ok {
			continue
		}

		newItem := action.NewItem.Stack
		if newItem.Count > 0 && newItem.NetworkID != 0 {
			b.InventoryMap[globalSlot] = newItem
			if action.NewItem.StackNetworkID != 0 {
				b.StackNetworkIDs[globalSlot] = action.NewItem.StackNetworkID
			}
		} else {
			delete(b.InventoryMap, globalSlot)
			delete(b.StackNetworkIDs, globalSlot)
		}
		updated++
		updatedSlots = append(updatedSlots, globalSlot)
	}

	if updated > 0 {
		b.Logger.Info("inventory transaction synced",
			slog.Int("actions", len(p.Actions)),
			slog.Int("updated_slots", updated),
			slog.Any("slots", updatedSlots),
			slog.Int("total_tracked", len(b.InventoryMap)),
		)
	} else {
		b.Logger.Debug("inventory transaction received but no player inventory slots updated",
			slog.Int("actions", len(p.Actions)),
			slog.Int("total_tracked", len(b.InventoryMap)),
		)
	}
}

// applyItemStackResponse processes an ItemStackResponse packet, which the
// server sends after the client's ItemStackRequest (crafting, moving items,
// dropping, etc.) is approved or rejected. When approved, the response
// contains authoritative slot updates that must be applied to keep
// InventoryMap in sync — otherwise the bot's view of its inventory drifts
// from the server's after every transaction.
func applyItemStackResponse(b *bot.Bot, p *packet.ItemStackResponse) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	for _, resp := range p.Responses {
		// Check if this is a response to a pending craft request.
		pendingCh, craftOutputNetID, hasPending := b.PendingCraftLookup(resp.RequestID)

		if resp.Status != 0 {
			// Non-zero status = rejected. Notify the pending craft if any.
			b.Logger.Info("item stack request rejected",
				slog.Int("request_id", int(resp.RequestID)),
				slog.Uint64("status", uint64(resp.Status)),
			)
			if hasPending {
				select {
				case pendingCh <- bot.CraftResult(false):
				default:
				}
				b.PendingCraftDelete(resp.RequestID)
			}
			continue
		}

		for _, container := range resp.ContainerInfo {
			containerID := container.Container.ContainerID
			offset := containerSlotOffset(containerID)

			// ItemStackResponse can also reference the cursor or crafting
			// containers — skip those since they're not part of the
			// persistent inventory.
			if !isPlayerInventoryContainer(containerID) {
				continue
			}

			for _, slotInfo := range container.SlotInfo {
				globalSlot := offset + uint32(slotInfo.Slot)
				if slotInfo.Count > 0 && slotInfo.StackNetworkID != 0 {
					// StackResponseSlotInfo.StackNetworkID is the unique stack
					// instance ID assigned by the server, NOT the item type
					// NetworkID. Store it in StackNetworkIDs and preserve the
					// existing item type from InventoryMap. The response does
					// not include the item type — the client already knows it
					// from the action it initiated.
					b.StackNetworkIDs[globalSlot] = slotInfo.StackNetworkID
					if existing, ok := b.InventoryMap[globalSlot]; ok {
						existing.Count = uint16(slotInfo.Count)
						b.InventoryMap[globalSlot] = existing
					} else {
						// Slot didn't exist before (e.g. crafted output placed
						// into a previously empty slot). Use the pending
						// craft's output NetworkID if available so the item
						// type is immediately known. Otherwise store a
						// placeholder and let a subsequent
						// InventoryContent/InventorySlot sync fill it in.
						newItem := protocol.ItemStack{
							Count:        uint16(slotInfo.Count),
							HasNetworkID: true,
						}
						if craftOutputNetID != 0 {
							newItem.NetworkID = craftOutputNetID
							newItem.HasNetworkID = true
						}
						b.InventoryMap[globalSlot] = newItem
					}
				} else {
					delete(b.InventoryMap, globalSlot)
					delete(b.StackNetworkIDs, globalSlot)
				}
			}
		}

		// Notify the pending craft that it was accepted.
		if hasPending {
			select {
			case pendingCh <- bot.CraftResult(true):
			default:
			}
			b.PendingCraftDelete(resp.RequestID)
		}
	}
}
