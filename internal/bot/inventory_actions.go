// Package bot provides the core Minecraft bot implementation.
package bot

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot/inventory/station"
	"bedrock-ai/internal/bot/survival"
	"bedrock-ai/internal/safecast"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// DropTargetSlotLocked picks which inventory slot to drop from for a request.
// The held slot wins when it matches — dropping from a random matching stack
// (map iteration order) leaves the held item rendered in the hand after the
// drop, which viewers read as a ghost item — otherwise the lowest numbered
// matching slot, so repeated drops are deterministic. Callers hold b.Mu.
func (b *Bot) DropTargetSlotLocked(name string) (uint32, protocol.ItemStack, bool) {
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
	targetSlot, foundItem, found := b.DropTargetSlotLocked(name)
	if !found {
		b.Mu.Unlock()
		return fmt.Errorf("item %q not found in inventory", name)
	}

	item := protocol.ItemInstance{StackNetworkID: b.StackNetworkIDs[targetSlot], Stack: foundItem}
	geyser := b.GeyserCompat
	b.Mu.Unlock()

	// The Geyser branch runs BEFORE the strict builder on purpose. Geyser's
	// drop path reads only the world action's count and the container action's
	// slot — never the item instances — and this host's slot syncs carry no
	// stack network IDs at all (the tracker deletes the entry, so the ID reads
	// zero). The old order called BuildDropStackAction first, whose zero-ID
	// guard killed every Geyser drop/give with "authoritative stack network ID
	// is missing" before a single packet reached the wire.
	if geyser {
		return b.dropViaHeldSlot(name, targetSlot, item, ClampDropCount(item.Stack.Count, count))
	}

	action, dropped, err := BuildDropStackAction(targetSlot, item, count)
	if err != nil {
		return fmt.Errorf("drop %q from inventory slot %d: %w", name, targetSlot, err)
	}

	// Face-direction swing for viewers, then the authoritative drop request.
	if err := b.Conn.WritePacket(BuildDropSwing(b.Conn.GameData().EntityRuntimeID)); err != nil {
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

// dropViaHeldSlot drops with the NORMAL InventoryTransaction the real client
// sends when you press Q: a container action out of the held hotbar slot plus a
// world action carrying the DROP_ITEM flag.
//
// The wire history here is instructive. A direct slot DropStackRequestAction
// went unanswered (it is the inventory-SCREEN drop; with no inventory open the
// host never processes it), the take-to-cursor path was answered OK but never
// spawned an item, and PlayerAction DropItem — the old Q packet — is simply
// not handled by Geyser's PlayerAction translator at all. Only this
// transaction shape maps to a real Java-side drop, so the stack is selected
// into the hand first (EquipItem, server-confirmed on Geyser) and every toss
// is verified against the authoritative inventory afterwards.
func (b *Bot) dropViaHeldSlot(name string, slot uint32, item protocol.ItemInstance, dropped uint16) error {
	// The container action must address a hotbar slot (0-8): Geyser matches it
	// against the Java held slot, and its fallback path offsets the slot by 36
	// before clicking. A main-inventory slot number would click the wrong Java
	// slot, so anything not in the hand is moved into the hand first.
	if err := b.EquipItem(slot); err != nil {
		return fmt.Errorf("drop %q: select slot %d: %w", name, slot, err)
	}

	b.Mu.Lock()
	held := b.HeldSlot
	b.Mu.Unlock()
	if held >= 9 {
		return fmt.Errorf("drop %q: slot %d never reached the hotbar", name, slot)
	}

	sent := 0
	for remaining := dropped; remaining > 0; {
		b.Mu.Lock()
		stack, ok := b.InventoryMap[held]
		stackNetID := b.StackNetworkIDs[held]
		b.Mu.Unlock()
		if !ok || stack.Count == 0 {
			break
		}

		// Geyser reads the world action's item count as the whole decision:
		// count 1 drops a single item, anything above drops the entire stack.
		// A partial request therefore goes out as one packet per item, while a
		// request that covers the whole stack is a single packet.
		batch := remaining
		if stack.Count > remaining {
			batch = 1
		}

		if err := b.Conn.WritePacket(BuildDropTransaction(held, stackNetID, stack, batch)); err != nil {
			return fmt.Errorf("drop %d %q: send drop transaction: %w", batch, name, err)
		}
		_ = b.Conn.WritePacket(BuildDropSwing(b.Conn.GameData().EntityRuntimeID))
		sent += int(batch)
		remaining -= batch
		if remaining > 0 {
			time.Sleep(dropItemGap)
		}
	}
	if sent == 0 {
		return fmt.Errorf("drop %q: hand slot %d is empty on the server's side", name, held)
	}

	// The toss is only real once the authoritative inventory says so: the host
	// answers with a slot/content resync after the Java server confirms the
	// drop. Before this check, a host that silently ignored the transaction
	// (the PlayerAction era) still reported success while the item sat in the
	// inventory.
	deadline := time.Now().Add(dropVerifyTimeout)
	for {
		b.Mu.Lock()
		stack, ok := b.InventoryMap[held]
		count := uint16(0)
		if ok {
			count = stack.Count
		}
		b.Mu.Unlock()
		if int(count) <= int(item.Stack.Count)-int(dropped) {
			break
		}
		if time.Now().After(deadline) {
			gone := int(item.Stack.Count) - int(count)
			if gone < 0 {
				gone = 0
			}
			return fmt.Errorf("server did not process the drop: %d of %d %s left the inventory", gone, dropped, name)
		}
		time.Sleep(100 * time.Millisecond)
	}

	b.Logger.Info("dropped via held slot transaction", "item", name, "slot", held, "count", dropped, "packets", sent)
	return nil
}

// dropVerifyTimeout is how long the held-slot drop waits for the server's
// authoritative inventory resync before declaring the toss lost.
const dropVerifyTimeout = 2500 * time.Millisecond

// dropItemGap separates consecutive drop packets. Geyser maps each packet's
// world-action count onto Java drop-one/drop-stack, so a partial toss of N
// items is N packets, and back-to-back sends are how a host processes the
// first and loses the rest.
const dropItemGap = 120 * time.Millisecond

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
		mainPlayers := b.AiCfg.GetMainPlayers()
		botName := b.Name
		b.Mu.Unlock()

		if len(mainPlayers) == 0 {
			return
		}
		mainPlayer := mainPlayers[0]

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

// CraftingGridInput is one ingredient already staged on the crafting grid: the
// grid slot, how many items it holds, and the stack network ID the server gave
// it, which every later consume and place action has to echo.
type CraftingGridInput struct {
	Slot           byte
	Count          int
	StackNetworkID int32
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
	picks, err := PlanIngredientConsumption(b.InventoryMap, b.ItemNames, recipe.Ingredients, count)
	if err != nil {
		b.Mu.Unlock()
		return err
	}
	itemName := b.ItemNames[recipe.Output.NetworkID]
	ingredientSources := SnapshotIngredientSources(b.InventoryMap, picks)
	itemNetworkIDs := make(map[uint32]int32, len(picks))
	for _, pick := range picks {
		itemNetworkIDs[pick.Slot] = b.InventoryMap[pick.Slot].NetworkID
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

	if err := b.openPersonalInventory(); err != nil {
		return err
	}
	defer b.closePersonalInventory()

	staging := &craftStaging{
		gridInputs:         make([]CraftingGridInput, 0, len(picks)),
		gridInputIndexes:   make(map[byte]int, len(picks)),
		predictedSourceIDs: make(map[uint32]int32, len(picks)),
		stagedIngredients:  make([]stagedCraftIngredient, 0, len(picks)),
		itemNetworkIDs:     itemNetworkIDs,
		itemName:           itemName,
	}
	for _, pick := range picks {
		gridSlot, err := CraftingGridSlot(recipe, pick.IngredientIndex)
		if err != nil {
			return err
		}
		if err := b.stageCraftIngredient(pick, staging, gridSlot); err != nil {
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

// openPersonalInventory sends the OpenInventory interact and waits for the
// host to register it. Geyser only answers ItemStackRequests once the personal
// inventory is open (crafting always did this; drop and equip did not and sat
// unanswered until the 5s timeout).
func (b *Bot) openPersonalInventory() error {
	if err := b.Conn.WritePacket(&packet.Interact{
		ActionType:            packet.InteractActionOpenInventory,
		TargetEntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
	}); err != nil {
		return fmt.Errorf("open personal inventory: %w", err)
	}
	time.Sleep(InventoryOpenDelay)
	return nil
}

// closePersonalInventory tells Geyser the inventory screen was closed, as the
// vanilla client does. Left open, the host keeps treating the player as inside
// the inventory screen and ignores the next block click (the crafting table
// never opened). Geyser only: other hosts never needed it.
func (b *Bot) closePersonalInventory() {
	if b.WorldCache == nil || !b.WorldCache.GeyserCompat() {
		return
	}
	_ = b.Conn.WritePacket(&packet.ContainerClose{WindowID: 0, ContainerType: 255, ServerSide: false})
	b.Logger.Info("personal inventory closed (geyser)")
}

// equipViaStackRequest moves inventory slot into the hotbar with server
// confirmation and then selects it. An empty hotbar slot is preferred (take +
// place, the same proven pair crafting uses); otherwise the held slot is
// swapped. Local state changes only after the server accepted the move.
func (b *Bot) equipViaStackRequest(slot uint32) error {
	b.Mu.Lock()
	item, ok := b.InventoryMap[slot]
	if !ok || item.Count == 0 {
		b.Mu.Unlock()
		return fmt.Errorf("slot %d empty", slot)
	}
	itemStackID := b.StackNetworkIDs[slot]
	target := b.EmptyHotbarSlotLocked()
	if target >= 9 {
		target = 0
		if b.HeldSlot < 9 {
			target = b.HeldSlot
		}
	}
	hotbarItem := b.InventoryMap[target]
	hotbarStackID := b.StackNetworkIDs[target]
	name := b.ItemNames[item.NetworkID]
	b.Mu.Unlock()

	if err := b.openPersonalInventory(); err != nil {
		return err
	}
	defer b.closePersonalInventory()

	source := PlayerStackRequestSlot(slot, itemStackID)
	dest := PlayerStackRequestSlot(target, hotbarStackID)
	b.Logger.Info("equip via stack request (geyser)", "item", name, "from_slot", slot, "to_slot", target, "swap", hotbarItem.Count > 0)

	var newTargetID, newSourceID int32
	if hotbarItem.Count > 0 {
		reqID, ch := b.beginStackRequest(0)
		res, err := b.sendStackRequest(reqID, ch, []protocol.StackRequestAction{
			&protocol.SwapStackRequestAction{Source: source, Destination: dest},
		}, name)
		if err != nil {
			return fmt.Errorf("equip %q: swap to hotbar slot %d: %w", name, target, err)
		}
		newTargetID = res.stackNetworkID(dest.Container.ContainerID, dest.Slot)
		newSourceID = res.stackNetworkID(source.Container.ContainerID, source.Slot)
	} else {
		takeID, takeCh := b.beginStackRequest(0)
		takeRes, err := b.sendStackRequest(takeID, takeCh, []protocol.StackRequestAction{BuildTakeToCursorAction(source, int(item.Count))}, name)
		if err != nil {
			return fmt.Errorf("equip %q: move to cursor: %w", name, err)
		}
		cursorID := takeRes.stackNetworkID(protocol.ContainerCursor, 0)
		if cursorID == 0 {
			cursorID = takeID
		}
		placeID, placeCh := b.beginStackRequest(item.NetworkID)
		placeRes, err := b.sendStackRequest(placeID, placeCh, []protocol.StackRequestAction{
			buildPlaceFromCursorAction(cursorID, dest, int(item.Count)),
		}, name)
		if err != nil {
			_ = b.returnCursorToInventory(cursorID, slot, int(item.Count), item.NetworkID, name)
			return fmt.Errorf("equip %q: place into hotbar slot %d: %w", name, target, err)
		}
		newTargetID = placeRes.stackNetworkID(dest.Container.ContainerID, dest.Slot)
		if newTargetID == 0 {
			newTargetID = placeID
		}
	}

	// The response handler only rewrites counts and stack IDs; it keeps the old
	// item type in a slot, which is wrong after a move. Set both slots outright.
	b.Mu.Lock()
	b.InventoryMap[target] = item
	if newTargetID == 0 {
		newTargetID = itemStackID
	}
	if newTargetID != 0 {
		b.StackNetworkIDs[target] = newTargetID
	} else {
		delete(b.StackNetworkIDs, target)
	}
	if hotbarItem.Count > 0 {
		b.InventoryMap[slot] = hotbarItem
		if newSourceID == 0 {
			newSourceID = hotbarStackID
		}
		if newSourceID != 0 {
			b.StackNetworkIDs[slot] = newSourceID
		}
	} else {
		delete(b.InventoryMap, slot)
		delete(b.StackNetworkIDs, slot)
	}
	b.HeldSlot = target
	runtimeID := b.Conn.GameData().EntityRuntimeID
	b.Mu.Unlock()

	return b.Conn.WritePacket(&packet.MobEquipment{
		EntityRuntimeID: runtimeID,
		NewItem:         protocol.ItemInstance{StackNetworkID: newTargetID, Stack: survival.WireItemStack(item)},
		InventorySlot:   byte(target),
		HotBarSlot:      byte(target),
		WindowID:        0,
	})
}

// wornArmorSlotBase is the global inventory slot the worn helmet occupies.
// The armor container maps to global slots 36-39 (network/player:
// ContainerSlotOffset). Declared here rather than imported from the survival
// package because the slot mapping belongs to the inventory layer; survival
// only reads it.
const wornArmorSlotBase uint32 = 36

// EquipToOffhand moves an inventory slot into the off-hand container with
// server confirmation, swapping with whatever is already there. The off hand
// is the only place a shield blocks from: Bedrock renders and resolves
// blocking off the off-hand slot, so a shield in the hotbar is a shield that
// does nothing.
//
// The wire shape is the proven take-to-cursor + place-from-cursor pair the
// hotbar equip uses, with the destination container changed to the off hand;
// when the off hand already holds something the whole move is a single swap.
// Local state changes only after the server accepted.
func (b *Bot) EquipToOffhand(slot uint32) error {
	b.Mu.Lock()
	item, ok := b.InventoryMap[slot]
	if !ok || item.Count == 0 {
		b.Mu.Unlock()
		return fmt.Errorf("slot %d empty", slot)
	}
	itemStackID := b.StackNetworkIDs[slot]
	offhandItem := b.InventoryMap[40]
	offhandStackID := b.StackNetworkIDs[40]
	name := b.ItemNames[item.NetworkID]
	b.Mu.Unlock()

	if err := b.openPersonalInventory(); err != nil {
		return err
	}
	defer b.closePersonalInventory()

	source := PlayerStackRequestSlot(slot, itemStackID)
	dest := protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerOffhand},
		Slot:           0,
		StackNetworkID: offhandStackID,
	}
	b.Logger.Info("equip to offhand via stack request", "item", name, "from_slot", slot, "swap", offhandItem.Count > 0)

	var newOffhandID, newSourceID int32
	if offhandItem.Count > 0 {
		reqID, ch := b.beginStackRequest(0)
		res, err := b.sendStackRequest(reqID, ch, []protocol.StackRequestAction{
			&protocol.SwapStackRequestAction{Source: source, Destination: dest},
		}, name)
		if err != nil {
			return fmt.Errorf("equip %q to offhand: swap: %w", name, err)
		}
		newOffhandID = res.stackNetworkID(dest.Container.ContainerID, dest.Slot)
		newSourceID = res.stackNetworkID(source.Container.ContainerID, source.Slot)
	} else {
		takeID, takeCh := b.beginStackRequest(0)
		takeRes, err := b.sendStackRequest(takeID, takeCh, []protocol.StackRequestAction{BuildTakeToCursorAction(source, int(item.Count))}, name)
		if err != nil {
			return fmt.Errorf("equip %q to offhand: move to cursor: %w", name, err)
		}
		cursorID := takeRes.stackNetworkID(protocol.ContainerCursor, 0)
		if cursorID == 0 {
			cursorID = takeID
		}
		placeID, placeCh := b.beginStackRequest(item.NetworkID)
		placeRes, err := b.sendStackRequest(placeID, placeCh, []protocol.StackRequestAction{
			buildPlaceFromCursorAction(cursorID, dest, int(item.Count)),
		}, name)
		if err != nil {
			_ = b.returnCursorToInventory(cursorID, slot, int(item.Count), item.NetworkID, name)
			return fmt.Errorf("equip %q to offhand: place: %w", name, err)
		}
		newOffhandID = placeRes.stackNetworkID(dest.Container.ContainerID, dest.Slot)
		if newOffhandID == 0 {
			newOffhandID = placeID
		}
	}

	// Same shape as the hotbar equip: the response handler rewrites counts and
	// stack IDs but keeps item types in place, and a move means both ends of
	// it changed.
	b.Mu.Lock()
	b.InventoryMap[40] = item
	if newOffhandID == 0 {
		newOffhandID = itemStackID
	}
	if newOffhandID != 0 {
		b.StackNetworkIDs[40] = newOffhandID
	} else {
		delete(b.StackNetworkIDs, 40)
	}
	if offhandItem.Count > 0 {
		b.InventoryMap[slot] = offhandItem
		if newSourceID == 0 {
			newSourceID = offhandStackID
		}
		if newSourceID != 0 {
			b.StackNetworkIDs[slot] = newSourceID
		} else {
			delete(b.StackNetworkIDs, slot)
		}
	} else {
		delete(b.InventoryMap, slot)
		delete(b.StackNetworkIDs, slot)
	}
	b.Mu.Unlock()

	return nil
}

// EquipArmorToSlot moves an inventory slot onto one of the four worn armor
// pieces with server confirmation. Bedrock resolves worn armor through the
// armor container (ContainerArmor, slots 0-3), so the hotbar-select packet
// EquipItem sends can never wear a piece — it only changes what the hand
// holds, which is exactly why "pasang armor" never showed on the body.
//
// Same wire shape as EquipToOffhand: a single swap when something is already
// worn there, take-to-cursor + place-from-cursor otherwise. Local state
// changes only after the server accepted.
func (b *Bot) EquipArmorToSlot(slot uint32, armorSlot byte) error {
	b.Mu.Lock()
	item, ok := b.InventoryMap[slot]
	if !ok || item.Count == 0 {
		b.Mu.Unlock()
		return fmt.Errorf("slot %d empty", slot)
	}
	itemStackID := b.StackNetworkIDs[slot]
	wornGlobal := wornArmorSlotBase + uint32(armorSlot)
	wornItem := b.InventoryMap[wornGlobal]
	wornStackID := b.StackNetworkIDs[wornGlobal]
	name := b.ItemNames[item.NetworkID]
	b.Mu.Unlock()

	if err := b.openPersonalInventory(); err != nil {
		return err
	}
	defer b.closePersonalInventory()

	source := PlayerStackRequestSlot(slot, itemStackID)
	dest := protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerArmor},
		Slot:           armorSlot,
		StackNetworkID: wornStackID,
	}
	b.Logger.Info("equip armor via stack request", "item", name, "from_slot", slot, "armor_slot", armorSlot, "swap", wornItem.Count > 0)

	var newWornID, newSourceID int32
	if wornItem.Count > 0 {
		reqID, ch := b.beginStackRequest(0)
		res, err := b.sendStackRequest(reqID, ch, []protocol.StackRequestAction{
			&protocol.SwapStackRequestAction{Source: source, Destination: dest},
		}, name)
		if err != nil {
			return fmt.Errorf("equip %q to armor slot %d: swap: %w", name, armorSlot, err)
		}
		newWornID = res.stackNetworkID(dest.Container.ContainerID, dest.Slot)
		newSourceID = res.stackNetworkID(source.Container.ContainerID, source.Slot)
	} else {
		takeID, takeCh := b.beginStackRequest(0)
		takeRes, err := b.sendStackRequest(takeID, takeCh, []protocol.StackRequestAction{BuildTakeToCursorAction(source, int(item.Count))}, name)
		if err != nil {
			return fmt.Errorf("equip %q to armor slot %d: move to cursor: %w", name, armorSlot, err)
		}
		cursorID := takeRes.stackNetworkID(protocol.ContainerCursor, 0)
		if cursorID == 0 {
			cursorID = takeID
		}
		placeID, placeCh := b.beginStackRequest(item.NetworkID)
		placeRes, err := b.sendStackRequest(placeID, placeCh, []protocol.StackRequestAction{
			buildPlaceFromCursorAction(cursorID, dest, int(item.Count)),
		}, name)
		if err != nil {
			_ = b.returnCursorToInventory(cursorID, slot, int(item.Count), item.NetworkID, name)
			return fmt.Errorf("equip %q to armor slot %d: place: %w", name, armorSlot, err)
		}
		newWornID = placeRes.stackNetworkID(dest.Container.ContainerID, dest.Slot)
		if newWornID == 0 {
			newWornID = placeID
		}
	}

	b.Mu.Lock()
	b.InventoryMap[wornGlobal] = item
	if newWornID == 0 {
		newWornID = itemStackID
	}
	if newWornID != 0 {
		b.StackNetworkIDs[wornGlobal] = newWornID
	} else {
		delete(b.StackNetworkIDs, wornGlobal)
	}
	if wornItem.Count > 0 {
		b.InventoryMap[slot] = wornItem
		if newSourceID == 0 {
			newSourceID = wornStackID
		}
		if newSourceID != 0 {
			b.StackNetworkIDs[slot] = newSourceID
		} else {
			delete(b.StackNetworkIDs, slot)
		}
	} else {
		delete(b.InventoryMap, slot)
		delete(b.StackNetworkIDs, slot)
	}
	b.Mu.Unlock()

	return nil
}

// craftStaging keeps the authoritative grid stack IDs for personal and table
// crafting. A rejected transfer can return already-staged items to their source.
type craftStaging struct {
	gridInputs         []CraftingGridInput
	gridInputIndexes   map[byte]int
	predictedSourceIDs map[uint32]int32
	stagedIngredients  []stagedCraftIngredient
	itemNetworkIDs     map[uint32]int32
	itemName           string
}

// stageCraftIngredient stages one ingredient (inventory -> cursor -> crafting
// input) at the slot chosen for the currently open crafting grid.
func (b *Bot) stageCraftIngredient(pick IngredientPick, s *craftStaging, gridSlot byte) error {
	cursorStackID, err := b.takeIngredientToCursor(pick, s)
	if err != nil {
		return err
	}
	return b.placeIngredientOnGrid(pick, s, gridSlot, cursorStackID)
}

// takeIngredientToCursor moves pick.Count items from the ingredient's inventory
// slot onto the cursor and records the authoritative stack ID for later place
// requests. Returns the cursor stack ID, falling back to the take request ID
// when the server does not assign one.
func (b *Bot) takeIngredientToCursor(pick IngredientPick, s *craftStaging) (int32, error) {
	stackNetworkID := s.predictedSourceIDs[pick.Slot]
	if stackNetworkID == 0 {
		b.Mu.Lock()
		stackNetworkID = b.StackNetworkIDs[pick.Slot]
		b.Mu.Unlock()
	}
	if stackNetworkID == 0 {
		return 0, fmt.Errorf("cannot craft: slot %d has invalid StackNetworkID (0)", pick.Slot)
	}

	source := PlayerStackRequestSlot(pick.Slot, stackNetworkID)
	take := BuildTakeToCursorAction(source, pick.Count)
	takeID, takeCh := b.beginStackRequest(0)
	takeResult, err := b.sendStackRequest(takeID, takeCh, []protocol.StackRequestAction{take}, s.itemName)
	if err != nil {
		if errors.Is(err, errStackRequestRejected) && len(s.stagedIngredients) > 0 {
			if restoreErr := b.restoreCraftingGrid(s.stagedIngredients, s.gridInputs, s.gridInputIndexes, s.itemName); restoreErr != nil {
				return 0, fmt.Errorf("take ingredient from slot %d: %w; restore crafting grid: %v", pick.Slot, err, restoreErr)
			}
		}
		return 0, fmt.Errorf("take ingredient from slot %d: %w", pick.Slot, err)
	}
	s.predictedSourceIDs[pick.Slot] = takeResult.stackNetworkID(source.Container.ContainerID, source.Slot)
	if s.predictedSourceIDs[pick.Slot] == 0 {
		s.predictedSourceIDs[pick.Slot] = takeID
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
func (b *Bot) placeIngredientOnGrid(pick IngredientPick, s *craftStaging, gridSlot byte, cursorStackID int32) error {
	destinationStackID := int32(0)
	if index, exists := s.gridInputIndexes[gridSlot]; exists {
		destinationStackID = s.gridInputs[index].StackNetworkID
	}
	place := BuildPlaceCursorToCraftingAction(cursorStackID, gridSlot, destinationStackID, pick.Count)
	placeID, placeCh := b.beginStackRequest(0)
	placeResult, err := b.sendStackRequest(placeID, placeCh, []protocol.StackRequestAction{place}, s.itemName)
	if err != nil {
		if errors.Is(err, errStackRequestRejected) {
			if restoreErr := b.returnCursorToInventory(cursorStackID, pick.Slot, pick.Count, s.itemNetworkIDs[pick.Slot], s.itemName); restoreErr != nil {
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
		s.gridInputs[index].Count += pick.Count
		s.gridInputs[index].StackNetworkID = gridStackID
	} else {
		s.gridInputIndexes[gridSlot] = len(s.gridInputs)
		s.gridInputs = append(s.gridInputs, CraftingGridInput{Slot: gridSlot, Count: pick.Count, StackNetworkID: gridStackID})
	}
	s.stagedIngredients = append(s.stagedIngredients, stagedCraftIngredient{
		sourceSlot:    pick.Slot,
		gridSlot:      gridSlot,
		count:         pick.Count,
		itemNetworkID: s.itemNetworkIDs[pick.Slot],
	})
	return nil
}

// finalizeCraft submits the craft request for the staged grid, reconciles the
// inventory snapshot on success, and restores the partially-filled grid on
// rejection. Extracted from CraftItem so the main function stays a thin
// validate → stage → finalize orchestrator.
func (b *Bot) finalizeCraft(recipe RecipeInfo, recipeNetID uint32, count int, outputSlot uint32, ingredientSources map[uint32]IngredientSourceSnapshot, s *craftStaging) error {
	requestID, resultCh := b.beginStackRequest(recipe.Output.NetworkID)
	actions, err := BuildCraftActions(requestID, recipeNetID, recipe, count, s.gridInputs, outputSlot, s.itemName)
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
	ReconcileCraftIngredientCounts(b.InventoryMap, b.StackNetworkIDs, ingredientSources)
	b.Mu.Unlock()

	b.Logger.Info("CraftItem accepted",
		"recipeNetID", recipeNetID,
		"item", s.itemName,
		"count", count,
		"inventory", b.GetInventorySummary(),
	)
	return nil
}

// CraftItemOnTable fills the table's nine-slot input before submitting the
// same manual craft/consume/place sequence as CraftItem. The workbench window
// must be open and confirmed before staging any ingredients.
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
	if err := ValidateTableCraftRecipe(recipe); err != nil {
		b.Mu.Unlock()
		return err
	}
	picks, err := PlanIngredientConsumption(b.InventoryMap, b.ItemNames, recipe.Ingredients, count)
	if err != nil {
		b.Mu.Unlock()
		return err
	}
	itemName := b.ItemNames[recipe.Output.NetworkID]
	sources := SnapshotIngredientSources(b.InventoryMap, picks)
	itemNetworkIDs := make(map[uint32]int32, len(picks))
	for _, pick := range picks {
		itemNetworkIDs[pick.Slot] = b.InventoryMap[pick.Slot].NetworkID
	}
	outputSlot, hasOutputSlot := findFirstEmptyPlayerSlot(b.InventoryMap)
	b.Mu.Unlock()

	outputCount := int(recipe.Output.Count) * count
	if outputCount <= 0 || outputCount > MaxStackSize {
		return fmt.Errorf("crafted output count %d is unsupported", outputCount)
	}
	if itemName == "" {
		return fmt.Errorf("cannot determine the item name of recipe output %+v", recipe.Output)
	}
	if !hasOutputSlot {
		return fmt.Errorf("inventory full, cannot place crafted output")
	}

	b.Logger.Info("CraftItemOnTable manual grid", "recipeNetID", recipeNetID,
		"item", itemName, "count", count, "ingredientCount", len(picks))
	staging := &craftStaging{
		gridInputs:         make([]CraftingGridInput, 0, len(picks)),
		gridInputIndexes:   make(map[byte]int, len(picks)),
		predictedSourceIDs: make(map[uint32]int32, len(picks)),
		stagedIngredients:  make([]stagedCraftIngredient, 0, len(picks)),
		itemNetworkIDs:     itemNetworkIDs,
		itemName:           itemName,
	}
	for _, pick := range picks {
		gridSlot, err := CraftingTableGridSlot(recipe, pick.IngredientIndex)
		if err != nil {
			return err
		}
		if err := b.stageCraftIngredient(pick, staging, gridSlot); err != nil {
			return err
		}
	}
	return b.finalizeCraft(recipe, recipeNetID, count, outputSlot, sources, staging)
}

// ValidateTableCraftRecipe rejects recipes the workbench grid cannot express.
// Non-crafting-table blocks keep their own UI, and anything larger than the
// 3×3 grid has to be left to the server's recipe book.
func ValidateTableCraftRecipe(recipe RecipeInfo) error {
	if recipe.Block != "crafting_table" {
		return fmt.Errorf("recipe %s requires its own interface, not a crafting table", recipe.Block)
	}
	if recipe.Shapeless {
		if len(recipe.Ingredients) > 9 {
			return fmt.Errorf("shapeless recipe has %d ingredients, more than the 3x3 grid holds", len(recipe.Ingredients))
		}
		return nil
	}
	if recipe.Width <= 0 || recipe.Height <= 0 || recipe.Width > 3 || recipe.Height > 3 {
		return fmt.Errorf("invalid crafting table shape %dx%d", recipe.Width, recipe.Height)
	}
	return nil
}

// CraftingTableGridSlot maps a recipe ingredient to its slot in the open
// crafting table's 3×3 input, anchored at the top-left like a player does. A
// shaped pattern may legally sit anywhere in the grid, so the top-left anchor
// keeps one recipe mapping to one stable set of slots.
func CraftingTableGridSlot(recipe RecipeInfo, ingredientIndex int) (byte, error) {
	if recipe.Shapeless {
		if ingredientIndex < 0 || ingredientIndex >= 9 {
			return 0, fmt.Errorf("ingredient %d does not fit the 3x3 crafting table grid", ingredientIndex)
		}
		return byte(CraftingTableGridBaseSlot + ingredientIndex), nil
	}
	if ingredientIndex < 0 || ingredientIndex >= int(recipe.Width*recipe.Height) {
		return 0, fmt.Errorf("ingredient %d is outside recipe shape", ingredientIndex)
	}
	row := ingredientIndex / int(recipe.Width)
	column := ingredientIndex % int(recipe.Width)
	return byte(CraftingTableGridBaseSlot + row*3 + column), nil
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

func CraftingGridSlot(recipe RecipeInfo, ingredientIndex int) (byte, error) {
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

func PlayerStackRequestSlot(slot uint32, stackNetworkID int32) protocol.StackRequestSlotInfo {
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

func BuildTakeToCursorAction(source protocol.StackRequestSlotInfo, count int) *protocol.TakeStackRequestAction {
	action := &protocol.TakeStackRequestAction{}
	action.Count = byte(count)
	action.Source = source
	action.Destination = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCursor},
		Slot:      0,
	}
	return action
}

func BuildPlaceCursorToCraftingAction(cursorStackID int32, gridSlot byte, destinationStackID int32, count int) *protocol.PlaceStackRequestAction {
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
	action := buildPlaceFromCursorAction(cursorStackID, PlayerStackRequestSlot(destinationSlot, destinationStackID), count)
	requestID, resultCh := b.beginStackRequest(itemNetworkID)
	_, err := b.sendStackRequest(requestID, resultCh, []protocol.StackRequestAction{action}, itemName)
	return err
}

func (b *Bot) restoreCraftingGrid(staged []stagedCraftIngredient, gridInputs []CraftingGridInput, indexes map[byte]int, itemName string) error {
	for i := len(staged) - 1; i >= 0; i-- {
		entry := staged[i]
		gridInput := &gridInputs[indexes[entry.gridSlot]]
		source := protocol.StackRequestSlotInfo{
			Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCraftingInput},
			Slot:           entry.gridSlot,
			StackNetworkID: gridInput.StackNetworkID,
		}
		takeID, takeCh := b.beginStackRequest(0)
		takeResult, err := b.sendStackRequest(takeID, takeCh, []protocol.StackRequestAction{BuildTakeToCursorAction(source, entry.count)}, itemName)
		if err != nil {
			return err
		}
		gridInput.Count -= entry.count
		gridInput.StackNetworkID = takeResult.stackNetworkID(protocol.ContainerCraftingInput, entry.gridSlot)
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

// describeStackActions renders each action with its concrete type and fields.
// %+v alone prints nested pointers as addresses, which is useless in a log.
func describeStackActions(actions []protocol.StackRequestAction) string {
	parts := make([]string, 0, len(actions))
	for _, a := range actions {
		parts = append(parts, fmt.Sprintf("%T%+v", a, reflect.Indirect(reflect.ValueOf(a)).Interface()))
	}
	return strings.Join(parts, " | ")
}

func (b *Bot) sendStackRequest(requestID int32, resultCh chan craftResult, actions []protocol.StackRequestAction, itemName string) (craftResult, error) {
	request := protocol.ItemStackRequest{RequestID: requestID, Actions: actions, FilterCause: -1}
	if err := b.Conn.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{request}}); err != nil {
		b.Mu.Lock()
		delete(b.pendingCrafts, requestID)
		b.Mu.Unlock()
		return craftResult{}, fmt.Errorf("send item stack request: %w", err)
	}
	b.Logger.Info("CraftItem awaiting response", "item", itemName, "requestID", requestID, "actions", len(actions),
		"detail", describeStackActions(actions))

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
		// The entry has to go here too, not just on the two exits above. A
		// timed-out request is still sitting in pendingCrafts holding a channel
		// nobody will ever read, and every one of those is a map entry that never
		// comes back. A bot that crafts through a laggy server grows this map
		// for the rest of the session, and a server that answers after the
		// timeout still finds a listener to write to — which is worse, because
		// the result arrives into a channel the craft path has already given up
		// on.
		b.Mu.Lock()
		delete(b.pendingCrafts, requestID)
		b.Mu.Unlock()
		return craftResult{}, fmt.Errorf("server did not respond to item stack request within 5s (item: %s)", itemName)
	}
}

// PlaceLapisInEnchantingTable moves count lapis into the enchanting table's
// material slot, which is what an option is charged against. The server refuses
// an option outright when that slot is empty, so this runs before any option is
// applied rather than as part of it.
//
// The transfer is the same server-authoritative PlaceIntoContainerSlotIn the
// other station actions use, addressed by protocol container ID rather than by
// windowID. windowID is accepted because station.Enchanter's shape includes it,
// and is deliberately unused: the server resolves this slot against
// protocol.ContainerEnchantingMaterial, and a container it does not recognise is
// refused. The lapis, not the tool, is what goes in the material container.
func (e EnchantTable) PlaceLapisInEnchantingTable(windowID byte, count int) error {
	if e.B == nil {
		return ErrNoBot
	}
	if count <= 0 {
		return fmt.Errorf("bot: refusing to place %d lapis in an enchanting table", count)
	}

	slot, ok := e.B.FindItemSlotByName(LapisItemName)
	if !ok {
		return fmt.Errorf("bot: no %s in the inventory to enchant with", LapisItemName)
	}

	// destStackNetID 0 is the empty-slot convention the station manager already
	// uses for the tool, and the material slot is empty on a table that has not
	// been used this session.
	if err := e.B.PlaceIntoContainerSlotIn(
		station.EnchantMaterialContainerID, station.EnchantMaterialSlot, 0, slot, count,
	); err != nil {
		return fmt.Errorf("bot: place %s in the enchanting table: %w", LapisItemName, err)
	}
	return nil
}

// ApplyEnchant selects the option at index and spends it, blocking until the
// server answers.
//
// What it returns means exactly this: the server received the option's recipe
// network ID in a CraftRecipe action and accepted the resulting ItemStackRequest.
// It is not "the request was sent", and it is not "the item is now enchanted".
// The protocol puts those apart and this method can only reach the first one:
//
//   - The response's non-zero status is a rejection, and that is a real server
//     signal, so a rejection is reported as an error rather than swallowed.
//   - An accepted status proves the request was processed. The response's
//     per-slot payload has no item identity and no enchantment list, so nothing
//     in it can say what the item now is.
//
// Confirming the result is therefore the manager's job, and it does it by
// re-reading the table's input slot after this returns. That check is weaker
// than it looks — see EnchantItem — so this method's contract deliberately stops
// short of claiming the enchant landed rather than borrowing a certainty it does
// not have. A refusal here, though, is always a refusal: every early return
// below is a case where no enchantment was bought.
func (e EnchantTable) ApplyEnchant(windowID byte, optionIndex int) error {
	if e.B == nil {
		return ErrNoBot
	}

	option, err := EnchantOptionAt(ReadEnchantOptions(e.B), optionIndex)
	if err != nil {
		return err
	}

	// windowID is unused for the same reason it is unused in
	// PlaceLapisInEnchantingTable: a CraftRecipe action carries no slot, so the
	// request has nowhere to put a window ID even if the server wanted one.
	actions := BuildApplyEnchantActions(option)
	requestID, resultCh := e.B.beginStackRequest(0)
	if _, err := e.B.sendStackRequest(requestID, resultCh, actions, "enchanting table"); err != nil {
		return fmt.Errorf("bot: apply enchantment %q (recipe %d): %w", option.Name, option.RecipeNetworkID, err)
	}
	return nil
}

// BuildCraftActions assembles the stack request actions for one craft. Craft
// results are addressed by namespaced item identifier rather than network ID, so
// the output item must be resolvable to a name; otherwise the server would
// receive an empty identifier and reject the request without a clear reason.
func BuildCraftActions(requestID int32, recipeNetID uint32, recipe RecipeInfo, count int, gridInputs []CraftingGridInput, outputSlot uint32, outputName string) ([]protocol.StackRequestAction, error) {
	outputCount := int(recipe.Output.Count) * count
	resultItem := recipe.Output
	resultItem.Count = safecast.To[uint16](outputCount)
	resultStackItem, ok := StackRequestItemFromStack(resultItem, outputName)
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
				Count: byte(input.Count),
				Source: protocol.StackRequestSlotInfo{
					Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCraftingInput},
					Slot:           input.Slot,
					StackNetworkID: input.StackNetworkID,
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
