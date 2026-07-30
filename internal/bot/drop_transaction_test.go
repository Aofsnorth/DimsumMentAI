package bot

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestBuildDropPacketsUsesAuthoritativeStackNetworkID(t *testing.T) {
	t.Parallel()

	item := dropTestItem(8, 73)
	transaction, swing, dropped, err := buildDropPackets(99, 4, item, 3)
	if err != nil {
		t.Fatalf("buildDropPackets() error = %v", err)
	}
	if dropped != 3 {
		t.Fatalf("dropped count = %d, want 3", dropped)
	}
	if len(transaction.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(transaction.Actions))
	}
	if _, ok := transaction.TransactionData.(*protocol.NormalTransactionData); !ok {
		t.Fatalf("transaction data = %T, want *protocol.NormalTransactionData", transaction.TransactionData)
	}

	inventory := transaction.Actions[0]
	if inventory.SourceType != protocol.InventoryActionSourceContainer || inventory.WindowID != protocol.WindowIDInventory || inventory.InventorySlot != 4 {
		t.Fatalf("inventory action target = %+v", inventory)
	}
	if inventory.OldItem.StackNetworkID != 73 || inventory.OldItem.Stack.Count != 8 {
		t.Fatalf("old item = %+v, want count 8 and stack ID 73", inventory.OldItem)
	}
	if inventory.NewItem.StackNetworkID != 73 || inventory.NewItem.Stack.Count != 5 {
		t.Fatalf("new item = %+v, want count 5 and stack ID 73", inventory.NewItem)
	}

	world := transaction.Actions[1]
	if world.SourceType != protocol.InventoryActionSourceWorld || world.SourceFlags != 1 {
		t.Fatalf("world action source = %+v", world)
	}
	if world.NewItem.StackNetworkID != 73 || world.NewItem.Stack.Count != 3 {
		t.Fatalf("dropped item = %+v, want count 3 and stack ID 73", world.NewItem)
	}
	if swing.ActionType != packet.AnimateActionSwingArm || swing.EntityRuntimeID != 99 || swing.SwingSource != packet.AnimateSwingSourceDropItem {
		t.Fatalf("drop swing = %+v", swing)
	}
}

func TestBuildDropPacketsClearsRemainderOnlyForFullDrop(t *testing.T) {
	t.Parallel()

	transaction, _, dropped, err := buildDropPackets(99, 4, dropTestItem(8, 73), 0)
	if err != nil {
		t.Fatalf("buildDropPackets() error = %v", err)
	}
	if dropped != 8 {
		t.Fatalf("dropped count = %d, want 8", dropped)
	}
	remaining := transaction.Actions[0].NewItem
	if remaining.Stack.Count != 0 || remaining.Stack.NetworkID != 0 || remaining.StackNetworkID != 0 {
		t.Fatalf("full drop remainder = %+v, want empty item", remaining)
	}
	if droppedItem := transaction.Actions[1].NewItem; droppedItem.StackNetworkID != 73 || droppedItem.Stack.Count != 8 {
		t.Fatalf("dropped item = %+v, want count 8 and stack ID 73", droppedItem)
	}
}

func TestBuildDropPacketsRejectsMissingAuthoritativeStackNetworkID(t *testing.T) {
	t.Parallel()

	if _, _, _, err := buildDropPackets(99, 4, dropTestItem(8, 0), 3); err == nil {
		t.Fatal("buildDropPackets() accepted a missing stack network ID")
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
			HasNetworkID:   true,
		},
	}
}
