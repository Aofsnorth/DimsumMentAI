package player_test

import (
	"log/slog"
	"os"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/network/player"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestContainerSlotOffset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		containerID byte
		want        uint32
	}{
		{protocol.ContainerHotBar, 0},
		{protocol.ContainerInventory, 9},
		{protocol.ContainerArmor, 36},
		{protocol.ContainerOffhand, 40},
		{protocol.ContainerCombinedHotBarAndInventory, 0},
	}
	for _, tt := range tests {
		got := player.ContainerSlotOffset(tt.containerID)
		if got != tt.want {
			t.Errorf("containerSlotOffset(0x%02x) = %d, want %d", tt.containerID, got, tt.want)
		}
	}
}

func TestStackResponseSlotOffset(t *testing.T) {
	t.Parallel()
	if got := player.StackResponseSlotOffset(protocol.ContainerInventory); got != 0 {
		t.Fatalf("ContainerInventory stack response offset = %d, want 0", got)
	}
	if got := player.StackResponseSlotOffset(protocol.ContainerArmor); got != 36 {
		t.Fatalf("ContainerArmor stack response offset = %d, want 36", got)
	}
}

func TestCoveredSlotSet_HotBar(t *testing.T) {
	t.Parallel()
	slots := player.CoveredSlotSet(protocol.ContainerHotBar)
	if len(slots) != 9 {
		t.Fatalf("expected 9 hotbar slots, got %d", len(slots))
	}
	for i := uint32(0); i < 9; i++ {
		if _, ok := slots[i]; !ok {
			t.Errorf("expected slot %d in hotbar set", i)
		}
	}
}

func TestCoveredSlotSet_Inventory(t *testing.T) {
	t.Parallel()
	slots := player.CoveredSlotSet(protocol.ContainerInventory)
	if len(slots) != 27 {
		t.Fatalf("expected 27 main inventory slots, got %d", len(slots))
	}
	for i := uint32(9); i < 36; i++ {
		if _, ok := slots[i]; !ok {
			t.Errorf("expected slot %d in inventory set", i)
		}
	}
	// Hotbar slots should NOT be in the inventory set.
	for i := uint32(0); i < 9; i++ {
		if _, ok := slots[i]; ok {
			t.Errorf("slot %d should NOT be in inventory set", i)
		}
	}
}

func TestCoveredSlotSet_Armor(t *testing.T) {
	t.Parallel()
	slots := player.CoveredSlotSet(protocol.ContainerArmor)
	if len(slots) != 4 {
		t.Fatalf("expected 4 armor slots, got %d", len(slots))
	}
	for i := uint32(36); i < 40; i++ {
		if _, ok := slots[i]; !ok {
			t.Errorf("expected slot %d in armor set", i)
		}
	}
}

func TestCoveredSlotSet_Offhand(t *testing.T) {
	t.Parallel()
	slots := player.CoveredSlotSet(protocol.ContainerOffhand)
	if len(slots) != 1 {
		t.Fatalf("expected 1 offhand slot, got %d", len(slots))
	}
	if _, ok := slots[40]; !ok {
		t.Error("expected slot 40 in offhand set")
	}
}

func TestCoveredSlotSet_Combined(t *testing.T) {
	t.Parallel()
	slots := player.CoveredSlotSet(protocol.ContainerCombinedHotBarAndInventory)
	if len(slots) != 36 {
		t.Fatalf("expected 36 combined slots, got %d", len(slots))
	}
}

func TestCoveredSlotSet_Unknown(t *testing.T) {
	t.Parallel()
	slots := player.CoveredSlotSet(0xFF)
	if slots != nil {
		t.Errorf("expected nil for unknown container, got %v", slots)
	}
}

func TestSlotRange(t *testing.T) {
	t.Parallel()
	r := player.SlotRange(5, 8)
	if len(r) != 3 {
		t.Fatalf("expected 3 slots, got %d", len(r))
	}
	for _, want := range []uint32{5, 6, 7} {
		if _, ok := r[want]; !ok {
			t.Errorf("expected slot %d in range", want)
		}
	}
}

func TestIsPlayerInventoryContainer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id   byte
		want bool
	}{
		{protocol.ContainerHotBar, true},
		{protocol.ContainerInventory, true},
		{protocol.ContainerCombinedHotBarAndInventory, true},
		{protocol.ContainerOffhand, true},
		{protocol.ContainerArmor, true},
		{protocol.ContainerAnvilInput, false},
		{protocol.ContainerFurnaceFuel, false},
		{0xFF, false},
	}
	for _, tt := range tests {
		got := player.IsPlayerInventoryContainer(tt.id)
		if got != tt.want {
			t.Errorf("isPlayerInventoryContainer(0x%02x) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestTransactionSlotToGlobal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		action protocol.InventoryAction
		want   uint32
		ok     bool
	}{
		{protocol.InventoryAction{WindowID: protocol.Option(int8(protocol.WindowIDInventory)), InventorySlot: 0}, 0, true},
		{protocol.InventoryAction{WindowID: protocol.Option(int8(protocol.WindowIDInventory)), InventorySlot: 35}, 35, true},
		{protocol.InventoryAction{WindowID: protocol.Option(int8(protocol.WindowIDInventory)), InventorySlot: 36}, 0, false},
		{protocol.InventoryAction{WindowID: protocol.Option(int8(protocol.WindowIDArmour)), InventorySlot: 0}, 36, true},
		{protocol.InventoryAction{WindowID: protocol.Option(int8(protocol.WindowIDArmour)), InventorySlot: 3}, 39, true},
		{protocol.InventoryAction{WindowID: protocol.Option(int8(protocol.WindowIDArmour)), InventorySlot: 4}, 0, false},
		{protocol.InventoryAction{WindowID: protocol.Option(int8(protocol.WindowIDOffHand)), InventorySlot: 0}, 40, true},
		{protocol.InventoryAction{WindowID: protocol.Option(int8(protocol.WindowIDOffHand)), InventorySlot: 1}, 0, false},
		{protocol.InventoryAction{WindowID: protocol.Option(int8(123)), InventorySlot: 0}, 0, false},
		{protocol.InventoryAction{InventorySlot: 0}, 0, false},
	}
	for _, tt := range tests {
		got, ok := player.TransactionSlotToGlobal(tt.action)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("transactionSlotToGlobal(%+v) = (%d, %v), want (%d, %v)", tt.action, got, ok, tt.want, tt.ok)
		}
	}
}

func TestIsPlayerInventoryTransaction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		action protocol.InventoryAction
		want   bool
	}{
		{protocol.InventoryAction{SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.Option(int8(protocol.WindowIDInventory))}, true},
		{protocol.InventoryAction{SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.Option(int8(protocol.WindowIDArmour))}, true},
		{protocol.InventoryAction{SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.Option(int8(protocol.WindowIDOffHand))}, true},
		{protocol.InventoryAction{SourceType: protocol.InventoryActionSourceWorld, WindowID: protocol.Option(int8(protocol.WindowIDInventory))}, false},
		{protocol.InventoryAction{SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.Option(int8(123))}, false},
	}
	for _, tt := range tests {
		got := player.IsPlayerInventoryTransaction(tt.action)
		if got != tt.want {
			t.Errorf("isPlayerInventoryTransaction(%+v) = %v, want %v", tt.action, got, tt.want)
		}
	}
}

func newTestBot() *bot.Bot {
	return &bot.Bot{
		InventoryMap:    make(map[uint32]protocol.ItemStack),
		StackNetworkIDs: make(map[uint32]int32),
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
}

func testItemInstance(networkID int32, count uint16, stackNetworkID int32) protocol.ItemInstance {
	return protocol.ItemInstance{
		StackNetworkID: stackNetworkID,
		Stack: protocol.ItemStack{
			ItemType: protocol.ItemType{NetworkID: networkID},
			Count:    count,
		},
	}
}

func TestProcessItemStackResponseInventorySlotIsGlobal(t *testing.T) {
	t.Parallel()
	b := newTestBot()
	b.InventoryMap[0] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 17}, Count: 1}
	b.InventoryMap[9] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 5}, Count: 7}

	player.ApplyItemStackResponse(b, &packet.ItemStackResponse{
		Responses: []protocol.ItemStackResponse{{
			Status: protocol.ItemStackResponseStatusOK,
			ContainerInfo: []protocol.StackResponseContainerInfo{{
				Container: protocol.FullContainerName{ContainerID: protocol.ContainerInventory},
				SlotInfo: []protocol.StackResponseSlotInfo{{
					Slot:           0,
					Count:          2,
					StackNetworkID: 99,
				}},
			}},
		}},
	})

	if got := b.InventoryMap[0].Count; got != 2 {
		t.Fatalf("slot 0 count = %d, want 2", got)
	}
	if got := b.InventoryMap[9].Count; got != 7 {
		t.Fatalf("slot 9 count = %d, want unchanged 7", got)
	}
	if got := b.StackNetworkIDs[0]; got != 99 {
		t.Fatalf("slot 0 stack ID = %d, want 99", got)
	}
}

func TestApplyInventoryContent_PartialWindowIDInventory(t *testing.T) {
	t.Parallel()
	b := newTestBot()
	b.InventoryMap[15] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 1}, Count: 5}
	b.InventoryMap[20] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 2}, Count: 3}

	// Server sends WindowIDInventory but only 9 items (hotbar-only).
	// Previously this wiped slots 9-35; now it should only update hotbar.
	content := make([]protocol.ItemInstance, 9)
	content[0] = protocol.ItemInstance{Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 17}, Count: 7}}

	p := &packet.InventoryContent{
		WindowID:  protocol.WindowIDInventory,
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Content:   content,
	}
	player.ApplyInventoryContent(b, p)

	if _, ok := b.InventoryMap[15]; !ok {
		t.Error("expected slot 15 (main inventory) to survive partial WindowIDInventory update")
	}
	if _, ok := b.InventoryMap[20]; !ok {
		t.Error("expected slot 20 (main inventory) to survive partial WindowIDInventory update")
	}
	if stack, ok := b.InventoryMap[0]; !ok || stack.Count != 7 {
		t.Errorf("expected slot 0 to have 7 items, got %v", stack)
	}
}

func TestApplyInventoryContent_FullWindowIDInventory(t *testing.T) {
	t.Parallel()
	b := newTestBot()
	b.InventoryMap[15] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 1}, Count: 5}

	content := make([]protocol.ItemInstance, 36)
	content[10] = protocol.ItemInstance{Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 17}, Count: 12}}

	p := &packet.InventoryContent{
		WindowID:  protocol.WindowIDInventory,
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Content:   content,
	}
	player.ApplyInventoryContent(b, p)

	if _, ok := b.InventoryMap[15]; ok {
		t.Error("expected slot 15 to be cleared on full sync")
	}
	if stack, ok := b.InventoryMap[10]; !ok || stack.Count != 12 {
		t.Errorf("expected slot 10 to have 12 items, got %v", stack)
	}
}

func TestApplyInventoryContentReportsOnlyRealHeldChanges(t *testing.T) {
	t.Parallel()

	t.Run("identical full sync", func(t *testing.T) {
		t.Parallel()
		b := newTestBot()
		b.HeldSlot = 2
		held := testItemInstance(58, 2, 41)
		b.InventoryMap[2] = held.Stack
		b.StackNetworkIDs[2] = held.StackNetworkID

		content := make([]protocol.ItemInstance, player.PlayerInvSlotCount)
		content[2] = held
		updated := player.ApplyInventoryContent(b, &packet.InventoryContent{
			WindowID:  protocol.WindowIDInventory,
			Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
			Content:   content,
		})

		if updated {
			t.Fatal("identical full sync requested an equipment refresh")
		}
	})

	t.Run("held count changes", func(t *testing.T) {
		t.Parallel()
		b := newTestBot()
		b.HeldSlot = 2
		before := testItemInstance(58, 2, 41)
		b.InventoryMap[2] = before.Stack
		b.StackNetworkIDs[2] = before.StackNetworkID

		content := make([]protocol.ItemInstance, player.PlayerInvSlotCount)
		content[2] = testItemInstance(58, 1, 41)
		updated := player.ApplyInventoryContent(b, &packet.InventoryContent{
			WindowID:  protocol.WindowIDInventory,
			Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
			Content:   content,
		})

		if !updated {
			t.Fatal("held count change did not request an equipment refresh")
		}
	})
}

func TestApplyInventorySlotReportsHeldSlotChanges(t *testing.T) {
	t.Parallel()

	b := newTestBot()
	b.HeldSlot = 2
	b.InventoryMap[2] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 58}, Count: 1}
	b.StackNetworkIDs[2] = 41

	heldUpdated := player.ApplyInventorySlot(b, &packet.InventorySlot{
		WindowID: protocol.WindowIDInventory,
		Slot:     2,
		NewItem:  protocol.ItemInstance{},
	})
	if !heldUpdated {
		t.Fatal("held slot update was not reported")
	}
	if _, ok := b.InventoryMap[2]; ok {
		t.Fatal("empty held slot remained in inventory")
	}
	if _, ok := b.StackNetworkIDs[2]; ok {
		t.Fatal("empty held slot retained its stack network ID")
	}

	nonHeldUpdated := player.ApplyInventorySlot(b, &packet.InventorySlot{
		WindowID: protocol.WindowIDInventory,
		Slot:     5,
		NewItem: protocol.ItemInstance{
			StackNetworkID: 42,
			Stack: protocol.ItemStack{
				ItemType: protocol.ItemType{NetworkID: 5},
				Count:    3,
			},
		},
	})
	if nonHeldUpdated {
		t.Fatal("non-held slot update requested an equipment refresh")
	}
}

func TestApplyItemStackResponseReportsHeldCountChange(t *testing.T) {
	t.Parallel()

	b := newTestBot()
	b.HeldSlot = 4
	b.InventoryMap[4] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 58}, Count: 2}
	b.StackNetworkIDs[4] = 51

	updated := player.ApplyItemStackResponse(b, &packet.ItemStackResponse{
		Responses: []protocol.ItemStackResponse{{
			Status: protocol.ItemStackResponseStatusOK,
			ContainerInfo: []protocol.StackResponseContainerInfo{{
				Container: protocol.FullContainerName{ContainerID: protocol.ContainerInventory},
				SlotInfo: []protocol.StackResponseSlotInfo{{
					Slot:           4,
					Count:          1,
					StackNetworkID: 51,
				}},
			}},
		}},
	})
	if !updated {
		t.Fatal("held stack response update was not reported")
	}
	if got := b.InventoryMap[4].Count; got != 1 {
		t.Fatalf("held stack count = %d, want 1", got)
	}
}

func TestApplyInventoryTransactionReportsHeldCountChange(t *testing.T) {
	t.Parallel()

	b := newTestBot()
	b.HeldSlot = 1
	before := testItemInstance(58, 2, 61)
	after := testItemInstance(58, 1, 61)
	b.InventoryMap[1] = before.Stack
	b.StackNetworkIDs[1] = before.StackNetworkID

	updated := player.ApplyInventoryTransaction(b, &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{{
			SourceType:    protocol.InventoryActionSourceContainer,
			WindowID:      protocol.Option(int8(protocol.WindowIDInventory)),
			InventorySlot: 1,
			OldItem:       before,
			NewItem:       after,
		}},
		TransactionData: &protocol.NormalTransactionData{},
	})

	if !updated {
		t.Fatal("held transaction count change did not request an equipment refresh")
	}
	if got := b.InventoryMap[1].Count; got != 1 {
		t.Fatalf("held transaction count = %d, want 1", got)
	}
}
