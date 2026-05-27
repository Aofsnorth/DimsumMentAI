package player

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const mainInventoryContainerID byte = 0x1b

func isPlayerInventoryContent(p *packet.InventoryContent) bool {
	if p.WindowID == protocol.WindowIDInventory {
		return true
	}
	return p.Container.ContainerID == mainInventoryContainerID || p.Container.ContainerID == 0
}

func isPlayerInventorySlot(p *packet.InventorySlot) bool {
	if p.WindowID == protocol.WindowIDInventory {
		return true
	}
	if container, ok := p.Container.Value(); ok {
		return container.ContainerID == mainInventoryContainerID || container.ContainerID == 0
	}
	return false
}
