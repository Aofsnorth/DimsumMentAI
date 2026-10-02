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

// PlaceSwing builds the one arm swing a real client sends when placing a
// block — placing is a single right-click, not a mining loop, so exactly one
// swing goes out. Callers must not pace it: a second swing within ~250ms
// restarts the viewer's arm cycle mid-flight and reads as a twitch.
func PlaceSwing(entityRuntimeID uint64) *packet.Animate {
	return &packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: entityRuntimeID,
		SwingSource:     packet.AnimateSwingSourceBuild,
	}
}
