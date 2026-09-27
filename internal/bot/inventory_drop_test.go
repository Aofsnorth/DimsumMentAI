package bot

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The ghost-item bug these tests pin: DropItem used to pick a matching stack
// from raw map iteration, so with several dirt stacks the drop often came from
// a slot nobody was looking at while the held dirt stayed rendered in the hand.

func TestDropTargetSlotPrefersHeldSlot(t *testing.T) {
	t.Parallel()

	b := &Bot{
		InventoryMap: map[uint32]protocol.ItemStack{
			2: {ItemType: protocol.ItemType{NetworkID: 3}, Count: 64}, // dirt, main inv
			5: {ItemType: protocol.ItemType{NetworkID: 3}, Count: 32}, // dirt, held
		},
		StackNetworkIDs: map[uint32]int32{2: 100, 5: 200},
		ItemNames:       map[int32]string{3: "minecraft:dirt"},
		HeldSlot:        5,
	}

	slot, item, ok := b.dropTargetSlotLocked("dirt")
	if !ok {
		t.Fatal("dropTargetSlotLocked(dirt) not found, want the held slot")
	}
	if slot != 5 {
		t.Fatalf("slot = %d, want held slot 5 so the hand clears visually", slot)
	}
	if item.Count != 32 {
		t.Fatalf("item count = %d, want the held stack's 32", item.Count)
	}
}

func TestDropTargetSlotFallsBackToLowestSlot(t *testing.T) {
	t.Parallel()

	b := &Bot{
		InventoryMap: map[uint32]protocol.ItemStack{
			0: {ItemType: protocol.ItemType{NetworkID: 9}, Count: 1},  // not dirt
			4: {ItemType: protocol.ItemType{NetworkID: 3}, Count: 64}, // dirt
			7: {ItemType: protocol.ItemType{NetworkID: 3}, Count: 7},  // dirt
		},
		StackNetworkIDs: map[uint32]int32{4: 100, 7: 101},
		ItemNames:       map[int32]string{3: "minecraft:dirt", 9: "minecraft:cobblestone"},
		HeldSlot:        0,
	}

	slot, _, ok := b.dropTargetSlotLocked("dirt")
	if !ok {
		t.Fatal("dropTargetSlotLocked(dirt) not found")
	}
	// Deterministic: always the lowest matching slot, never map order.
	if slot != 4 {
		t.Fatalf("slot = %d, want lowest matching slot 4", slot)
	}
}

func TestDropTargetSlotMatchesCaseInsensitively(t *testing.T) {
	t.Parallel()

	b := &Bot{
		InventoryMap: map[uint32]protocol.ItemStack{
			1: {ItemType: protocol.ItemType{NetworkID: 3}, Count: 5},
		},
		ItemNames: map[int32]string{3: "minecraft:dirt"},
		HeldSlot:  1,
	}

	if _, _, ok := b.dropTargetSlotLocked("Dirt"); !ok {
		t.Fatal("dropTargetSlotLocked(Dirt) not found, want case-insensitive match")
	}
}

func TestDropTargetSlotSkipsEmptyStacks(t *testing.T) {
	t.Parallel()

	b := &Bot{
		InventoryMap: map[uint32]protocol.ItemStack{
			0: {ItemType: protocol.ItemType{NetworkID: 3}, Count: 0}, // stale empty entry
			1: {ItemType: protocol.ItemType{NetworkID: 3}, Count: 2},
		},
		ItemNames: map[int32]string{3: "minecraft:dirt"},
		HeldSlot:  0,
	}

	slot, item, ok := b.dropTargetSlotLocked("dirt")
	if !ok {
		t.Fatal("dropTargetSlotLocked(dirt) not found")
	}
	if slot != 1 || item.Count != 2 {
		t.Fatalf("slot=%d count=%d, want slot 1 with count 2 (skipping the empty stack)", slot, item.Count)
	}
}

func TestDropTargetSlotNotFound(t *testing.T) {
	t.Parallel()

	b := &Bot{
		InventoryMap: map[uint32]protocol.ItemStack{
			0: {ItemType: protocol.ItemType{NetworkID: 9}, Count: 1},
		},
		ItemNames: map[int32]string{9: "minecraft:cobblestone"},
		HeldSlot:  0,
	}

	if _, _, ok := b.dropTargetSlotLocked("dirt"); ok {
		t.Fatal("dropTargetSlotLocked(dirt) found an item, want not found")
	}
}

// UnequipItem used to send HotBarSlot 0 with an empty item while leaving
// b.HeldSlot pointing at the old slot: the server-side selection and the local
// state disagreed, and the next MobEquipment echo flipped the hand back.

func TestEmptyHotbarSlotSkipsOccupied(t *testing.T) {
	t.Parallel()

	b := &Bot{
		InventoryMap: map[uint32]protocol.ItemStack{
			0: {ItemType: protocol.ItemType{NetworkID: 3}, Count: 64},
			1: {ItemType: protocol.ItemType{NetworkID: 4}, Count: 1},
		},
	}

	if slot := b.emptyHotbarSlotLocked(); slot != 2 {
		t.Fatalf("emptyHotbarSlotLocked() = %d, want 2 (first two occupied)", slot)
	}
}

func TestEmptyHotbarSlotTreatsZeroCountAsEmpty(t *testing.T) {
	t.Parallel()

	b := &Bot{
		InventoryMap: map[uint32]protocol.ItemStack{
			0: {ItemType: protocol.ItemType{NetworkID: 3}, Count: 0},
		},
	}

	if slot := b.emptyHotbarSlotLocked(); slot != 0 {
		t.Fatalf("emptyHotbarSlotLocked() = %d, want 0 (zero-count stack is empty)", slot)
	}
}

func TestEmptyHotbarSlotFullHotbar(t *testing.T) {
	t.Parallel()

	b := &Bot{InventoryMap: map[uint32]protocol.ItemStack{}}
	for slot := uint32(0); slot < 9; slot++ {
		b.InventoryMap[slot] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 3}, Count: 1}
	}

	if slot := b.emptyHotbarSlotLocked(); slot != 9 {
		t.Fatalf("emptyHotbarSlotLocked() = %d, want sentinel 9 (hotbar full)", slot)
	}
}
