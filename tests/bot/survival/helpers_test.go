package survival_test

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Network IDs used by the armor fixtures. The real bot resolves item names
// through a network-ID-to-name map, so a fake item only needs a distinct ID and
// a matching entry in the fake's names map.
const (
	netIDLeatherHelmet  int32 = 100
	netIDIronChestplate int32 = 101
	netIDDiamondHelmet  int32 = 102
	netIDGoldenBoots    int32 = 103
	netIDWoodenPickaxe  int32 = 104
)

// stock puts a single item of the given type into a fake inventory slot,
// registering its name so the manager can resolve it.
func stock(b *fakeBot, slot uint32, networkID int32, name string) {
	b.inventory[slot] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: networkID}, Count: 1}
	b.names[networkID] = name
}
