package animation

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// MineSwing builds the arm animation used for block-breaking actions.
func MineSwing(entityRuntimeID uint64) *packet.Animate {
	return &packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: entityRuntimeID,
		SwingSource:     packet.AnimateSwingSourceMine,
	}
}
