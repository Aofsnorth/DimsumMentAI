package bot_test

import (
	"testing"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// A placement names the held item back to the server. A stack the server itself
// sent carries NBT and a block runtime ID, and echoing those in a client-sent
// item packet is what a Geyser front-end refuses — it answers with silence, so
// the placement never confirms and the bot reports a timeout rather than a
// rejection. Reducing the stack to type and count is what makes it act on.
func TestPlacementSendsABareStackOnGeyser(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{GeyserCompat: true}
	b.SetHeldSlotForTest(0)
	b.SetInventorySlotForTest(0, protocol.ItemStack{
		ItemType:       protocol.ItemType{NetworkID: -967},
		Count:          1,
		BlockRuntimeID: 4242,
		NBTData:        map[string]any{"GeyserHash": int64(1234)},
	})
	b.SetStackNetworkIDForTest(0, 19)

	_, held, ok := b.HeldItemInstance()
	if !ok {
		t.Fatal("the held slot reported empty, so nothing can be asserted about what was sent")
	}

	if len(held.Stack.NBTData) != 0 {
		t.Errorf("the placement echoed %v back to the server; NBT is not accepted on this path",
			held.Stack.NBTData)
	}
	if held.Stack.BlockRuntimeID != 0 {
		t.Errorf("the placement echoed block runtime ID %d; it is not accepted on this path",
			held.Stack.BlockRuntimeID)
	}
}

// Stripping the identity off the stack would leave the server unable to tell
// which item was placed, so the type and count have to survive.
func TestPlacementKeepsTheItemIdentity(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{GeyserCompat: true}
	b.SetHeldSlotForTest(0)
	b.SetInventorySlotForTest(0, protocol.ItemStack{
		ItemType: protocol.ItemType{NetworkID: -967},
		Count:    1,
		NBTData:  map[string]any{"GeyserHash": int64(1234)},
	})
	b.SetStackNetworkIDForTest(0, 19)

	_, held, ok := b.HeldItemInstance()
	if !ok {
		t.Fatal("the held slot reported empty")
	}

	if held.Stack.NetworkID != -967 {
		t.Errorf("NetworkID = %d, want -967: the item must still be identifiable", held.Stack.NetworkID)
	}
	if held.Stack.Count != 1 {
		t.Errorf("Count = %d, want 1", held.Stack.Count)
	}
	// The stack ID is what distinguishes one stack from another server-side, so
	// dropping it makes the placement ambiguous rather than merely terse.
	if held.StackNetworkID != 19 {
		t.Errorf("StackNetworkID = %d, want 19", held.StackNetworkID)
	}
}

// BDS is a different host with different expectations: it reads the echoed NBT
// and block runtime ID as part of the interaction. Gating the reduction on
// Geyser keeps an already-working session from changing behaviour.
func TestPlacementKeepsTheFullStackOnBDS(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{GeyserCompat: false}
	b.SetHeldSlotForTest(0)
	b.SetInventorySlotForTest(0, protocol.ItemStack{
		ItemType:       protocol.ItemType{NetworkID: 5},
		Count:          1,
		BlockRuntimeID: 4242,
		NBTData:        map[string]any{"display": "named"},
	})
	b.SetStackNetworkIDForTest(0, 19)

	_, held, ok := b.HeldItemInstance()
	if !ok {
		t.Fatal("the held slot reported empty")
	}

	if len(held.Stack.NBTData) == 0 {
		t.Error("the BDS placement stripped NBT; that host reads it as part of the interaction")
	}
	if held.Stack.BlockRuntimeID != 4242 {
		t.Errorf("BlockRuntimeID = %d, want 4242 on BDS", held.Stack.BlockRuntimeID)
	}
}
