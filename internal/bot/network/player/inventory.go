package player

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// isPlayerInventoryContainer reports whether a ContainerID refers to a slot in
// the player's own inventory family. The server may push updates via any of
// these IDs depending on what triggered the change (pickup, /give, drop into
// open inventory UI, etc.).
func isPlayerInventoryContainer(id byte) bool {
	switch id {
	case protocol.ContainerInventory,
		protocol.ContainerHotBar,
		protocol.ContainerCombinedHotBarAndInventory,
		protocol.ContainerOffhand,
		protocol.ContainerArmor:
		return true
	}
	return false
}

func isPlayerInventoryContent(p *packet.InventoryContent) bool {
	if p.WindowID == protocol.WindowIDInventory {
		return true
	}
	return isPlayerInventoryContainer(p.Container.ContainerID)
}

func isPlayerInventorySlot(p *packet.InventorySlot) bool {
	if p.WindowID == protocol.WindowIDInventory {
		return true
	}
	if c, ok := p.Container.Value(); ok {
		return isPlayerInventoryContainer(c.ContainerID)
	}
	return false
}
