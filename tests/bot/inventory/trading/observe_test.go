package trading_test

import (
	"testing"

	"bedrock-ai/internal/bot/inventory/trading"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// TestTradeWindowFromPacketCopiesTheServersWindow is the wiring test. This is
// the one conversion between the wire and the manager, and a field left out of
// it is a field the trade logic never sees.
func TestTradeWindowFromPacketCopiesTheServersWindow(t *testing.T) {
	t.Parallel()

	pk := &packet.UpdateTrade{
		WindowID:          42,
		WindowType:        trading.TradeWindowType,
		Size:              2,
		TradeTier:         3,
		VillagerUniqueID:  900,
		EntityUniqueID:    901,
		DisplayName:       "minecraft:butcher",
		NewTradeUI:        true,
		DemandBasedPrices: true,
		SerialisedOffers: serialisedWith(t, map[string]any{
			"Offers": []any{
				map[string]any{
					"buyItem":  item("minecraft:emerald", 3),
					"sellItem": item("minecraft:cooked_beef", 7),
					"maxUses":  int32(12),
					"uses":     int32(5),
				},
				map[string]any{
					"buyItem":  item("minecraft:emerald", 1),
					"sellItem": item("minecraft:leather", 2),
				},
			},
		}),
	}

	window, err := trading.TradeWindowFromPacket(pk)
	if err != nil {
		t.Fatalf("TradeWindowFromPacket: %v", err)
	}

	if window.WindowID != 42 {
		t.Errorf("WindowID = %d, want 42", window.WindowID)
	}
	if window.WindowType != trading.TradeWindowType {
		t.Errorf("WindowType = %d, want %d", window.WindowType, trading.TradeWindowType)
	}
	if window.TradeTier != 3 {
		t.Errorf("TradeTier = %d, want 3", window.TradeTier)
	}
	if window.OfferCount != 2 {
		t.Errorf("OfferCount = %d, want 2", window.OfferCount)
	}
	if window.VillagerUniqueID != 900 || window.EntityUniqueID != 901 {
		t.Errorf("entity IDs = %d/%d, want 900/901", window.VillagerUniqueID, window.EntityUniqueID)
	}
	if window.DisplayName != "minecraft:butcher" {
		t.Errorf("DisplayName = %q, want minecraft:butcher", window.DisplayName)
	}
	if !window.NewTradeUI || !window.DemandBasedPrices {
		t.Errorf("NewTradeUI/DemandBasedPrices = %v/%v, want true/true", window.NewTradeUI, window.DemandBasedPrices)
	}

	if len(window.Offers) != 2 {
		t.Fatalf("Offers = %d, want the 2 the server sent", len(window.Offers))
	}
	if window.Offers[0].Output.Name != "minecraft:cooked_beef" || window.Offers[0].Output.Count != 7 {
		t.Errorf("first offer = %+v, want 7 minecraft:cooked_beef", window.Offers[0])
	}
	if window.Offers[0].Uses != 5 || window.Offers[0].MaxUses != 12 {
		t.Errorf("first offer uses = %d/%d, want 5/12", window.Offers[0].Uses, window.Offers[0].MaxUses)
	}
	if window.Offers[1].Output.Name != "minecraft:leather" {
		t.Errorf("second offer = %+v, want the leather offer", window.Offers[1])
	}
}

// TestTradeWindowFromPacketKeepsTheWindowWhenOffersAreUnreadable. Everything
// but the trade table still arrived and is worth keeping: dropping the whole
// packet loses the window ID and the tier, which are the parts that can be
// diagnosed at all.
func TestTradeWindowFromPacketKeepsTheWindowWhenOffersAreUnreadable(t *testing.T) {
	t.Parallel()

	pk := &packet.UpdateTrade{
		WindowID:         42,
		TradeTier:        1,
		DisplayName:      "minecraft:leatherworker",
		SerialisedOffers: []byte("this is not nbt"),
	}

	window, err := trading.TradeWindowFromPacket(pk)
	if err == nil {
		t.Fatal("TradeWindowFromPacket accepted an unreadable offers blob")
	}
	if len(window.Offers) != 0 {
		t.Errorf("Offers = %+v from an unreadable blob; they must be empty", window.Offers)
	}
	if window.WindowID != 42 || window.TradeTier != 1 {
		t.Errorf("window = %+v; what did arrive should still be recorded", window)
	}
	if len(window.SerialisedOffers) == 0 {
		t.Error("the raw blob was dropped, so a decode failure cannot be diagnosed")
	}
}

// TestTradeWindowFromPacketRejectsAnEmptyBlob. No blob at all is different from
// an empty villager, and only the second one means "nothing on offer".
func TestTradeWindowFromPacketRejectsAnEmptyBlob(t *testing.T) {
	t.Parallel()

	window, err := trading.TradeWindowFromPacket(&packet.UpdateTrade{WindowID: 5})
	if err == nil {
		t.Fatal("TradeWindowFromPacket accepted an UpdateTrade with no offers blob")
	}
	if window.WindowID != 5 {
		t.Errorf("WindowID = %d, want the 5 that did arrive", window.WindowID)
	}
}

// TestTradeWindowFromPacketHandlesNil rather than panicking on a nil packet.
func TestTradeWindowFromPacketHandlesNil(t *testing.T) {
	t.Parallel()

	if _, err := trading.TradeWindowFromPacket(nil); err == nil {
		t.Fatal("TradeWindowFromPacket(nil) returned no error")
	}
}

// TestTradeWindowFromPacketCopiesTheBlob guards against aliasing the packet's
// own buffer: the network goroutine reuses its read buffers, so a window that
// pointed at them would decode to something else by the time it is used.
func TestTradeWindowFromPacketCopiesTheBlob(t *testing.T) {
	t.Parallel()

	blob := serialisedWith(t, map[string]any{
		"Offers": []any{
			map[string]any{"buyItem": item("minecraft:emerald", 1), "sellItem": item("minecraft:bread", 8)},
		},
	})
	pk := &packet.UpdateTrade{WindowID: 1, SerialisedOffers: blob}

	window, err := trading.TradeWindowFromPacket(pk)
	if err != nil {
		t.Fatalf("TradeWindowFromPacket: %v", err)
	}
	for i := range blob {
		blob[i] = 0
	}
	if len(window.Offers) != 1 {
		t.Errorf("Offers = %+v after the source blob was overwritten; the window aliased it", window.Offers)
	}
}
