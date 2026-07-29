package movement

import (
	"testing"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestItemStackRequestEmbeddedInNextPlayerAuthInput(t *testing.T) {
	b := &bot.Bot{}
	request := protocol.ItemStackRequest{RequestID: 7}
	if err := b.QueueItemStackRequest(request); err != nil {
		t.Fatalf("QueueItemStackRequest() error = %v", err)
	}

	inputData := protocol.NewBitset(packet.PlayerAuthInputBitsetSize)
	tc := &TickContext{B: b}
	queuedRequest := tc.takeItemStackRequest(inputData)
	if queuedRequest == nil {
		t.Fatal("takeItemStackRequest() returned nil")
	}
	if !inputData.Load(packet.InputFlagPerformItemStackRequest) {
		t.Fatal("PerformItemStackRequest input flag is not set")
	}

	inputPacket := tc.buildPlayerAuthInputPacket(inputData, queuedRequest)
	if !inputPacket.InputData.Load(packet.InputFlagPerformItemStackRequest) {
		t.Fatal("PlayerAuthInput is missing PerformItemStackRequest flag")
	}
	if inputPacket.ItemStackRequest.RequestID != request.RequestID {
		t.Fatalf("embedded request ID = %d, want %d", inputPacket.ItemStackRequest.RequestID, request.RequestID)
	}
	if _, ok := b.TakeItemStackRequest(); ok {
		t.Fatal("item stack request remained queued after take")
	}
}
