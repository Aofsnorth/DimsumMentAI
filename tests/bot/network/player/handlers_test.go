package player_test

import (
	"testing"

	"bedrock-ai/internal/bot/network/player"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestMergeMoveActorDeltaPosition(t *testing.T) {
	t.Parallel()
	current := mgl32.Vec3{10, 64, -20}

	tests := []struct {
		name  string
		delta *packet.MoveActorDelta
		want  mgl32.Vec3
	}{
		{
			name:  "no axes present keeps current position",
			delta: &packet.MoveActorDelta{},
			want:  mgl32.Vec3{10, 64, -20},
		},
		{
			name:  "only x updates x",
			delta: &packet.MoveActorDelta{PositionX: protocol.Option(float32(11))},
			want:  mgl32.Vec3{11, 64, -20},
		},
		{
			name:  "only z updates z",
			delta: &packet.MoveActorDelta{PositionZ: protocol.Option(float32(-21))},
			want:  mgl32.Vec3{10, 64, -21},
		},
		{
			name: "all axes update whole vector",
			delta: &packet.MoveActorDelta{
				PositionX: protocol.Option(float32(1)),
				PositionY: protocol.Option(float32(2)),
				PositionZ: protocol.Option(float32(3)),
			},
			want: mgl32.Vec3{1, 2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := player.MergeMoveActorDeltaPosition(current, tt.delta)
			if got != tt.want {
				t.Fatalf("mergeMoveActorDeltaPosition(%v, %+v) = %v, want %v", current, tt.delta, got, tt.want)
			}
		})
	}
}
