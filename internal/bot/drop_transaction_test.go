package bot

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestBuildDropStackActionUsesAuthoritativeStackNetworkID(t *testing.T) {
	t.Parallel()

	item := dropTestItem(8, 73)
	action, dropped, err := buildDropStackAction(4, item, 3)
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

	action, dropped, err := buildDropStackAction(4, dropTestItem(8, 73), 0)
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

	if _, _, err := buildDropStackAction(4, dropTestItem(8, 0), 3); err == nil {
		t.Fatal("buildDropStackAction() accepted a missing stack network ID")
	}
}

func TestBuildDropStackActionRejectsEmptyStack(t *testing.T) {
	t.Parallel()

	if _, _, err := buildDropStackAction(4, dropTestItem(0, 73), 3); err == nil {
		t.Fatal("buildDropStackAction() accepted an empty stack")
	}
}

func TestBuildDropSwingUsesDropItemSource(t *testing.T) {
	t.Parallel()

	swing := buildDropSwing(99)
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
			HasNetworkID:   true,
		},
	}
}
