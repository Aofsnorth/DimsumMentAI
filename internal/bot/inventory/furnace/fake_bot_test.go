package furnace

import (
	"context"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// fakeBot is a scripted stand-in for the live bot. It records every window
// operation so a test can assert the furnace used the window the server
// assigned, and it can be told whether the window ever opened and whether the
// smelt ever produced output.
type fakeBot struct {
	pos    mgl32.Vec3
	blocks map[[3]int32]string
	names  map[int32]string
	items  map[uint32]protocol.ItemStack

	// windowID is the ID the server assigns on ContainerOpen. It is deliberately
	// non-zero in tests so a stray hardcoded window 0 cannot pass by accident.
	windowID byte
	// openFails simulates a click the server never answers.
	openFails bool
	// smeltResult is what ends up in the output slot. nil means the furnace
	// never produces anything.
	smeltResult *protocol.ItemInstance

	armedWatch bool
	clicked     []protocol.BlockPos
	placed      []placedStack
	taken       []takenStack
	closed      []byte
}

type placedStack struct {
	windowID     byte
	slot         uint32
	count        int
	destStackNet int32
	srcSlot      uint32
}

type takenStack struct {
	windowID   byte
	slot       uint32
	count      int
	stackNetID int32
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		blocks:   map[[3]int32]string{},
		names:    map[int32]string{},
		items:    map[uint32]protocol.ItemStack{},
		windowID: 1,
	}
}

func (f *fakeBot) GetCoords() mgl32.Vec3 { return f.pos }

func (f *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	name, ok := f.blocks[[3]int32{x, y, z}]
	return name, ok
}

func (f *fakeBot) NavigateToBlock(x, y, z int32, tolerance float32) bool { return true }
func (f *fakeBot) StopMovement()                                        {}
func (f *fakeBot) LookAt(pos mgl32.Vec3)                                 {}
func (f *fakeBot) ResetLook()                                           {}

func (f *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack { return f.items }
func (f *fakeBot) GetItemNames() map[int32]string                  { return f.names }

func (f *fakeBot) BeginContainerWatch() { f.armedWatch = true }

func (f *fakeBot) ClickBlockAt(ctx context.Context, pos protocol.BlockPos) (bool, string) {
	f.clicked = append(f.clicked, pos)
	return true, ""
}

func (f *fakeBot) WaitContainerOpen(ctx context.Context, timeout time.Duration) (byte, protocol.BlockPos, bool) {
	if f.openFails {
		return 0, protocol.BlockPos{}, false
	}
	return f.windowID, protocol.BlockPos{}, true
}

// ContainerItems reports the current window contents. Only the output slot is
// ever populated, which is the one thing the smelt wait reads.
func (f *fakeBot) ContainerItems() map[uint32]protocol.ItemInstance {
	if f.smeltResult == nil {
		return map[uint32]protocol.ItemInstance{}
	}
	return map[uint32]protocol.ItemInstance{SlotOutput: *f.smeltResult}
}

func (f *fakeBot) ContainerItemName(item protocol.ItemInstance) string {
	return f.names[item.Stack.NetworkID]
}

func (f *fakeBot) CloseContainerWindow(windowID byte) {
	f.closed = append(f.closed, windowID)
}

func (f *fakeBot) PlaceIntoContainerSlot(windowID byte, containerSlot uint32, destStackNetID int32, srcSlot uint32, count int) error {
	f.placed = append(f.placed, placedStack{
		windowID:     windowID,
		slot:         containerSlot,
		count:        count,
		destStackNet: destStackNetID,
		srcSlot:      srcSlot,
	})
	return nil
}

func (f *fakeBot) TakeFromContainerSlot(windowID byte, slot uint32, count int, stackNetID int32, itemName string) error {
	f.taken = append(f.taken, takenStack{
		windowID:   windowID,
		slot:       slot,
		count:      count,
		stackNetID: stackNetID,
	})
	return nil
}
