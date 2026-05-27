package bot

import (
	"testing"

	"bedrock-ai/internal/bot/entity"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func TestGetInventorySlotsReturnsSnapshot(t *testing.T) {
	b := &Bot{
		InventoryMap: map[uint32]protocol.ItemStack{
			3: {ItemType: protocol.ItemType{NetworkID: 10}, Count: 2},
		},
	}

	snapshot := b.GetInventorySlots()
	delete(snapshot, 3)

	if _, ok := b.InventoryMap[3]; !ok {
		t.Fatal("mutating GetInventorySlots result changed bot inventory")
	}
}

func TestGetEntitiesReturnsSnapshot(t *testing.T) {
	b := &Bot{
		Actors: map[uint64]*entity.Info{
			7: {ID: 7, Position: mgl32.Vec3{1, 2, 3}},
		},
	}

	snapshot := b.GetEntities()
	snapshot[7].Position = mgl32.Vec3{9, 9, 9}
	delete(snapshot, 7)

	if _, ok := b.Actors[7]; !ok {
		t.Fatal("deleting from GetEntities result changed bot actors")
	}
	if got := b.Actors[7].Position; got != (mgl32.Vec3{1, 2, 3}) {
		t.Fatalf("mutating GetEntities result changed actor position to %v", got)
	}
}
