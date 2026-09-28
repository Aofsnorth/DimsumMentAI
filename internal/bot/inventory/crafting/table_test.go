package crafting

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/placement"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// fakeTableBot records what the workbench flow asked of the bot. The flow is
// about packet ordering — arm, click, confirm, close — so a fake that remembers
// the calls is the only way to pin it without a live server.
type fakeTableBot struct {
	table protocol.BlockPos

	clicks    []protocol.BlockPos
	clickOK   bool
	clickWhy  string
	armed     int
	openID    byte
	openReady bool
	openPos   protocol.BlockPos
	closed    []byte
}

func newFakeTableBot() *fakeTableBot {
	return &fakeTableBot{clickOK: true, openReady: true, openPos: protocol.BlockPos{1, 2, 3}, openID: 7}
}

func (f *fakeTableBot) GetCoords() mgl32.Vec3                                  { return mgl32.Vec3{0.5, 64, 0.5} }
func (f *fakeTableBot) WritePacket(pk packet.Packet) error                     { return nil }
func (f *fakeTableBot) GetEntities() map[uint64]*entity.Info                   { return nil }
func (f *fakeTableBot) NavigateTo(pos mgl32.Vec3)                              {}
func (f *fakeTableBot) NavigateToBlock(x, y, z int32, tol float32) bool        { return true }
func (f *fakeTableBot) StopMovement()                                          {}
func (f *fakeTableBot) LookAt(pos mgl32.Vec3)                                  {}
func (f *fakeTableBot) InjectAIEvent(msg string)                               {}
func (f *fakeTableBot) GetHeldItemSlot() uint32                                { return 0 }
func (f *fakeTableBot) GetInventorySlots() map[uint32]protocol.ItemStack       { return nil }
func (f *fakeTableBot) GetItemNames() map[int32]string                         { return nil }
func (f *fakeTableBot) EquipItem(slot uint32) error                            { return nil }
func (f *fakeTableBot) UnequipItem() error                                     { return nil }
func (f *fakeTableBot) SendChat(msg string)                                    {}
func (f *fakeTableBot) ReportActionStatus(u string, s event.ActionStatus)      {}
func (f *fakeTableBot) GetEntityRuntimeID() uint64                             { return 1 }
func (f *fakeTableBot) DropItem(name string, count int) error                  { return nil }
func (f *fakeTableBot) FindItemSlotByName(name string) (uint32, bool)          { return 0, false }
func (f *fakeTableBot) CraftItem(recipeNetID uint32, count int) error          { return nil }
func (f *fakeTableBot) GetRecipes() map[string]uint32                          { return nil }
func (f *fakeTableBot) GetBlockName(x, y, z int32) (string, bool)              { return "crafting_table", true }
func (f *fakeTableBot) GetLocalWorldModel() entity.WorldModel                  { return nil }
func (f *fakeTableBot) FindPlayer(u string) (uint64, mgl32.Vec3, bool)         { return 0, mgl32.Vec3{}, false }
func (f *fakeTableBot) SetLookAngles(yaw, pitch float32)                       {}
func (f *fakeTableBot) WaitForYawSync(targetYaw float32, d time.Duration) bool { return true }
func (f *fakeTableBot) AimAtPlayerForDrop(t string, p float32) (float32, bool) {
	return 0, false
}
func (f *fakeTableBot) OverrideLookPitch(pitch float32) {}
func (f *fakeTableBot) ResetLook()                      {}
func (f *fakeTableBot) PlaceBlock(ctx context.Context, r placement.Request) error {
	return nil
}

func (f *fakeTableBot) BeginContainerWatch() { f.armed++ }

func (f *fakeTableBot) ClickBlockAt(ctx context.Context, pos protocol.BlockPos) (bool, string) {
	f.clicks = append(f.clicks, pos)
	return f.clickOK, f.clickWhy
}

func (f *fakeTableBot) WaitContainerOpen(ctx context.Context, timeout time.Duration) (byte, protocol.BlockPos, bool) {
	if !f.openReady {
		return 0, protocol.BlockPos{}, false
	}
	return f.openID, f.openPos, true
}

func (f *fakeTableBot) CloseContainerWindow(windowID byte) { f.closed = append(f.closed, windowID) }

func newTestManager(b *fakeTableBot) *Manager {
	return NewManager(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestOpenCraftingTableWaitsForTheServerWindow is the regression for the craft
// that disconnected: ingredients were staged into the 3×3 input while the server
// still had the personal 2×2 grid open, because the open was assumed from a
// fixed sleep. The watch has to be armed before the click and the assigned
// window ID is the only proof the workbench is really open.
func TestOpenCraftingTableWaitsForTheServerWindow(t *testing.T) {
	t.Parallel()

	bot := newFakeTableBot()
	mgr := newTestManager(bot)
	table := protocol.BlockPos{10, 64, 10}

	if err := mgr.OpenCraftingTable(context.Background(), table); err != nil {
		t.Fatalf("OpenCraftingTable() error = %v", err)
	}
	if bot.armed != 1 {
		t.Errorf("watch armed %d times, want exactly once before the click", bot.armed)
	}
	if len(bot.clicks) != 1 || bot.clicks[0] != table {
		t.Fatalf("clicks = %v, want one click on the table", bot.clicks)
	}
	if mgr.windowID != bot.openID {
		t.Fatalf("tracked window %d, want the assigned %d", mgr.windowID, bot.openID)
	}
}

// TestOpenCraftingTableFailsWhenTheServerNeverOpens keeps the flow honest: a
// click that never becomes a window must not be reported as a usable workbench,
// because the next step would stage items into a grid the server is not showing.
func TestOpenCraftingTableFailsWhenTheServerNeverOpens(t *testing.T) {
	t.Parallel()

	bot := newFakeTableBot()
	bot.openReady = false
	mgr := newTestManager(bot)

	err := mgr.OpenCraftingTable(context.Background(), protocol.BlockPos{10, 64, 10})
	if err == nil {
		t.Fatal("a window the server never opened was accepted")
	}
	if mgr.windowID != 0 {
		t.Errorf("windowID = %d after a failed open, want 0", mgr.windowID)
	}
	mgr.CloseWindow()
	if len(bot.closed) != 0 {
		t.Errorf("closed %v after a failed open, want nothing closed", bot.closed)
	}
}

// TestOpenCraftingTableReportsAFailedClick covers the click itself: the
// interactor can refuse a target, and that refusal has to reach the caller
// instead of becoming a silent "the table is open".
func TestOpenCraftingTableReportsAFailedClick(t *testing.T) {
	t.Parallel()

	bot := newFakeTableBot()
	bot.clickOK = false
	bot.clickWhy = "crafting_table tidak merespons klik"
	mgr := newTestManager(bot)

	err := mgr.OpenCraftingTable(context.Background(), protocol.BlockPos{10, 64, 10})
	if err == nil {
		t.Fatal("a refused click was accepted")
	}
}

// TestCloseWindowUsesTheAssignedID is the second half of the same bug: the
// window was closed with a hard-coded ID the server never issued, so the
// workbench stayed open server-side and the next personal craft went into the
// wrong grid.
func TestCloseWindowUsesTheAssignedID(t *testing.T) {
	t.Parallel()

	bot := newFakeTableBot()
	bot.openID = 42
	mgr := newTestManager(bot)

	if err := mgr.OpenCraftingTable(context.Background(), protocol.BlockPos{1, 64, 1}); err != nil {
		t.Fatal(err)
	}
	mgr.CloseWindow()

	if len(bot.closed) != 1 || bot.closed[0] != 42 {
		t.Fatalf("closed = %v, want the assigned window 42 exactly once", bot.closed)
	}
	if mgr.windowID != 0 {
		t.Errorf("windowID = %d after close, want cleared", mgr.windowID)
	}
	// Closing twice must not re-close a window that is already gone.
	mgr.CloseWindow()
	if len(bot.closed) != 1 {
		t.Fatalf("closed = %v after a second close, want still one close", bot.closed)
	}
}
