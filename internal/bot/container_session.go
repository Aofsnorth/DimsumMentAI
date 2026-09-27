package bot

import (
	"bedrock-ai/internal/bot/storage"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// ContainerWatchState tracks one open container window (a chest, a barrel, a
// shulker box). The bot opens a container, the server answers with
// ContainerOpen (which assigns the window ID) and then InventoryContent /
// InventorySlot packets carrying the real contents.
//
// Everything here lives under b.Mu, same as the player inventory state: the
// packets arrive on the network goroutine, and the chest actions read the
// snapshot from the action goroutine.
type ContainerWatchState struct {
	WindowID byte
	Type     byte
	Pos      protocol.BlockPos
	Opened   bool

	// Items is the latest known slot -> instance of the open container. The
	// full ItemInstance is kept rather than the bare ItemStack because the
	// transfer actions need BOTH halves: Stack.NetworkID names the item type
	// (for the name lookup) and StackNetworkID names this specific stack
	// instance (for the server to validate the move). Keeping only the
	// ItemStack silently sent an item-type ID where a stack ID belongs, which
	// the server rejects as an unknown stack.
	Items map[uint32]protocol.ItemInstance

	// ContentSeen flips true on the first content/slot packet for this
	// window, so a caller can tell an empty chest from one whose contents
	// never arrived.
	ContentSeen bool

	// openCh, contentCh and closedCh each carry at most one signal; sends
	// are non-blocking because a watcher may already be gone.
	openCh    chan struct{}
	contentCh chan struct{}
	closedCh  chan struct{}
}

func newContainerWatch() *ContainerWatchState {
	return &ContainerWatchState{
		Items:     make(map[uint32]protocol.ItemInstance),
		openCh:    make(chan struct{}, 1),
		contentCh: make(chan struct{}, 1),
		closedCh:  make(chan struct{}, 1),
	}
}

// BeginContainerWatch resets the container session so the next ContainerOpen
// (and its content packets) is captured. Call it right before clicking the
// container block.
func (b *Bot) BeginContainerWatch() {
	b.Mu.Lock()
	b.ContainerWatch = newContainerWatch()
	b.Mu.Unlock()
}

// WaitContainerOpen blocks until the server opens a container window, or the
// timeout elapses. It returns the assigned window ID and the block position of
// the container that was opened.
func (b *Bot) WaitContainerOpen(ctx context.Context, timeout time.Duration) (byte, protocol.BlockPos, bool) {
	deadline := time.After(timeout)
	for {
		b.Mu.Lock()
		w := b.ContainerWatch
		if w != nil && w.Opened {
			id, pos := w.WindowID, w.Pos
			b.Mu.Unlock()
			return id, pos, true
		}
		var openCh chan struct{}
		if w != nil {
			openCh = w.openCh
		}
		b.Mu.Unlock()

		if openCh == nil {
			// Nobody armed a watch — fail fast rather than hanging for the
			// full timeout on a programming error.
			return 0, protocol.BlockPos{}, false
		}
		select {
		case <-openCh:
		case <-deadline:
			return 0, protocol.BlockPos{}, false
		case <-ctx.Done():
			return 0, protocol.BlockPos{}, false
		}
	}
}

// WaitContainerContent blocks until at least one content packet has been
// applied to the open window, then returns a copy of the slot map.
func (b *Bot) WaitContainerContent(ctx context.Context, timeout time.Duration) (map[uint32]protocol.ItemInstance, bool) {
	deadline := time.After(timeout)
	for {
		items, seen, contentCh, closed := b.containerSnapshot()
		if seen {
			return items, true
		}
		if closed {
			return items, false
		}
		if contentCh == nil {
			return nil, false
		}
		select {
		case <-contentCh:
		case <-deadline:
			items, seen, _, _ := b.containerSnapshot()
			return items, seen
		case <-ctx.Done():
			items, seen, _, _ := b.containerSnapshot()
			return items, seen
		}
	}
}

// ContainerItems returns the latest known contents of the open container.
func (b *Bot) ContainerItems() map[uint32]protocol.ItemInstance {
	items, _, _, _ := b.containerSnapshot()
	return items
}

func (b *Bot) containerSnapshot() (items map[uint32]protocol.ItemInstance, seen bool, contentCh chan struct{}, closed bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	w := b.ContainerWatch
	if w == nil {
		return nil, false, nil, true
	}
	out := make(map[uint32]protocol.ItemInstance, len(w.Items))
	for slot, item := range w.Items {
		out[slot] = item
	}
	return out, w.ContentSeen, w.contentCh, false
}

// markContainerClosed clears the session. Server close echoes and
// server-initiated closes both land here.
func (b *Bot) MarkContainerClosed(windowID byte) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	w := b.ContainerWatch
	if w == nil || !w.Opened || w.WindowID != windowID {
		return
	}
	b.ContainerWatch = nil
	select {
	case w.closedCh <- struct{}{}:
	default:
	}
}

// containerOpened records a ContainerOpen for a pending watch.
func (b *Bot) ContainerOpened(windowID, ctype byte, pos protocol.BlockPos) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	w := b.ContainerWatch
	if w == nil {
		return
	}
	// The click that opens a chest can make the server open a window it
	// re-assigns; adopt whatever window the server says is open now.
	w.WindowID = windowID
	w.Type = ctype
	w.Pos = pos
	w.Opened = true
	select {
	case w.openCh <- struct{}{}:
	default:
	}
}

// containerContent replaces the known contents of the window.
func (b *Bot) ContainerContent(windowID uint32, items []protocol.ItemInstance) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	w := b.ContainerWatch
	if w == nil || !w.Opened || uint32(w.WindowID) != windowID {
		return
	}
	w.Items = make(map[uint32]protocol.ItemInstance, len(items))
	for i, item := range items {
		if item.Stack.Count <= 0 {
			continue
		}
		w.Items[uint32(i)] = item
	}
	w.ContentSeen = true
	select {
	case w.contentCh <- struct{}{}:
	default:
	}
}

// ContainerSlot updates one slot of the open window.
func (b *Bot) ContainerSlot(windowID uint32, slot uint32, item protocol.ItemInstance) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	w := b.ContainerWatch
	if w == nil || !w.Opened || uint32(w.WindowID) != windowID {
		return
	}
	if item.Stack.Count <= 0 {
		delete(w.Items, slot)
	} else {
		w.Items[slot] = item
	}
	w.ContentSeen = true
	select {
	case w.contentCh <- struct{}{}:
	default:
	}
}

// containerMatchesWindow reports whether a window-bearing packet belongs to the
// container currently being watched.
func (b *Bot) ContainerMatchesWindow(windowID uint32) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	w := b.ContainerWatch
	return w != nil && w.Opened && uint32(w.WindowID) == windowID
}

// CloseContainerWindow tells the server the bot closed the window and clears
// the session. A vanilla client always closes the windows it opens — staying
// silent would leave the chest visually open for viewers and desync the next
// open.
func (b *Bot) CloseContainerWindow(windowID byte) {
	_ = b.Conn.WritePacket(&packet.ContainerClose{WindowID: windowID, ServerSide: false})
	b.Mu.Lock()
	b.ContainerWatch = nil
	b.Mu.Unlock()
}

// containerTransferContext is shared plumbing for the two transfer directions.
type containerTransferContext struct {
	windowID byte
}

// firstFreeInventorySlot returns the lowest free slot in the combined
// hotbar+inventory (0..35). Callers hold b.Mu.
func (b *Bot) firstFreeInventorySlot() (uint32, bool) {
	for slot := uint32(0); slot < 36; slot++ {
		item, ok := b.InventoryMap[slot]
		if !ok || item.Count <= 0 {
			return slot, true
		}
	}
	return 0, false
}

// findInventorySlotFor returns the slot where itemName should go: the first
// stack it can merge onto when possible, otherwise a free slot. Callers hold
// b.Mu.
func (b *Bot) findInventorySlotFor(itemName string) (uint32, bool) {
	want := strings.ToLower(itemName)
	for slot := uint32(0); slot < 36; slot++ {
		item, ok := b.InventoryMap[slot]
		if !ok || item.Count <= 0 {
			continue
		}
		if int(item.Count) >= MaxStackSize {
			continue
		}
		if strings.Contains(strings.ToLower(b.ItemNames[item.NetworkID]), want) {
			return slot, true
		}
	}
	return b.firstFreeInventorySlot()
}

// findContainerSlotFor picks where an item should land inside an open
// container: the first stack of the same item that still has room, otherwise
// the first empty slot. Callers hold b.Mu.
func (b *Bot) findContainerSlotFor(items map[uint32]protocol.ItemInstance, itemName string) (uint32, bool) {
	want := strings.ToLower(itemName)
	for slot := uint32(0); slot < 27; slot++ {
		item, ok := items[slot]
		if !ok || item.Stack.Count <= 0 {
			continue
		}
		if int(item.Stack.Count) >= MaxStackSize {
			continue
		}
		if strings.Contains(strings.ToLower(b.ItemNames[item.Stack.NetworkID]), want) {
			return slot, true
		}
	}
	for slot := uint32(0); slot < 27; slot++ {
		item, ok := items[slot]
		if !ok || item.Stack.Count <= 0 {
			return slot, true
		}
	}
	return 0, false
}

// TakeFromContainerSlot moves up to count items from one slot of an open
// container into the bot's inventory through a server-authoritative
// ItemStackRequest. The authoritative response updates InventoryMap the same
// way crafting does, so no local guessing happens.
func (b *Bot) TakeFromContainerSlot(windowID byte, slot uint32, count int, stackNetID int32, itemName string) error {
	if count <= 0 {
		count = MaxStackSize
	}

	b.Mu.Lock()
	free, ok := b.firstFreeInventorySlot()
	if !ok {
		b.Mu.Unlock()
		return fmt.Errorf("inventory full, cannot take %s", itemName)
	}
	source := protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: windowID},
		Slot:           byte(slot),
		StackNetworkID: stackNetID,
	}
	b.Mu.Unlock()

	take := &protocol.TakeStackRequestAction{}
	take.Count = byte(count)
	take.Source = source
	take.Destination = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:      byte(free),
	}

	requestID, resultCh := b.beginStackRequest(0)
	if _, err := b.sendStackRequest(requestID, resultCh, []protocol.StackRequestAction{take}, itemName); err != nil {
		return fmt.Errorf("take %s from container slot %d: %w", itemName, slot, err)
	}
	return nil
}

// PlaceIntoContainerSlot moves up to count items from one of the bot's
// inventory slots into one slot of an open container. destStackNetID is the
// authoritative stack ID already in that container slot (0 when empty), which
// the server cross-checks; sending the item type there instead makes it reject
// the move as an unknown stack.
func (b *Bot) PlaceIntoContainerSlot(windowID byte, containerSlot uint32, destStackNetID int32, srcSlot uint32, count int) error {
	b.Mu.Lock()
	item, ok := b.InventoryMap[srcSlot]
	if !ok || item.Count <= 0 {
		b.Mu.Unlock()
		return fmt.Errorf("no item in inventory slot %d", srcSlot)
	}
	if count <= 0 || count > int(item.Count) {
		count = int(item.Count)
	}
	srcStackNetID := b.StackNetworkIDs[srcSlot]
	name := b.ItemNames[item.NetworkID]
	b.Mu.Unlock()

	place := &protocol.PlaceStackRequestAction{}
	place.Count = byte(count)
	place.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: byte(protocol.ContainerCombinedHotBarAndInventory)},
		Slot:           byte(srcSlot),
		StackNetworkID: srcStackNetID,
	}
	place.Destination = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: windowID},
		Slot:           byte(containerSlot),
		StackNetworkID: destStackNetID,
	}

	requestID, resultCh := b.beginStackRequest(0)
	if _, err := b.sendStackRequest(requestID, resultCh, []protocol.StackRequestAction{place}, name); err != nil {
		return fmt.Errorf("place %s into container slot %d: %w", name, containerSlot, err)
	}
	return nil
}

// Storage returns the container service. It is built on first use so the bot
// struct does not grow a field for it, and the service itself holds no world
// state, so a rejoin cannot leave it reasoning about a world that is gone.
func (b *Bot) Storage() *storage.Service {
	if b.storageSvc == nil {
		b.storageSvc = storage.New(b)
	}
	return b.storageSvc
}

// BlockLoaded reports terrain knowledge for a cell: whether the bot has data
// there, and whether the cell is solid. Storage's line-of-sight walk needs
// exactly this distinction — an unknown cell cannot occlude, because the bot
// has no idea what is in it.
func (b *Bot) BlockLoaded(x, y, z int32) (solid bool, loaded bool) {
	if b.WorldCache == nil {
		return false, false
	}
	return b.WorldCache.IsBlockSolid(x, y, z)
}

// SignText returns the text of a sign at a position, when the chunk payload
// carried it. Signs are block entities, so the text only exists once the chunk
// holding the sign has been decoded.
func (b *Bot) SignText(x, y, z int32) (string, bool) {
	if b.WorldCache == nil {
		return "", false
	}
	return b.WorldCache.SignText(x, y, z)
}

// ContainerItemName resolves a display name for a container stack.
func (b *Bot) ContainerItemName(item protocol.ItemInstance) string {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if name, ok := b.ItemNames[item.Stack.NetworkID]; ok {
		return name
	}
	return "unknown"
}

// FindInventorySlotFor reports where an item can land in the bot's inventory:
// an existing partial stack it can merge onto, otherwise the first free slot.
func (b *Bot) FindInventorySlotFor(name string) (uint32, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.findInventorySlotFor(name)
}

// ContainerItemName resolves the display name of an item instance in the open
// container, "unknown" when the runtime ID is not in the name table yet.
func (b *Bot) ContainerItemNameLegacy(item protocol.ItemInstance) string {
	return b.ContainerItemName(item)
}
