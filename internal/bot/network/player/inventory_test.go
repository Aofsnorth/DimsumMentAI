package player

import (
	"bedrock-ai/internal/bot"
	"log/slog"
	"os"
	"testing"

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
		got := containerSlotOffset(tt.containerID)
		if got != tt.want {
			t.Errorf("containerSlotOffset(0x%02x) = %d, want %d", tt.containerID, got, tt.want)
		}
	}
}

func TestCoveredSlotSet_HotBar(t *testing.T) {
	t.Parallel()
	slots := coveredSlotSet(protocol.ContainerHotBar)
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
	slots := coveredSlotSet(protocol.ContainerInventory)
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
	slots := coveredSlotSet(protocol.ContainerArmor)
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
	slots := coveredSlotSet(protocol.ContainerOffhand)
	if len(slots) != 1 {
		t.Fatalf("expected 1 offhand slot, got %d", len(slots))
	}
	if _, ok := slots[40]; !ok {
		t.Error("expected slot 40 in offhand set")
	}
}

func TestCoveredSlotSet_Combined(t *testing.T) {
	t.Parallel()
	slots := coveredSlotSet(protocol.ContainerCombinedHotBarAndInventory)
	if len(slots) != 36 {
		t.Fatalf("expected 36 combined slots, got %d", len(slots))
	}
}

func TestCoveredSlotSet_Unknown(t *testing.T) {
	t.Parallel()
	slots := coveredSlotSet(0xFF)
	if slots != nil {
		t.Errorf("expected nil for unknown container, got %v", slots)
	}
}

func TestSlotRange(t *testing.T) {
	t.Parallel()
	r := slotRange(5, 8)
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
		got := isPlayerInventoryContainer(tt.id)
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
		{protocol.InventoryAction{WindowID: protocol.WindowIDInventory, InventorySlot: 0}, 0, true},
		{protocol.InventoryAction{WindowID: protocol.WindowIDInventory, InventorySlot: 35}, 35, true},
		{protocol.InventoryAction{WindowID: protocol.WindowIDInventory, InventorySlot: 36}, 0, false},
		{protocol.InventoryAction{WindowID: protocol.WindowIDArmour, InventorySlot: 0}, 36, true},
		{protocol.InventoryAction{WindowID: protocol.WindowIDArmour, InventorySlot: 3}, 39, true},
		{protocol.InventoryAction{WindowID: protocol.WindowIDArmour, InventorySlot: 4}, 0, false},
		{protocol.InventoryAction{WindowID: protocol.WindowIDOffHand, InventorySlot: 0}, 40, true},
		{protocol.InventoryAction{WindowID: protocol.WindowIDOffHand, InventorySlot: 1}, 0, false},
		{protocol.InventoryAction{WindowID: 123, InventorySlot: 0}, 0, false},
	}
	for _, tt := range tests {
		got, ok := transactionSlotToGlobal(tt.action)
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
		{protocol.InventoryAction{SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.WindowIDInventory}, true},
		{protocol.InventoryAction{SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.WindowIDArmour}, true},
		{protocol.InventoryAction{SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.WindowIDOffHand}, true},
		{protocol.InventoryAction{SourceType: protocol.InventoryActionSourceWorld, WindowID: protocol.WindowIDInventory}, false},
		{protocol.InventoryAction{SourceType: protocol.InventoryActionSourceContainer, WindowID: 123}, false},
	}
	for _, tt := range tests {
		got := isPlayerInventoryTransaction(tt.action)
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
	applyInventoryContent(b, p)

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
	applyInventoryContent(b, p)

	if _, ok := b.InventoryMap[15]; ok {
		t.Error("expected slot 15 to be cleared on full sync")
	}
	if stack, ok := b.InventoryMap[10]; !ok || stack.Count != 12 {
		t.Errorf("expected slot 10 to have 12 items, got %v", stack)
	}
}
