package survival_test

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// TestEatingUsesStandaloneClickAirOnTheHotbarSlot: Geyser ignores inline
// PlayerAuthInput item interactions except block breaks, so eating never
// happened. Food use is a standalone InventoryTransaction ClickAir carrying
// the real hotbar slot (food from the main inventory is swapped there first)
// and its current stack.
func TestEatingUsesStandaloneClickAirOnTheHotbarSlot(t *testing.T) {
	b := newFakeBot()
	b.names[7] = "minecraft:bread"
	b.inventory[20] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 7}, Count: 3}

	m := newTestManager(b)
	if !m.EatBestFood() {
		t.Fatal("EatBestFood found no food")
	}

	if len(b.queued) != 0 {
		t.Fatalf("queued %d inline interactions, want 0", len(b.queued))
	}
	var txs []*packet.InventoryTransaction
	for _, pk := range b.written {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			txs = append(txs, tx)
		}
	}
	if len(txs) != 1 {
		t.Fatalf("wrote %d InventoryTransactions, want 1", len(txs))
	}
	use, ok := txs[0].TransactionData.(*protocol.UseItemTransactionData)
	if !ok {
		t.Fatalf("TransactionData = %T, want *UseItemTransactionData", txs[0].TransactionData)
	}
	if use.ActionType != protocol.UseItemActionClickAir {
		t.Errorf("ActionType = %d, want ClickAir", use.ActionType)
	}
	if use.HotBarSlot < 0 || use.HotBarSlot > 8 {
		t.Errorf("HotBarSlot = %d, want a hotbar slot 0-8", use.HotBarSlot)
	}
	if use.HeldItem.Stack.NetworkID != 7 {
		t.Errorf("HeldItem network ID = %d, want the bread (7)", use.HeldItem.Stack.NetworkID)
	}
}

// TestEatingHeldFoodEmitsNoEquipPacket: MobEquipment is what took the connection
// down on the Geyser host, so a meal out of the selected hotbar slot must cost
// only the use-item transaction.
func TestEatingHeldFoodEmitsNoEquipPacket(t *testing.T) {
	b := newFakeBot()
	b.held = 5
	b.names[271] = "minecraft:cookie"
	b.inventory[5] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 271}, Count: 24}

	if !newTestManager(b).EatBestFood() {
		t.Fatal("EatBestFood found no food")
	}

	if len(b.equipped) != 0 {
		t.Errorf("equipped %v, want no equip when the food is already held", b.equipped)
	}
	for _, pk := range b.written {
		if _, ok := pk.(*packet.MobEquipment); ok {
			t.Error("a MobEquipment was written for a meal out of the held slot")
		}
	}
}

// TestEatingIgnoresNonFoodLookalikes: substring matching treated these as food.
func TestEatingIgnoresNonFoodLookalikes(t *testing.T) {
	b := newFakeBot()
	b.names[1] = "minecraft:carrot_on_a_stick"
	b.names[2] = "minecraft:beetroot_seeds"
	b.inventory[0] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 1}, Count: 1}
	b.inventory[1] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 2}, Count: 5}

	if newTestManager(b).EatBestFood() {
		t.Error("ate a carrot_on_a_stick / beetroot_seeds")
	}
}
