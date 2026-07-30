// Package placement defines the input required for a server-confirmed block
// placement operation.
package placement

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Request describes one block placement against a supporting block face.
type Request struct {
	InventorySlot uint32
	Destination   protocol.BlockPos
	Support       protocol.BlockPos
	Face          int32
	ClickedOffset mgl32.Vec3
}
