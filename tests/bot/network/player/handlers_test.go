package player_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/dimension"
	"bedrock-ai/internal/bot/entity"
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

func TestHandleTransfer(t *testing.T) {
	t.Parallel()
	b := &bot.Bot{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	var joinedAddr string
	b.SetJoinHook(func(address string) error {
		joinedAddr = address
		return nil
	})

	handled := player.HandlePlayerPacket(b, &packet.Transfer{
		Address: "subserver.example.com",
		Port:    19132,
	})
	if !handled {
		t.Fatal("HandlePlayerPacket returned false for Transfer packet")
	}
	if joinedAddr != "subserver.example.com:19132" {
		t.Fatalf("joinedAddr = %q, want subserver.example.com:19132", joinedAddr)
	}
}

func TestHandleChangeDimension(t *testing.T) {
	t.Parallel()
	b := &bot.Bot{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Actors: map[uint64]*entity.Info{
			1: {ID: 1, Type: "minecraft:zombie"},
		},
	}

	handled := player.HandlePlayerPacket(b, &packet.ChangeDimension{
		Dimension: 1, // Nether
		Position:  mgl32.Vec3{100, 64, -200},
	})
	if !handled {
		t.Fatal("HandlePlayerPacket returned false for ChangeDimension packet")
	}
	if b.ChunkDimension != 1 {
		t.Errorf("ChunkDimension = %d, want 1", b.ChunkDimension)
	}
	if b.Dimension != dimension.Nether {
		t.Errorf("Dimension = %v, want Nether", b.Dimension)
	}
	if b.Pos != (mgl32.Vec3{100, 64, -200}) {
		t.Errorf("Pos = %v, want (100, 64, -200)", b.Pos)
	}
	if !b.TeleportAckPending {
		t.Error("TeleportAckPending = false, want true")
	}
	if len(b.Actors) != 0 {
		t.Errorf("Actors len = %d, want 0 after dimension change", len(b.Actors))
	}
}

func TestHandleAddActorWithNametag(t *testing.T) {
	t.Parallel()
	b := &bot.Bot{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Actors:              make(map[uint64]*entity.Info),
		UniqueIDToRuntimeID: make(map[int64]uint64),
	}

	meta := protocol.NewEntityMetadata()
	meta[uint32(protocol.EntityDataKeyName)] = "HorizonSMP Join Sekarang!"

	handled := player.HandlePlayerPacket(b, &packet.AddActor{
		EntityRuntimeID: 88,
		EntityUniqueID:  888,
		EntityType:      "minecraft:armor_stand",
		Position:        mgl32.Vec3{63, 73, -52},
		EntityMetadata:  meta,
	})
	if !handled {
		t.Fatal("HandlePlayerPacket returned false for AddActor packet")
	}

	act, ok := b.Actors[88]
	if !ok {
		t.Fatal("actor 88 not in b.Actors")
	}
	if act.Name != "HorizonSMP Join Sekarang!" {
		t.Errorf("act.Name = %q, want 'HorizonSMP Join Sekarang!'", act.Name)
	}
}

func TestHandleRespawn(t *testing.T) {
	t.Parallel()
	b := &bot.Bot{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	handled := player.HandlePlayerPacket(b, &packet.Respawn{
		State:    packet.RespawnStateReadyToSpawn,
		Position: mgl32.Vec3{0, 65.62, 0},
	})
	if !handled {
		t.Fatal("HandlePlayerPacket returned false for Respawn packet")
	}
	if !b.TeleportAckPending {
		t.Error("TeleportAckPending = false, want true")
	}
	wantPos := mgl32.Vec3{0, 64.0, 0}
	if mathAbs(b.Pos.X()-wantPos.X()) > 0.01 || mathAbs(b.Pos.Y()-wantPos.Y()) > 0.01 || mathAbs(b.Pos.Z()-wantPos.Z()) > 0.01 {
		t.Errorf("Pos = %v, want %v", b.Pos, wantPos)
	}
}

func mathAbs(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
