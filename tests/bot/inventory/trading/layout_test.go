package trading_test

import (
	"testing"

	"bedrock-ai/internal/bot/inventory/trading"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// TestTradeContainerIDsPinTheVendoredConstants is the regression this file
// exists for. The trade window is not addressed by the window ID the server
// assigned: a StackRequestSlotInfo names one of the per-station container
// constants, and a server refuses an ID it does not recognise. Getting the
// number wrong means the placement is rejected while the code around it reads
// as though it worked.
//
// The numbers are pinned against protocol/container.go as vendored by
// gophertunnel v1.62.0, not against a comment.
func TestTradeContainerIDsPinTheVendoredConstants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  byte
		want int
	}{
		{"ingredient one", trading.IngredientOneContainerID, protocol.ContainerTradeIngredientOne},
		{"ingredient two", trading.IngredientTwoContainerID, protocol.ContainerTradeIngredientTwo},
		{"result preview", trading.ResultContainerID, protocol.ContainerTradeResultPreview},
		{"two-ingredient one", trading.IngredientTwoOneContainerID, protocol.ContainerTradeTwoIngredientOne},
		{"two-ingredient two", trading.IngredientTwoTwoContainerID, protocol.ContainerTradeTwoIngredientTwo},
		{"two-ingredient result", trading.TwoResultContainerID, protocol.ContainerTradeTwoResultPreview},
	}
	for _, tc := range cases {
		if int(tc.got) != tc.want {
			t.Errorf("%s container id = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// TestTradeContainerIDsAreTheVendoredNumbers states the literal values as well,
// so a change in the vendored protocol shows up here as a failure to read
// rather than as a silently different number.
func TestTradeContainerIDsAreTheVendoredNumbers(t *testing.T) {
	t.Parallel()

	if got := trading.IngredientOneContainerID; got != 31 {
		t.Errorf("ContainerTradeIngredientOne = %d, want 31", got)
	}
	if got := trading.IngredientTwoContainerID; got != 32 {
		t.Errorf("ContainerTradeIngredientTwo = %d, want 32", got)
	}
	if got := trading.ResultContainerID; got != 33 {
		t.Errorf("ContainerTradeResultPreview = %d, want 33", got)
	}
	if got := trading.IngredientTwoOneContainerID; got != 47 {
		t.Errorf("ContainerTradeTwoIngredientOne = %d, want 47", got)
	}
	if got := trading.IngredientTwoTwoContainerID; got != 48 {
		t.Errorf("ContainerTradeTwoIngredientTwo = %d, want 48", got)
	}
	if got := trading.TwoResultContainerID; got != 49 {
		t.Errorf("ContainerTradeTwoResultPreview = %d, want 49", got)
	}
}

// TestTradeWindowTypeIsFifteen pins the window type the server opens a villager
// trade UI with. packet.UpdateTrade's own comment says vanilla always sends 15,
// and protocol.ContainerTypeTrade is the matching constant.
func TestTradeWindowTypeIsFifteen(t *testing.T) {
	t.Parallel()

	if got := trading.TradeWindowType; got != 15 {
		t.Errorf("TradeWindowType = %d, want 15", got)
	}
	if got := trading.TradeWindowType; int(got) != protocol.ContainerTypeTrade {
		t.Errorf("TradeWindowType = %d, want protocol.ContainerTypeTrade (%d)", got, protocol.ContainerTypeTrade)
	}
}

// TestContainerIDForSlotMapsTheThreeTradeSlots pins the window-slot to
// container-ID mapping. Bedrock numbers a trade window 0 = first input,
// 1 = second input, 2 = the result; each of those is a different container, so
// the mapping is a lookup rather than an offset anyone can do in their head.
func TestContainerIDForSlotMapsTheThreeTradeSlots(t *testing.T) {
	t.Parallel()

	cases := []struct {
		slot     uint32
		wantID   byte
		wantSlot bool
	}{
		{trading.SlotIngredientOne, trading.IngredientOneContainerID, true},
		{trading.SlotIngredientTwo, trading.IngredientTwoContainerID, true},
		{trading.SlotResult, trading.ResultContainerID, true},
	}
	for _, tc := range cases {
		gotID, gotOK := trading.ContainerIDForSlot(tc.slot)
		if gotOK != tc.wantSlot {
			t.Fatalf("ContainerIDForSlot(%d) ok = %v, want %v", tc.slot, gotOK, tc.wantSlot)
		}
		if gotID != tc.wantID {
			t.Errorf("ContainerIDForSlot(%d) = %d, want %d", tc.slot, gotID, tc.wantID)
		}
	}
}

// TestContainerIDForSlotRefusesSlotsOutsideTheTradeWindow is the guard. Handing
// out a container ID for a slot the trade window does not have is how a
// transfer gets addressed somewhere the server never looks.
func TestContainerIDForSlotRefusesSlotsOutsideTheTradeWindow(t *testing.T) {
	t.Parallel()

	for _, slot := range []uint32{trading.TradeWindowSlots, 3, 4, 99} {
		if id, ok := trading.ContainerIDForSlot(slot); ok {
			t.Errorf("ContainerIDForSlot(%d) = %d, ok; the trade window has three slots", slot, id)
		}
	}
}

// TestTradeSlotCountIsThree guards the layout itself. A fourth slot appearing in
// the window layout without a matching container ID is the first sign of the
// old trade UI, which has a different shape.
func TestTradeSlotCountIsThree(t *testing.T) {
	t.Parallel()

	if trading.TradeWindowSlots != 3 {
		t.Errorf("TradeWindowSlots = %d, want 3", trading.TradeWindowSlots)
	}
}
