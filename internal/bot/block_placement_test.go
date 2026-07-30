package bot

import (
	"testing"

	"bedrock-ai/internal/bot/placement"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func TestBuildPlacementPacketsMatchesBedrockPlacementCycle(t *testing.T) {
	t.Parallel()

	request := placement.Request{
		InventorySlot: 12,
		Destination:   protocol.BlockPos{4, 65, -2},
		Support:       protocol.BlockPos{4, 64, -2},
		Face:          BlockFaceTop,
		ClickedOffset: mgl32.Vec3{0.5, 1, 0.25},
	}
	heldItem := protocol.ItemInstance{
		StackNetworkID: 37,
		Stack: protocol.ItemStack{
			ItemType: protocol.ItemType{NetworkID: 58},
			Count:    1,
		},
	}
	playerPosition := mgl32.Vec3{2.5, 66.62, -2.5}
	start, transaction, stop := buildPlacementPackets(99, request, 2, heldItem, playerPosition, 0xdeadbeef)

	if start.ActionType != protocol.PlayerActionStartItemUseOn {
		t.Fatalf("start action = %d, want %d", start.ActionType, protocol.PlayerActionStartItemUseOn)
	}
	if start.BlockPosition != request.Support || start.ResultPosition != request.Destination || start.BlockFace != request.Face {
		t.Fatalf("start packet targets = %+v, want support=%v destination=%v face=%d", start, request.Support, request.Destination, request.Face)
	}

	data, ok := transaction.TransactionData.(*protocol.UseItemTransactionData)
	if !ok {
		t.Fatalf("transaction data type = %T, want *protocol.UseItemTransactionData", transaction.TransactionData)
	}
	if data.ActionType != protocol.UseItemActionClickBlock || data.TriggerType != protocol.TriggerTypePlayerInput {
		t.Fatalf("interaction action/trigger = %d/%d", data.ActionType, data.TriggerType)
	}
	if data.ClientPrediction != protocol.ClientPredictionSuccess {
		t.Fatalf("client prediction = %d, want success", data.ClientPrediction)
	}
	if data.BlockPosition != request.Support || data.BlockFace != request.Face || data.BlockRuntimeID != 0xdeadbeef {
		t.Fatalf("interaction target = %+v", data)
	}
	if data.HotBarSlot != 2 || data.HeldItem.StackNetworkID != heldItem.StackNetworkID {
		t.Fatalf("held item identity = slot %d stack ID %d", data.HotBarSlot, data.HeldItem.StackNetworkID)
	}
	if data.Position != playerPosition || data.ClickedPosition != request.ClickedOffset {
		t.Fatalf("interaction vectors = position %v click %v", data.Position, data.ClickedPosition)
	}
	if stop.ActionType != protocol.PlayerActionStopItemUseOn || stop.BlockPosition != request.Destination {
		t.Fatalf("stop packet = %+v", stop)
	}
}

func TestValidatePlacementRequestRejectsInvalidFaceAndOffset(t *testing.T) {
	t.Parallel()

	if err := validatePlacementRequest(placement.Request{Face: 6}); err == nil {
		t.Fatal("validatePlacementRequest() accepted invalid face")
	}
	if err := validatePlacementRequest(placement.Request{Face: BlockFaceTop, ClickedOffset: mgl32.Vec3{0.5, 1.1, 0.5}}); err == nil {
		t.Fatal("validatePlacementRequest() accepted offset outside block bounds")
	}
}
