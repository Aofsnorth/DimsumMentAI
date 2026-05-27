package player

import (
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestInventoryContentOnlyTracksMainInventory(t *testing.T) {
	tests := []struct {
		name string
		pk   *packet.InventoryContent
		want bool
	}{
		{
			name: "main inventory window",
			pk: &packet.InventoryContent{
				WindowID: protocol.WindowIDInventory,
			},
			want: true,
		},
		{
			name: "armour window must not replace inventory",
			pk: &packet.InventoryContent{
				WindowID:  protocol.WindowIDArmour,
				Container: protocol.FullContainerName{ContainerID: 0},
			},
			want: false,
		},
		{
			name: "ui/crafting window must not replace inventory",
			pk: &packet.InventoryContent{
				WindowID:  protocol.WindowIDUI,
				Container: protocol.FullContainerName{ContainerID: 0},
			},
			want: false,
		},
		{
			name: "offhand window must not replace inventory",
			pk: &packet.InventoryContent{
				WindowID:  protocol.WindowIDOffHand,
				Container: protocol.FullContainerName{ContainerID: 0},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPlayerInventoryContent(tt.pk); got != tt.want {
				t.Fatalf("isPlayerInventoryContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInventorySlotOnlyTracksMainInventory(t *testing.T) {
	tests := []struct {
		name string
		pk   *packet.InventorySlot
		want bool
	}{
		{
			name: "main inventory window",
			pk: &packet.InventorySlot{
				WindowID: protocol.WindowIDInventory,
			},
			want: true,
		},
		{
			name: "armour window must not mutate inventory",
			pk: &packet.InventorySlot{
				WindowID:  protocol.WindowIDArmour,
				Container: protocol.Option(protocol.FullContainerName{ContainerID: 0}),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPlayerInventorySlot(tt.pk); got != tt.want {
				t.Fatalf("isPlayerInventorySlot() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTakeItemActorRemovesTrackedDrop(t *testing.T) {
	b := &bot.Bot{
		Actors: map[uint64]*entity.Info{
			42: {ID: 42, Type: "minecraft:item", Position: mgl32.Vec3{1, 2, 3}},
		},
		UniqueIDToRuntimeID: map[int64]uint64{},
	}

	handled := HandlePlayerPacket(b, &packet.TakeItemActor{ItemEntityRuntimeID: 42, TakerEntityRuntimeID: 99})
	if !handled {
		t.Fatal("TakeItemActor was not handled")
	}
	if _, ok := b.Actors[42]; ok {
		t.Fatal("item actor was not removed after pickup")
	}
}
