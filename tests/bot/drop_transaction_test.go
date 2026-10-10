package bot_test

import (
	"testing"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestBuildDropStackActionUsesAuthoritativeStackNetworkID(t *testing.T) {
	t.Parallel()

	item := dropTestItem(8, 73)
	action, dropped, err := bot.BuildDropStackAction(4, item, 3)
	if err != nil {
		t.Fatalf("buildDropStackAction() error = %v", err)
	}
	if dropped != 3 {
		t.Fatalf("dropped count = %d, want 3", dropped)
	}
	if action.Count != 3 {
		t.Fatalf("action count = %d, want 3", action.Count)
	}
	if action.Source.StackNetworkID != 73 {
		t.Fatalf("source stack network ID = %d, want 73", action.Source.StackNetworkID)
	}
	if action.Source.Slot != 4 {
		t.Fatalf("source slot = %d, want 4", action.Source.Slot)
	}
	if action.Randomly {
		t.Fatal("drop action should not be marked Randomly")
	}
}

func TestBuildDropStackActionDropsWholeStackForNonPositiveCount(t *testing.T) {
	t.Parallel()

	action, dropped, err := bot.BuildDropStackAction(4, dropTestItem(8, 73), 0)
	if err != nil {
		t.Fatalf("buildDropStackAction() error = %v", err)
	}
	if dropped != 8 {
		t.Fatalf("dropped count = %d, want 8", dropped)
	}
	if action.Count != 8 {
		t.Fatalf("action count = %d, want 8", action.Count)
	}
}

func TestBuildDropStackActionRejectsMissingAuthoritativeStackNetworkID(t *testing.T) {
	t.Parallel()

	if _, _, err := bot.BuildDropStackAction(4, dropTestItem(8, 0), 3); err == nil {
		t.Fatal("buildDropStackAction() accepted a missing stack network ID")
	}
}

func TestBuildDropStackActionRejectsEmptyStack(t *testing.T) {
	t.Parallel()

	if _, _, err := bot.BuildDropStackAction(4, dropTestItem(0, 73), 3); err == nil {
		t.Fatal("buildDropStackAction() accepted an empty stack")
	}
}

func TestBuildDropSwingUsesDropItemSource(t *testing.T) {
	t.Parallel()

	swing := bot.BuildDropSwing(99)
	if swing.ActionType != packet.AnimateActionSwingArm || swing.EntityRuntimeID != 99 || swing.SwingSource != packet.AnimateSwingSourceDropItem {
		t.Fatalf("drop swing = %+v", swing)
	}
}

func dropTestItem(count uint16, stackNetworkID int32) protocol.ItemInstance {
	return protocol.ItemInstance{
		StackNetworkID: stackNetworkID,
		Stack: protocol.ItemStack{
			ItemType:       protocol.ItemType{NetworkID: 17, MetadataValue: 2},
			BlockRuntimeID: 101,
			Count:          count,
			NBTData:        map[string]any{"test": int32(1)},
			CanBePlacedOn:  []string{"minecraft:stone"},
			CanBreak:       []string{"minecraft:dirt"},
		},
	}
}

func TestBuildDropCursorActionTargetsTheCursor(t *testing.T) {
	t.Parallel()

	action, err := bot.BuildDropCursorAction(41, 3)
	if err != nil {
		t.Fatalf("BuildDropCursorAction() error = %v", err)
	}
	if action.Count != 3 {
		t.Fatalf("count = %d, want 3", action.Count)
	}
	if action.Source.Container.ContainerID != protocol.ContainerCursor || action.Source.Slot != 0 {
		t.Fatalf("source = %+v, want the cursor slot 0", action.Source)
	}
	if action.Source.StackNetworkID != 41 {
		t.Fatalf("source stack network ID = %d, want 41", action.Source.StackNetworkID)
	}
}

func TestBuildDropCursorActionRejectsBadInput(t *testing.T) {
	t.Parallel()

	if _, err := bot.BuildDropCursorAction(0, 1); err == nil {
		t.Fatal("a zero cursor stack ID must be refused")
	}
	if _, err := bot.BuildDropCursorAction(5, 0); err == nil {
		t.Fatal("a zero count must be refused")
	}
}

func TestBuildDropTransactionIsTheNormalTwoActionShape(t *testing.T) {
	t.Parallel()

	stack := dropTestItem(8, 73).Stack
	tx := bot.BuildDropTransaction(4, 73, stack, 3)
	if _, ok := tx.TransactionData.(*protocol.NormalTransactionData); !ok {
		t.Fatalf("transaction data = %T, want *protocol.NormalTransactionData", tx.TransactionData)
	}
	if len(tx.Actions) != 2 {
		t.Fatalf("action count = %d, want 2 (container hand-off + world drop)", len(tx.Actions))
	}
	container, world := tx.Actions[0], tx.Actions[1]
	if container.SourceType != protocol.InventoryActionSourceContainer || container.InventorySlot != 4 {
		t.Fatalf("container action = %+v, want source container slot 4", container)
	}
	if container.OldItem.Stack.Count != 8 || container.NewItem.Stack.Count != 5 {
		t.Fatalf("container counts = %d -> %d, want 8 -> 5", container.OldItem.Stack.Count, container.NewItem.Stack.Count)
	}
	if container.OldItem.StackNetworkID != 73 || container.NewItem.StackNetworkID != 73 {
		t.Fatalf("container stack IDs = %d/%d, want 73/73", container.OldItem.StackNetworkID, container.NewItem.StackNetworkID)
	}
	if world.SourceType != protocol.InventoryActionSourceWorld {
		t.Fatalf("world action source = %d, want InventoryActionSourceWorld", world.SourceType)
	}
	if flags, ok := world.SourceFlags.Value(); !ok || flags != 1 {
		t.Fatalf("world action flags = %+v, want DROP_ITEM (1) set", world.SourceFlags)
	}
	if world.NewItem.Stack.Count != 3 {
		t.Fatalf("world item count = %d, want 3", world.NewItem.Stack.Count)
	}
}

func TestBuildDropTransactionPartialDropIsOneItemPerPacket(t *testing.T) {
	t.Parallel()

	// Geyser reads the world action's count as drop-one (1) vs drop-the-stack
	// (>1), so a partial request of 3 out of 64 must travel as three packets
	// with a world count of exactly 1 each.
	stack := dropTestItem(64, 90).Stack
	tx := bot.BuildDropTransaction(2, 90, stack, 1)
	world := tx.Actions[1]
	if world.NewItem.Stack.Count != 1 {
		t.Fatalf("world item count = %d, want 1 for a partial drop", world.NewItem.Stack.Count)
	}
	container := tx.Actions[0]
	if container.OldItem.Stack.Count != 64 || container.NewItem.Stack.Count != 63 {
		t.Fatalf("container counts = %d -> %d, want 64 -> 63", container.OldItem.Stack.Count, container.NewItem.Stack.Count)
	}
}

func TestBuildDropTransactionWholeStackLeavesAnEmptySlot(t *testing.T) {
	t.Parallel()

	stack := dropTestItem(8, 73).Stack
	tx := bot.BuildDropTransaction(0, 73, stack, 8)
	container, world := tx.Actions[0], tx.Actions[1]
	if world.NewItem.Stack.Count != 8 {
		t.Fatalf("world item count = %d, want the whole stack (8)", world.NewItem.Stack.Count)
	}
	if container.NewItem.StackNetworkID != 0 || container.NewItem.Stack.Count != 0 {
		t.Fatalf("remaining item = %+v, want an empty instance", container.NewItem)
	}
	if container.OldItem.Stack.Count != 8 {
		t.Fatalf("old item count = %d, want 8", container.OldItem.Stack.Count)
	}
}

// The count rule both drop paths share: a non-positive request drops the whole
// stack, a request larger than the stack drops everything it can.
func TestClampDropCount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		stackCount uint16
		requested  int
		want       uint16
	}{
		{"partial request", 64, 3, 3},
		{"request equals stack", 64, 64, 64},
		{"request over stack clamps", 5, 20, 5},
		{"non-positive drops whole stack", 5, 0, 5},
		{"negative drops whole stack", 8, -3, 8},
	}
	for _, tc := range cases {
		if got := bot.ClampDropCount(tc.stackCount, tc.requested); got != tc.want {
			t.Errorf("%s: clampDropCount(%d, %d) = %d, want %d", tc.name, tc.stackCount, tc.requested, got, tc.want)
		}
	}
}
