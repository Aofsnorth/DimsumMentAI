package player

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func isPlayerInventoryContent(p *packet.InventoryContent) bool {
	return p.WindowID == protocol.WindowIDInventory
}

func isPlayerInventorySlot(p *packet.InventorySlot) bool {
	return p.WindowID == protocol.WindowIDInventory
}
