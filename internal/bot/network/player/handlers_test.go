package player

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestMergeMoveActorDeltaPosition(t *testing.T) {
	t.Parallel()
	current := mgl32.Vec3{10, 64, -20}

	tests := []struct {
		name     string
		incoming mgl32.Vec3
		flags    uint16
		want     mgl32.Vec3
	}{
		{
			name:     "no flags keeps current position",
			incoming: mgl32.Vec3{0, 0, 0},
			flags:    0,
			want:     mgl32.Vec3{10, 64, -20},
		},
		{
			name:     "only x updates x",
			incoming: mgl32.Vec3{11, 0, 0},
			flags:    packet.MoveActorDeltaFlagHasX,
			want:     mgl32.Vec3{11, 64, -20},
		},
		{
			name:     "only z updates z",
			incoming: mgl32.Vec3{0, 0, -21},
			flags:    packet.MoveActorDeltaFlagHasZ,
			want:     mgl32.Vec3{10, 64, -21},
		},
		{
			name:     "all axes update whole vector",
			incoming: mgl32.Vec3{1, 2, 3},
			flags:    packet.MoveActorDeltaFlagHasX | packet.MoveActorDeltaFlagHasY | packet.MoveActorDeltaFlagHasZ,
			want:     mgl32.Vec3{1, 2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mergeMoveActorDeltaPosition(current, tt.incoming, tt.flags)
			if got != tt.want {
				t.Fatalf("mergeMoveActorDeltaPosition(%v, %v, %d) = %v, want %v", current, tt.incoming, tt.flags, got, tt.want)
			}
		})
	}
}
