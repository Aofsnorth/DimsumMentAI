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

// InteractSwing builds the arm animation a real client sends when pressing
// something: a button, a door, a sign or an NPC. The SwingSource field is what
// distinguishes it from a mining swing, and it is why the hand visibly moves
// when the bot clicks instead of pressing a button with a frozen body.
func InteractSwing(entityRuntimeID uint64) *packet.Animate {
	return &packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: entityRuntimeID,
		SwingSource:     packet.AnimateSwingSourceInteract,
	}
}
