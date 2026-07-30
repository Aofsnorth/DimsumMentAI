package animation

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestMineSwing(t *testing.T) {
	const entityRuntimeID uint64 = 42

	got := MineSwing(entityRuntimeID)

	if got.ActionType != packet.AnimateActionSwingArm {
		t.Fatalf("ActionType = %d, want %d", got.ActionType, packet.AnimateActionSwingArm)
	}
	if got.EntityRuntimeID != entityRuntimeID {
		t.Fatalf("EntityRuntimeID = %d, want %d", got.EntityRuntimeID, entityRuntimeID)
	}
	if got.SwingSource != packet.AnimateSwingSourceMine {
		t.Fatalf("SwingSource = %d, want %d", got.SwingSource, packet.AnimateSwingSourceMine)
	}
}
