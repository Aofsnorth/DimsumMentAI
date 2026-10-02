package trading_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/inventory/trading"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// placeCall and takeCall record exactly what the manager asked the server to
// move. The assertions about container IDs are made against these, because the
// whole failure this package guards against is a transfer addressed to the
// wrong container that the server rejects in silence.
type placeCall struct {
	containerID byte
	slot        uint32
	destStack   int32
	srcSlot     uint32
	count       int
}

type takeCall struct {
	containerID byte
	slot        uint32
	count       int
	stackNetID  int32
	itemName    string
}

// errInjected is what the fake returns for a deliberately broken move, so a test
// can tell a refused transfer from a bot that never attempted one.
var errInjected = errors.New("injected transfer failure")

// fakeBot is a server that behaves the way a Bedrock host does for the parts a
// trade touches: it assigns a window ID, it only accepts a container ID it
// recognises, and it answers a take by putting the stack in the inventory.
type fakeBot struct {
	mu sync.Mutex

	coords   mgl32.Vec3
	entities map[uint64]*entity.Info

	invSlots  map[uint32]protocol.ItemStack
	itemNames map[int32]string
	heldSlot  uint32

	// container is the open window's contents, keyed by window slot.
	container map[uint32]protocol.ItemInstance

	calls     []string
	places    []placeCall
	takes     []takeCall
	written   []packet.Packet
	closed    []byte
	navigated []mgl32.Vec3
	looked    []mgl32.Vec3

	// server behaviour
	openWindowID byte
	openOK       bool
	polls        int

	// resultAfter is how many ContainerItems reads the server waits before it
	// materialises the trade result. Zero means it is there on the first read.
	resultAfter   int
	produceResult bool
	resultName    string
	resultCount   int
	resultStackID int32

	// failure injection
	placeErr     error
	takeErr      error
	takeSwallows bool // the take is accepted and the stack never lands

	// onTake fires when the server actually hands a result over, which is where
	// a host would also update the player's experience level.
	onTake func()
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		coords:        mgl32.Vec3{0, 0, 0},
		entities:      map[uint64]*entity.Info{},
		invSlots:      map[uint32]protocol.ItemStack{},
		itemNames:     map[int32]string{},
		container:     map[uint32]protocol.ItemInstance{},
		openWindowID:  42,
		openOK:        true,
		produceResult: true,
		resultName:    "minecraft:bread",
		resultCount:   8,
		resultStackID: 9001,
	}
}

// itemStack builds a protocol.ItemStack. NetworkID is promoted from the
// embedded ItemType and a promoted field cannot be named in a struct literal
// before Go 1.27, so the embed is named explicitly.
func itemStack(networkID int32, count uint16) protocol.ItemStack {
	return protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: networkID}, Count: count}
}

// putItem seeds an inventory slot and registers its name. The network ID is
// derived from the slot so a test never has to invent one.
func (f *fakeBot) putItem(slot uint32, name string, count uint16) {
	netID := int32(1000 + slot)
	f.itemNames[netID] = name
	f.invSlots[slot] = itemStack(netID, count)
}

// countItem is how many of an item the bot is carrying across every slot,
// matching on the exact normalised name.
func (f *fakeBot) countItem(name string) int {
	total := 0
	for _, stack := range f.invSlots {
		if stack.Count == 0 {
			continue
		}
		if trading.NormalizeItemName(f.itemNames[stack.NetworkID]) == trading.NormalizeItemName(name) {
			total += int(stack.Count)
		}
	}
	return total
}

// addVillager puts a villager next to the bot. The type is what villager
// detection reads, so tests vary it deliberately.
func (f *fakeBot) addVillager(id uint64, typeName string, pos mgl32.Vec3) {
	f.entities[id] = &entity.Info{ID: id, Type: typeName, Name: typeName, Position: pos, Health: 20}
}

// nextFreeSlot finds somewhere a taken stack can land.
func (f *fakeBot) nextFreeSlot() uint32 {
	for slot := uint32(0); slot < 36; slot++ {
		if stack, ok := f.invSlots[slot]; !ok || stack.Count == 0 {
			return slot
		}
	}
	return 0
}

// --- trading.Bot ---

func (f *fakeBot) GetCoords() mgl32.Vec3 {
	return f.coords
}

func (f *fakeBot) GetEntities() map[uint64]*entity.Info {
	out := make(map[uint64]*entity.Info, len(f.entities))
	for id, info := range f.entities {
		out[id] = info
	}
	return out
}

func (f *fakeBot) NavigateTo(pos mgl32.Vec3) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.navigated = append(f.navigated, pos)
	f.calls = append(f.calls, "NavigateTo")
}

func (f *fakeBot) StopMovement() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "StopMovement")
}

func (f *fakeBot) LookAt(pos mgl32.Vec3) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.looked = append(f.looked, pos)
	f.calls = append(f.calls, "LookAt")
}

func (f *fakeBot) ResetLook() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ResetLook")
}

func (f *fakeBot) GetEntityRuntimeID() uint64 { return 1 }

func (f *fakeBot) GetHeldItemSlot() uint32 { return f.heldSlot }

func (f *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack {
	out := make(map[uint32]protocol.ItemStack, len(f.invSlots))
	for slot, stack := range f.invSlots {
		out[slot] = stack
	}
	return out
}

func (f *fakeBot) GetItemNames() map[int32]string {
	out := make(map[int32]string, len(f.itemNames))
	for id, name := range f.itemNames {
		out[id] = name
	}
	return out
}

func (f *fakeBot) WritePacket(pk packet.Packet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = append(f.written, pk)
	f.calls = append(f.calls, "WritePacket")
	return nil
}

func (f *fakeBot) BeginContainerWatch() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "BeginContainerWatch")
}

func (f *fakeBot) WaitContainerOpen(ctx context.Context, timeout time.Duration) (byte, protocol.BlockPos, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "WaitContainerOpen")
	if !f.openOK {
		return 0, protocol.BlockPos{}, false
	}
	return f.openWindowID, protocol.BlockPos{0, 0, 0}, true
}

func (f *fakeBot) ContainerItems() map[uint32]protocol.ItemInstance {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	if f.produceResult && f.polls > f.resultAfter {
		if _, exists := f.container[trading.SlotResult]; !exists {
			netID := int32(5000)
			f.itemNames[netID] = f.resultName
			f.container[trading.SlotResult] = protocol.ItemInstance{
				Stack:          itemStack(netID, uint16(f.resultCount)),
				StackNetworkID: f.resultStackID,
			}
		}
	}
	out := make(map[uint32]protocol.ItemInstance, len(f.container))
	for slot, item := range f.container {
		out[slot] = item
	}
	return out
}

func (f *fakeBot) ContainerItemName(item protocol.ItemInstance) string {
	if name, ok := f.itemNames[item.Stack.NetworkID]; ok {
		return name
	}
	return "unknown"
}

func (f *fakeBot) CloseContainerWindow(windowID byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, windowID)
	f.calls = append(f.calls, "CloseContainerWindow")
}

func (f *fakeBot) PlaceIntoContainerSlotIn(containerID byte, containerSlot uint32, destStackNetID int32, srcSlot uint32, count int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.places = append(f.places, placeCall{
		containerID: containerID,
		slot:        containerSlot,
		destStack:   destStackNetID,
		srcSlot:     srcSlot,
		count:       count,
	})
	if f.placeErr != nil {
		return f.placeErr
	}

	stack, ok := f.invSlots[srcSlot]
	if !ok || stack.Count == 0 || count <= 0 {
		return errInjected
	}
	if int(stack.Count) < count {
		return errInjected
	}
	stack.Count -= uint16(count)
	if stack.Count == 0 {
		delete(f.invSlots, srcSlot)
	} else {
		f.invSlots[srcSlot] = stack
	}
	// The server accepts the move only for a container it recognises.
	if containerID != trading.IngredientOneContainerID && containerID != trading.IngredientTwoContainerID {
		return errInjected
	}
	f.container[containerSlot] = protocol.ItemInstance{
		Stack:          itemStack(stack.NetworkID, uint16(count)),
		StackNetworkID: 7000 + int32(containerSlot),
	}
	return nil
}

func (f *fakeBot) TakeFromContainerSlotIn(containerID byte, slot uint32, count int, stackNetID int32, itemName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.takes = append(f.takes, takeCall{
		containerID: containerID,
		slot:        slot,
		count:       count,
		stackNetID:  stackNetID,
		itemName:    itemName,
	})
	if f.takeErr != nil {
		return f.takeErr
	}
	if containerID != trading.ResultContainerID {
		return errInjected
	}
	if f.takeSwallows {
		// The server accepted the request and then went quiet: the item never
		// arrives. This is the case a "did it work?" check has to catch.
		return nil
	}
	if f.onTake != nil {
		f.onTake()
	}
	delete(f.container, slot)
	free := f.nextFreeSlot()
	f.invSlots[free] = itemStack(5000, uint16(count))
	return nil
}

// --- observation seams ---

// fakeTradeObserver stands in for the network layer that would record
// packet.UpdateTrade. Leaving a villager out of it is how a test says "the
// server said nothing about this one".
type fakeTradeObserver struct {
	windows map[uint64]trading.TradeWindow
}

func newFakeTradeObserver() *fakeTradeObserver {
	return &fakeTradeObserver{windows: map[uint64]trading.TradeWindow{}}
}

func (o *fakeTradeObserver) LastTradeWindow(villagerID uint64) (trading.TradeWindow, bool) {
	tw, ok := o.windows[villagerID]
	return tw, ok
}

// fakeXP is the experience seam. known=false is the honest "nothing observes
// the bot's level", which is different from level 0.
type fakeXP struct {
	level int32
	known bool
}

func (x *fakeXP) ExperienceLevel() (int32, bool) {
	return x.level, x.known
}

// discardLogger keeps the manager's warnings out of the test output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// indexOfCall finds where a named call sits in the recorded sequence.
func indexOfCall(calls []string, name string) int {
	for i, call := range calls {
		if call == name {
			return i
		}
	}
	return -1
}
