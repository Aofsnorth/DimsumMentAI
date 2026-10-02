package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestBlockActionsEmbeddedInNextPlayerAuthInput(t *testing.T) {
	b := &bot.Bot{}
	pos := protocol.BlockPos{7, 63, -4}
	b.BeginServerAuthBreak(pos, 1)

	inputData := protocol.NewInputFlags(packet.InputFlagCount)
	tc := &movement.TickContext{B: b}
	actions := tc.TakeBlockActions(&inputData)
	if len(actions) != 1 || actions[0].Action != protocol.PlayerActionStartBreak {
		t.Fatalf("TakeBlockActions() = %+v, want a single StartBreak", actions)
	}
	if !inputData.Load(packet.InputFlagPerformBlockActions) {
		t.Fatal("PerformBlockActions input flag is not set")
	}

	inputPacket := tc.BuildPlayerAuthInputPacket(inputData, nil, nil, actions)
	embedded, present := inputPacket.BlockActions.Value()
	if !present || len(embedded) != 1 {
		t.Fatalf("PlayerAuthInput BlockActions = (present=%v, %+v), want one embedded action", present, embedded)
	}
	if embedded[0].BlockPos != pos {
		t.Fatalf("embedded action position = %v, want %v", embedded[0].BlockPos, pos)
	}

	// The next tick carries the per-tick ContinueDestroy while mining.
	inputData2 := protocol.NewInputFlags(packet.InputFlagCount)
	actions2 := tc.TakeBlockActions(&inputData2)
	if len(actions2) != 1 || actions2[0].Action != protocol.PlayerActionContinueDestroyBlock {
		t.Fatalf("second TakeBlockActions() = %+v, want a single ContinueDestroy", actions2)
	}

	// A tick with no queued actions must leave BlockActions absent so the
	// optional field does not marshal an empty present list.
	b.FinishServerAuthBreak(pos, 1)
	tc.TakeBlockActions(&inputData2)
	inputData3 := protocol.NewInputFlags(packet.InputFlagCount)
	if actions := tc.TakeBlockActions(&inputData3); len(actions) != 0 {
		t.Fatalf("post-break TakeBlockActions() = %+v, want none", actions)
	}
	if inputData3.Load(packet.InputFlagPerformBlockActions) {
		t.Fatal("PerformBlockActions flag set on a tick with no block actions")
	}
	empty := tc.BuildPlayerAuthInputPacket(inputData3, nil, nil, nil)
	if _, present := empty.BlockActions.Value(); present {
		t.Fatal("BlockActions optional is present on a packet with no actions")
	}
}

func TestItemStackRequestEmbeddedInNextPlayerAuthInput(t *testing.T) {
	b := &bot.Bot{}
	request := protocol.ItemStackRequest{RequestID: 7}
	if err := b.QueueItemStackRequest(request); err != nil {
		t.Fatalf("QueueItemStackRequest() error = %v", err)
	}

	inputData := protocol.NewInputFlags(packet.InputFlagCount)
	tc := &movement.TickContext{B: b}
	queuedRequest := tc.TakeItemStackRequest(&inputData)
	if queuedRequest == nil {
		t.Fatal("TakeItemStackRequest() returned nil")
	}
	if !inputData.Load(packet.InputFlagPerformItemStackRequest) {
		t.Fatal("PerformItemStackRequest input flag is not set")
	}

	inputPacket := tc.BuildPlayerAuthInputPacket(inputData, nil, queuedRequest, nil)
	if !inputPacket.InputData.Load(packet.InputFlagPerformItemStackRequest) {
		t.Fatal("PlayerAuthInput is missing PerformItemStackRequest flag")
	}
	embedded, ok := inputPacket.ItemStackRequest.Value()
	if !ok {
		t.Fatal("PlayerAuthInput is missing the embedded item stack request")
	}
	if embedded.RequestID != request.RequestID {
		t.Fatalf("embedded request ID = %d, want %d", embedded.RequestID, request.RequestID)
	}
	if _, ok := b.TakeItemStackRequest(); ok {
		t.Fatal("item stack request remained queued after take")
	}
}

func TestItemInteractionDataEmbeddedInNextPlayerAuthInput(t *testing.T) {
	b := &bot.Bot{}
	data := protocol.UseItemTransactionData{ActionType: protocol.UseItemActionClickBlock}
	if err := b.QueueItemInteractionData(data); err != nil {
		t.Fatalf("QueueItemInteractionData() error = %v", err)
	}

	inputData := protocol.NewInputFlags(packet.InputFlagCount)
	tc := &movement.TickContext{B: b}
	queuedData := tc.TakeItemInteractionData(&inputData)
	if queuedData == nil {
		t.Fatal("TakeItemInteractionData() returned nil")
	}
	if !inputData.Load(packet.InputFlagPerformItemInteraction) {
		t.Fatal("PerformItemInteraction input flag is not set")
	}

	inputPacket := tc.BuildPlayerAuthInputPacket(inputData, queuedData, nil, nil)
	if !inputPacket.InputData.Load(packet.InputFlagPerformItemInteraction) {
		t.Fatal("PlayerAuthInput is missing PerformItemInteraction flag")
	}
	embeddedData, ok := inputPacket.ItemInteractionData.Value()
	if !ok {
		t.Fatal("PlayerAuthInput is missing the embedded item interaction data")
	}
	if embeddedData.ActionType != data.ActionType {
		t.Fatalf("embedded action type = %d, want %d", embeddedData.ActionType, data.ActionType)
	}
	if _, ok := b.TakeItemInteractionData(); ok {
		t.Fatal("item interaction data remained queued after take")
	}
}
