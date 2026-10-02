package station_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"bedrock-ai/internal/bot/inventory/station"
)

// fakeBot is an in-memory stand-in for the slice of *bot.Bot a station drives.
// It is deliberately a real simulation rather than a pile of canned returns:
// place and take actually move items between the inventory and the open
// container, so a test asserting "the potion ended up in the inventory" is
// asserting on state the manager had to cause, not on a value the fake handed it.
type fakeBot struct {
	mu sync.Mutex

	coords    mgl32.Vec3
	blocks    map[[3]int32]string
	inv       map[uint32]protocol.ItemStack
	names     map[int32]string
	container map[uint32]protocol.ItemInstance

	windowID byte
	navFails bool
	// clickFails makes ClickBlockAt report a failure with clickReason, the way
	// the real interactor does when the server refuses the interaction.
	clickFails  bool
	clickReason string
	// openFails makes the server never assign a window.
	openFails bool

	// events records ordered side-effect calls so a test can assert the watch
	// was armed before the click and the window was closed by the assigned ID.
	events []string

	closedWindow byte
	closedLook   bool
	clicked      bool
	watched      bool

	// brewSequence is what the server turns the bottles into, one entry per
	// cycle: a glass bottle needs an awkward pass before it can be healed.
	// brewAfterReads is how many polls the stand takes per cycle, so the test
	// stays deterministic instead of sleeping.
	reads          int
	brewSequence   []string
	brewIndex      int
	brewTicks      int
	brewAfterReads int

	// outputAfterReads + output* simulate a station computing its result — an
	// anvil repair, a rename, a grindstone strip — again without sleeping.
	outputAfterReads int
	outputSlot       uint32
	outputName       string
	outputNetID      int32
	outputApplied    bool
	outputMaterials  []string

	// placeErr and takeErr let a test fail one specific transfer, which is how
	// the "every failure path returns false" rule gets exercised.
	placeErr map[uint32]error
	takeErr  map[uint32]error

	// placed and taken record the container every transfer addressed. The
	// container ID is what the server resolves a station slot against, and it is
	// invisible from the outside otherwise: a transfer that names the window ID
	// instead of the station's container is rejected server-side and still looks
	// like a clean success here, so the only way to pin it is to record what the
	// manager actually passed.
	placed []transferCall
	taken  []transferCall
}

// transferCall is one container transfer as the manager addressed it: which
// protocol container the slot was claimed to live in, and which slot of it.
type transferCall struct {
	containerID byte
	slot        uint32
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		coords:    mgl32.Vec3{4.5, 64, 4.5},
		blocks:    make(map[[3]int32]string),
		inv:       make(map[uint32]protocol.ItemStack),
		names:     make(map[int32]string),
		container: make(map[uint32]protocol.ItemInstance),
		windowID:  byte(7),
		placeErr:  make(map[uint32]error),
		takeErr:   make(map[uint32]error),
	}
}

// addItem registers an item name and puts a stack of it in the inventory.
func (f *fakeBot) addItem(slot uint32, netID int32, name string, count uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names[netID] = name
	f.inv[slot] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: netID}, Count: count}
}

// addBlock puts a named block in the world at a position.
func (f *fakeBot) addBlock(x, y, z int32, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocks[[3]int32{x, y, z}] = name
}

func (f *fakeBot) GetCoords() mgl32.Vec3 { return f.coords }

func (f *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name, ok := f.blocks[[3]int32{x, y, z}]
	return name, ok
}

func (f *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[uint32]protocol.ItemStack, len(f.inv))
	for k, v := range f.inv {
		out[k] = v
	}
	return out
}

func (f *fakeBot) GetItemNames() map[int32]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[int32]string, len(f.names))
	for k, v := range f.names {
		out[k] = v
	}
	return out
}

func (f *fakeBot) NavigateToBlock(int32, int32, int32, float32) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "navigate")
	return !f.navFails
}

func (f *fakeBot) StopMovement() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "stop")
}

func (f *fakeBot) LookAt(mgl32.Vec3) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "look")
}

func (f *fakeBot) ResetLook() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "reset_look")
	f.closedLook = true
}

func (f *fakeBot) BeginContainerWatch() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "watch")
	f.watched = true
}

func (f *fakeBot) ClickBlockAt(_ context.Context, _ protocol.BlockPos) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "click")
	f.clicked = true
	if f.clickFails {
		return false, f.clickReason
	}
	return true, ""
}

func (f *fakeBot) WaitContainerOpen(context.Context, time.Duration) (byte, protocol.BlockPos, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openFails {
		return 0, protocol.BlockPos{}, false
	}
	return f.windowID, protocol.BlockPos{4, 64, 4}, true
}

func (f *fakeBot) ContainerItems() map[uint32]protocol.ItemInstance {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.advanceBrew()
	f.advanceOutput()
	out := make(map[uint32]protocol.ItemInstance, len(f.container))
	for k, v := range f.container {
		out[k] = v
	}
	return out
}

// advanceBrew simulates the server working the stand: a cycle consumes the
// ingredient and rewrites the bottles. It only fires while the stand actually
// holds an ingredient, so a refill between cycles is what starts the next one.
func (f *fakeBot) advanceBrew() {
	if f.brewIndex >= len(f.brewSequence) {
		return
	}
	ingredient, held := f.container[station.BrewIngredientSlot]
	if !held || ingredient.Stack.Count == 0 {
		f.brewTicks = 0
		return
	}
	f.brewTicks++
	if f.brewTicks < f.brewAfterReads {
		return
	}
	f.brewTicks = 0
	delete(f.container, station.BrewIngredientSlot)

	result := f.brewSequence[f.brewIndex]
	f.brewIndex++
	for slot := uint32(0); slot < station.BrewBottleSlotCount; slot++ {
		netID := int32(200 + int(slot) + 100*f.brewIndex)
		f.names[netID] = result
		f.container[slot] = protocol.ItemInstance{
			StackNetworkID: netID,
			Stack: protocol.ItemStack{
				ItemType: protocol.ItemType{NetworkID: netID},
				Count:    1,
			},
		}
	}
}

// advanceOutput simulates a station computing its result once the manager has
// polled enough times: the output slot fills, and any refund the server owes
// lands in the bot's inventory.
func (f *fakeBot) advanceOutput() {
	if f.outputApplied || f.outputName == "" || f.reads < f.outputAfterReads {
		return
	}
	f.outputApplied = true
	f.names[f.outputNetID] = f.outputName
	f.container[f.outputSlot] = protocol.ItemInstance{
		StackNetworkID: f.outputNetID,
		Stack: protocol.ItemStack{
			ItemType: protocol.ItemType{NetworkID: f.outputNetID},
			Count:    1,
		},
	}
	for i, material := range f.outputMaterials {
		f.addItemLocked(uint32(70+i), int32(300+i), material, 1)
	}
}

func (f *fakeBot) ContainerItemName(item protocol.ItemInstance) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name, ok := f.names[item.Stack.NetworkID]; ok {
		return name
	}
	return "unknown"
}

func (f *fakeBot) CloseContainerWindow(windowID byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "close")
	f.closedWindow = windowID
}

func (f *fakeBot) PlaceIntoContainerSlotIn(containerID byte, containerSlot uint32, _ int32, srcSlot uint32, count int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placed = append(f.placed, transferCall{containerID: containerID, slot: containerSlot})
	if err, ok := f.placeErr[containerSlot]; ok {
		return err
	}
	stack, ok := f.inv[srcSlot]
	if !ok || stack.Count == 0 {
		return errors.New("no item in inventory slot")
	}
	if count <= 0 || count > int(stack.Count) {
		count = int(stack.Count)
	}
	moved := stack
	moved.Count = uint16(count)
	f.container[containerSlot] = protocol.ItemInstance{StackNetworkID: int32(100 + containerSlot), Stack: moved}

	left := int(stack.Count) - count
	if left == 0 {
		delete(f.inv, srcSlot)
	} else {
		stack.Count = uint16(left)
		f.inv[srcSlot] = stack
	}
	return nil
}

func (f *fakeBot) TakeFromContainerSlotIn(containerID byte, slot uint32, count int, _ int32, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taken = append(f.taken, transferCall{containerID: containerID, slot: slot})
	if err, ok := f.takeErr[slot]; ok {
		return err
	}
	item, ok := f.container[slot]
	if !ok || item.Stack.Count == 0 {
		return errors.New("container slot is empty")
	}
	if count <= 0 || count > int(item.Stack.Count) {
		count = int(item.Stack.Count)
	}
	taken := item
	taken.Stack.Count = uint16(count)
	delete(f.container, slot)

	if left := int(item.Stack.Count) - count; left > 0 {
		rest := item
		rest.Stack.Count = uint16(left)
		f.container[slot] = rest
	}

	for free := uint32(0); free < 36; free++ {
		existing, occupied := f.inv[free]
		if !occupied || existing.Count == 0 {
			f.inv[free] = protocol.ItemStack{ItemType: taken.Stack.ItemType, Count: taken.Stack.Count}
			return nil
		}
	}
	return errors.New("inventory full")
}

// addItemLocked is addItem without the lock, for the fake's own server-side
// updates that already hold it.
func (f *fakeBot) addItemLocked(slot uint32, netID int32, name string, count uint16) {
	f.names[netID] = name
	f.inv[slot] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: netID}, Count: count}
}

// addContainer puts a stack straight into an open window slot, standing in for
// a server-side update the manager did not cause.
func (f *fakeBot) addContainer(slot uint32, netID int32, name string, count uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names[netID] = name
	f.container[slot] = protocol.ItemInstance{
		StackNetworkID: netID,
		Stack: protocol.ItemStack{
			ItemType: protocol.ItemType{NetworkID: netID},
			Count:    count,
		},
	}
}

// inventoryName reports the resolved name and count in an inventory slot.
func (f *fakeBot) inventoryName(slot uint32) (string, uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stack, ok := f.inv[slot]
	if !ok {
		return "", 0
	}
	return f.names[stack.NetworkID], stack.Count
}

func (f *fakeBot) containerSlotName(slot uint32) (string, uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.container[slot]
	if !ok {
		return "", 0
	}
	return f.names[item.Stack.NetworkID], item.Stack.Count
}

// eventIndex reports the position of a recorded call, or -1.
func (f *fakeBot) eventIndex(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, e := range f.events {
		if e == name {
			return i
		}
	}
	return -1
}

// placedContainers lists, in order, the container of every slot the manager
// placed into. Duplicates are kept: a stand loads three bottle slots and the
// test should be able to say so.
func (f *fakeBot) placedContainers() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, 0, len(f.placed))
	for _, call := range f.placed {
		out = append(out, call.containerID)
	}
	return out
}

// placedSlots lists the slots the manager placed into, alongside the containers
// above, so a failure can be reported as a (container, slot) pair.
func (f *fakeBot) placedSlots() []transferCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]transferCall, len(f.placed))
	copy(out, f.placed)
	return out
}

// takenSlots lists the transfers the manager took out of a container.
func (f *fakeBot) takenSlots() []transferCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]transferCall, len(f.taken))
	copy(out, f.taken)
	return out
}

// errFakeNoItem is the canned failure the fake returns for a transfer the test
// wants rejected, so a test can drive the "the server refused" path.
var errFakeNoItem = errors.New("no item in inventory slot")

// compile-time proof the fake satisfies the seam the manager is built on.
var _ station.Bot = (*fakeBot)(nil)

// discardLogger keeps test output readable; the failure paths under test log a
// warning each, by design.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
