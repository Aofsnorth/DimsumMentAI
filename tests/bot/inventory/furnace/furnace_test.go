package furnace_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot/inventory/furnace"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// quietLogger keeps test output free of the manager's INFO smelt log.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestIsFurnaceBlock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		want  bool
		block string
	}{
		{name: "plain furnace", block: "furnace", want: true},
		{name: "namespaced furnace", block: "minecraft:furnace", want: true},
		{name: "blast furnace", block: "blast_furnace", want: true},
		{name: "namespaced blast furnace", block: "minecraft:blast_furnace", want: true},
		{name: "smoker", block: "smoker", want: true},
		{name: "namespaced smoker", block: "minecraft:smoker", want: true},
		{name: "lit furnace is still a furnace", block: "lit_furnace", want: true},
		{name: "mixed case", block: "MineCraft:Furnace", want: true},

		// The regression this guards: the old finder returned the first SOLID
		// block, so any wall or stone next to the bot looked like a furnace.
		{name: "stone is not a furnace", block: "stone", want: false},
		{name: "chest is not a furnace", block: "chest", want: false},
		{name: "crafting table is not a furnace", block: "crafting_table", want: false},
		{name: "furnace minecart is not a furnace", block: "furnace_minecart", want: false},
		{name: "empty", block: "", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := furnace.IsFurnaceBlock(tc.block); got != tc.want {
				t.Errorf("IsFurnaceBlock(%q) = %v, want %v", tc.block, got, tc.want)
			}
		})
	}
}

func TestFindNearbyFurnaceSkipsNonFurnaceBlocks(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.pos = mgl32.Vec3{0.5, 64, 0.5}
	// A wall of solid stone right next to the bot, and the real furnace two
	// blocks further. The old implementation returned the stone.
	bot.blocks[[3]int32{1, 64, 0}] = "stone"
	bot.blocks[[3]int32{0, 64, 1}] = "dirt"
	bot.blocks[[3]int32{2, 64, 0}] = "minecraft:furnace"

	fm := furnace.NewManager(bot, quietLogger())

	pos, ok := fm.FindNearbyFurnace()
	if !ok {
		t.Fatal("findNearbyFurnace() found nothing, want the furnace at 2,64,0")
	}
	want := protocol.BlockPos{2, 64, 0}
	if pos != want {
		t.Errorf("findNearbyFurnace() = %v, want %v", pos, want)
	}
}

func TestFindNearbyFurnaceNoneNearby(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.pos = mgl32.Vec3{0.5, 64, 0.5}
	bot.blocks[[3]int32{1, 64, 0}] = "stone"
	bot.blocks[[3]int32{0, 64, 1}] = "dirt"

	fm := furnace.NewManager(bot, quietLogger())

	if _, ok := fm.FindNearbyFurnace(); ok {
		t.Error("findNearbyFurnace() found a furnace in a world with no furnace block")
	}
}

func TestFurnaceSlotLayout(t *testing.T) {
	t.Parallel()

	// Bedrock numbers a furnace window as 0=input, 1=fuel, 2=output. Placing
	// an item into the wrong one of those silently does nothing, so the layout
	// is asserted rather than written inline at each call site.
	if furnace.SlotInput != 0 {
		t.Errorf("SlotInput = %d, want 0", furnace.SlotInput)
	}
	if furnace.SlotFuel != 1 {
		t.Errorf("SlotFuel = %d, want 1", furnace.SlotFuel)
	}
	if furnace.SlotOutput != 2 {
		t.Errorf("SlotOutput = %d, want 2", furnace.SlotOutput)
	}
}

func TestSmeltItemUsesRealWindowAndConfirmsOutput(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.pos = mgl32.Vec3{0.5, 64, 0.5}
	bot.blocks[[3]int32{1, 64, 0}] = "minecraft:furnace"
	// The server assigns 7, not 0. Everything must use the assigned ID.
	bot.windowID = 7
	bot.names = map[int32]string{
		1: "iron_ore",
		2: "coal",
		3: "iron_ingot",
	}
	bot.items = map[uint32]protocol.ItemStack{
		0: {ItemType: protocol.ItemType{NetworkID: 1}, Count: 3},
		1: {ItemType: protocol.ItemType{NetworkID: 2}, Count: 8},
	}
	bot.smeltResult = &protocol.ItemInstance{
		StackNetworkID: 99,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 3}, Count: 3},
	}

	fm := furnace.NewManager(bot, quietLogger())

	if !fm.SmeltItem(context.Background(), "iron_ore") {
		t.Fatal("SmeltItem() = false, want true")
	}

	if !bot.armedWatch {
		t.Error("container watch was not armed before the click")
	}
	if len(bot.clicked) != 1 {
		t.Fatalf("clicked %d blocks, want 1", len(bot.clicked))
	}

	// Input goes to slot 0, fuel to slot 1, both under the assigned window.
	if len(bot.placed) != 2 {
		t.Fatalf("placed %d stacks, want 2 (input + fuel)", len(bot.placed))
	}
	if got := bot.placed[0]; got.slot != furnace.SlotInput || got.windowID != 7 {
		t.Errorf("input placed at slot %d window %d, want slot %d window 7", got.slot, got.windowID, furnace.SlotInput)
	}
	if got := bot.placed[1]; got.slot != furnace.SlotFuel || got.windowID != 7 {
		t.Errorf("fuel placed at slot %d window %d, want slot %d window 7", got.slot, got.windowID, furnace.SlotFuel)
	}

	// The result is taken out of the output slot, again under the assigned window.
	if len(bot.taken) != 1 {
		t.Fatalf("took %d stacks, want 1", len(bot.taken))
	}
	if got := bot.taken[0]; got.slot != furnace.SlotOutput || got.windowID != 7 {
		t.Errorf("output taken from slot %d window %d, want slot %d window 7", got.slot, got.windowID, furnace.SlotOutput)
	}

	// And the window is closed by the ID the server assigned.
	if len(bot.closed) != 1 {
		t.Fatalf("closed %d windows, want 1", len(bot.closed))
	}
	if bot.closed[0] != 7 {
		t.Errorf("closed window %d, want the assigned window 7", bot.closed[0])
	}
}

func TestSmeltItemReportsFailureWhenOutputNeverArrives(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.pos = mgl32.Vec3{0.5, 64, 0.5}
	bot.blocks[[3]int32{1, 64, 0}] = "minecraft:furnace"
	bot.windowID = 4
	bot.names = map[int32]string{1: "iron_ore", 2: "coal"}
	bot.items = map[uint32]protocol.ItemStack{
		0: {ItemType: protocol.ItemType{NetworkID: 1}, Count: 1},
		1: {ItemType: protocol.ItemType{NetworkID: 2}, Count: 4},
	}
	// smeltResult stays nil: the furnace never produces anything.

	fm := furnace.NewManager(bot, quietLogger())
	fm.SetSmeltBudget(50 * time.Millisecond)

	if fm.SmeltItem(context.Background(), "iron_ore") {
		t.Error("SmeltItem() = true although the output never appeared")
	}
	if len(bot.taken) != 0 {
		t.Errorf("took %d stacks although nothing was confirmed", len(bot.taken))
	}
	// The window still has to be closed, by the assigned ID.
	if len(bot.closed) != 1 || bot.closed[0] != 4 {
		t.Errorf("closed = %v, want [4]", bot.closed)
	}
}

func TestSmeltItemReportsFailureWhenFurnaceNeverOpens(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.pos = mgl32.Vec3{0.5, 64, 0.5}
	bot.blocks[[3]int32{1, 64, 0}] = "minecraft:furnace"
	bot.windowID = 3
	bot.openFails = true

	fm := furnace.NewManager(bot, quietLogger())

	if fm.SmeltItem(context.Background(), "iron_ore") {
		t.Error("SmeltItem() = true although the server never opened a window")
	}
	if len(bot.placed) != 0 {
		t.Errorf("placed %d stacks into a window that was never open", len(bot.placed))
	}
}
