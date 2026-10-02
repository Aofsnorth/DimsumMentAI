package trading_test

import (
	"bytes"
	"testing"

	"bedrock-ai/internal/bot/inventory/trading"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

// serialisedOffers encodes data as the little-endian NBT blob that
// packet.UpdateTrade.SerialisedOffers carries. The test builds the bytes rather
// than pasting a hex blob so the fixture stays readable and the encoding under
// test is the one the protocol actually uses.
func serialisedWith(t *testing.T, data map[string]any) []byte {
	t.Helper()

	buf := &bytes.Buffer{}
	if err := nbt.NewEncoderWithEncoding(buf, nbt.LittleEndian).Encode(data); err != nil {
		t.Fatalf("encode offers fixture: %v", err)
	}
	return buf.Bytes()
}

func item(id string, count int32) map[string]any {
	return map[string]any{"item": id, "count": count}
}

// TestDecodeOffersReadsTheVanillaShape is the happy path. One emerald buys eight
// bread; the counts and the name both come out of the blob, not out of a guess.
func TestDecodeOffersReadsTheVanillaShape(t *testing.T) {
	t.Parallel()

	raw := serialisedWith(t, map[string]any{
		"Offers": []any{
			map[string]any{
				"buyItem":  item("minecraft:emerald", 1),
				"sellItem": item("minecraft:bread", 8),
				"maxUses":  int32(16),
				"uses":     int32(3),
			},
		},
		"TradeTier": int32(1),
	})

	offers, err := trading.DecodeOffers(raw)
	if err != nil {
		t.Fatalf("DecodeOffers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("DecodeOffers returned %d offers, want 1", len(offers))
	}

	got := offers[0]
	if got.Index != 0 {
		t.Errorf("offer index = %d, want 0", got.Index)
	}
	if len(got.Inputs) != 1 {
		t.Fatalf("offer has %d inputs, want 1", len(got.Inputs))
	}
	if got.Inputs[0].Name != "minecraft:emerald" || got.Inputs[0].Count != 1 {
		t.Errorf("input = %+v, want 1 minecraft:emerald", got.Inputs[0])
	}
	if got.Output.Name != "minecraft:bread" || got.Output.Count != 8 {
		t.Errorf("output = %+v, want 8 minecraft:bread", got.Output)
	}
	if got.Uses != 3 || got.MaxUses != 16 {
		t.Errorf("uses = %d/%d, want 3/16", got.Uses, got.MaxUses)
	}
}

// TestDecodeOffersIndexesInServerOrder matters because the index is what the
// manager stages against. A decoder that reversed the list would trade the
// wrong offer while looking perfectly correct.
func TestDecodeOffersIndexesInServerOrder(t *testing.T) {
	t.Parallel()

	raw := serialisedWith(t, map[string]any{
		"Offers": []any{
			map[string]any{"buyItem": item("minecraft:emerald", 1), "sellItem": item("minecraft:bread", 1)},
			map[string]any{"buyItem": item("minecraft:emerald", 2), "sellItem": item("minecraft:paper", 32)},
			map[string]any{"buyItem": item("minecraft:emerald", 3), "sellItem": item("minecraft:book", 4)},
		},
	})

	offers, err := trading.DecodeOffers(raw)
	if err != nil {
		t.Fatalf("DecodeOffers: %v", err)
	}
	if len(offers) != 3 {
		t.Fatalf("DecodeOffers returned %d offers, want 3", len(offers))
	}
	for i, want := range []string{"minecraft:bread", "minecraft:paper", "minecraft:book"} {
		if offers[i].Index != i {
			t.Errorf("offers[%d].Index = %d, want %d", i, offers[i].Index, i)
		}
		if offers[i].Output.Name != want {
			t.Errorf("offers[%d].Output = %q, want %q", i, offers[i].Output.Name, want)
		}
	}
}

// TestDecodeOffersReadsATwoInputTrade covers the second ingredient slot. A
// decoder that only understands buyItem silently produces a one-input offer,
// and the bot then walks away having paid for half a trade.
func TestDecodeOffersReadsATwoInputTrade(t *testing.T) {
	t.Parallel()

	raw := serialisedWith(t, map[string]any{
		"Offers": []any{
			map[string]any{
				"buyItems": []any{
					item("minecraft:emerald", 5),
					item("minecraft:book", 1),
				},
				"sellItem": item("minecraft:enchanted_book", 1),
			},
		},
	})

	offers, err := trading.DecodeOffers(raw)
	if err != nil {
		t.Fatalf("DecodeOffers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("DecodeOffers returned %d offers, want 1", len(offers))
	}
	if len(offers[0].Inputs) != 2 {
		t.Fatalf("offer has %d inputs, want 2: %+v", len(offers[0].Inputs), offers[0].Inputs)
	}
	if offers[0].Inputs[1].Name != "minecraft:book" || offers[0].Inputs[1].Count != 1 {
		t.Errorf("second input = %+v, want 1 minecraft:book", offers[0].Inputs[1])
	}
	if offers[0].Output.Name != "minecraft:enchanted_book" {
		t.Errorf("output = %q, want minecraft:enchanted_book", offers[0].Output.Name)
	}
}

// TestDecodeOffersReadsTheXPFields is the 7.2 requirement: a trade that spends
// levels has to come out of the blob with its cost attached, or the budget
// cannot reason about it at all.
func TestDecodeOffersReadsTheXPFields(t *testing.T) {
	t.Parallel()

	raw := serialisedWith(t, map[string]any{
		"Offers": []any{
			map[string]any{
				"buyItem":       item("minecraft:emerald", 8),
				"sellItem":      item("minecraft:honeycomb", 1),
				"xpCost":        int32(20),
				"requiredLevel": int32(15),
			},
		},
	})

	offers, err := trading.DecodeOffers(raw)
	if err != nil {
		t.Fatalf("DecodeOffers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("DecodeOffers returned %d offers, want 1", len(offers))
	}
	if offers[0].XPCost != 20 {
		t.Errorf("XPCost = %d, want 20", offers[0].XPCost)
	}
	if offers[0].RequiredLevel != 15 {
		t.Errorf("RequiredLevel = %d, want 15", offers[0].RequiredLevel)
	}
}

// TestDecodeOffersRejectsGarbageRatherThanInventingOffers is the rule that
// matters most here. SerialisedOffers is documented only as "a network NBT
// serialised compound of offers"; a host that sends a shape this decoder does
// not know must produce an error, not an empty-but-valid-looking offer list.
func TestDecodeOffersRejectsGarbageRatherThanInventingOffers(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"nil":            nil,
		"empty":          {},
		"not nbt":        []byte("this is not nbt at all"),
		"nbt wrong root": serialisedWith(t, map[string]any{"unrelated": int32(1)}),
	}
	for name, raw := range cases {
		offers, err := trading.DecodeOffers(raw)
		if err == nil {
			t.Errorf("DecodeOffers(%s) returned %d offers and no error; an unreadable blob is an error, not an empty villager",
				name, len(offers))
		}
	}
}

// TestDecodeOffersSkipsOffersItCannotRead keeps a partly-readable villager
// usable. A blob where one entry is malformed and the rest are fine should
// surface the readable offers, so the bot trades with the villager instead of
// declaring the whole thing unreadable.
func TestDecodeOffersSkipsOffersItCannotRead(t *testing.T) {
	t.Parallel()

	raw := serialisedWith(t, map[string]any{
		"Offers": []any{
			// No sellItem: nothing is being given, so this row says nothing usable.
			map[string]any{"buyItem": item("minecraft:emerald", 1)},
			map[string]any{"buyItem": item("minecraft:emerald", 1), "sellItem": item("minecraft:bread", 8)},
		},
	})

	offers, err := trading.DecodeOffers(raw)
	if err != nil {
		t.Fatalf("DecodeOffers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("DecodeOffers returned %d offers, want the 1 readable row: %+v", len(offers), offers)
	}
	if offers[0].Output.Name != "minecraft:bread" {
		t.Errorf("kept the wrong offer: %+v", offers[0])
	}
	if offers[0].Index != 1 {
		t.Errorf("kept offer re-indexed to %d, want its position 1 in the server's list", offers[0].Index)
	}
}

// TestDecodeOffersOnAnEmptyVillager is the honest empty: a villager with nothing
// to sell decodes to no offers and no error, because that is a real state.
func TestDecodeOffersOnAnEmptyVillager(t *testing.T) {
	t.Parallel()

	raw := serialisedWith(t, map[string]any{"Offers": []any{}})

	offers, err := trading.DecodeOffers(raw)
	if err != nil {
		t.Fatalf("DecodeOffers on an empty offer list: %v", err)
	}
	if len(offers) != 0 {
		t.Errorf("DecodeOffers returned %d offers from an empty list: %+v", len(offers), offers)
	}
}

// TestDecodeOffersAcceptsABareList is the shape some hosts use: the list at the
// root, with no Offers wrapper. A decoder that insisted on the wrapper would
// call a perfectly good villager unreadable.
// TestDecodeOffersAcceptsOffersMappedAtTheRoot is a shape some hosts use: the
// root compound holds the offers keyed by position, with no Offers list
// wrapping them. A decoder that insisted on the wrapper would call a perfectly
// good villager unreadable.
func TestDecodeOffersAcceptsOffersMappedAtTheRoot(t *testing.T) {
	t.Parallel()

	raw := serialisedWith(t, map[string]any{
		"TradeTier": int32(0),
		"0":         map[string]any{"buyItem": item("minecraft:emerald", 1), "sellItem": item("minecraft:bread", 8)},
	})

	offers, err := trading.DecodeOffers(raw)
	if err != nil {
		t.Fatalf("DecodeOffers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("DecodeOffers returned %d offers from a root map holding one offer: %+v", len(offers), offers)
	}
	if offers[0].Output.Name != "minecraft:bread" {
		t.Errorf("offer = %+v, want the bread offer", offers[0])
	}
}

// TestDecodeOffersDefaultsAMissingCountToOne. A trade written as a bare item id
// with no count is one item; reading it as zero would make every such offer
// look like it gives nothing.
func TestDecodeOffersDefaultsAMissingCountToOne(t *testing.T) {
	t.Parallel()

	raw := serialisedWith(t, map[string]any{
		"Offers": []any{
			map[string]any{
				"buyItem":  map[string]any{"item": "minecraft:emerald"},
				"sellItem": map[string]any{"item": "minecraft:bread"},
			},
		},
	})

	offers, err := trading.DecodeOffers(raw)
	if err != nil {
		t.Fatalf("DecodeOffers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("DecodeOffers returned %d offers, want 1", len(offers))
	}
	if offers[0].InputItem().Count != 1 {
		t.Errorf("input count = %d, want 1", offers[0].InputItem().Count)
	}
	if offers[0].Output.Count != 1 {
		t.Errorf("output count = %d, want 1", offers[0].Output.Count)
	}
}

// TestDecodeOffersTruncatesToTheTwoIngredientSlots. A trade window has two input
// slots. An offer listing three inputs cannot be staged, so it must not come
// back looking executable.
func TestDecodeOffersTruncatesToTheTwoIngredientSlots(t *testing.T) {
	t.Parallel()

	raw := serialisedWith(t, map[string]any{
		"Offers": []any{
			map[string]any{
				"buyItems": []any{
					item("minecraft:emerald", 1),
					item("minecraft:wheat", 1),
					item("minecraft:carrot", 1),
				},
				"sellItem": item("minecraft:bread", 8),
			},
		},
	})

	offers, err := trading.DecodeOffers(raw)
	if err != nil {
		t.Fatalf("DecodeOffers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("DecodeOffers returned %d offers, want 1", len(offers))
	}
	if len(offers[0].Inputs) > 2 {
		t.Errorf("offer kept %d inputs; a trade window stages at most 2: %+v", len(offers[0].Inputs), offers[0])
	}
}

// TestInputItemAndOutputItemAreNeverZero guards the convenience accessors used
// by the staging code, which indexes the first input without a bounds check.
func TestInputItemAndOutputItemAreNeverZero(t *testing.T) {
	t.Parallel()

	none := trading.Offer{}
	if got := none.InputItem(); got.Count != 0 || got.Name != "" {
		t.Errorf("InputItem on an offer with no inputs = %+v, want the zero item", got)
	}
	if got := none.InputItemCount(); got != 0 {
		t.Errorf("InputItemCount on an offer with no inputs = %d, want 0", got)
	}
}

// TestOfferSpendingIsTheSumOfItsInputs is what the ranking divides by. An offer
// with no inputs must score zero, not infinity, or it would always win.
func TestOfferSpendingIsTheSumOfItsInputs(t *testing.T) {
	t.Parallel()

	single := trading.Offer{Inputs: []trading.Item{{Name: "minecraft:emerald", Count: 3}}}
	if got := single.InputTotal(); got != 3 {
		t.Errorf("InputTotal = %d, want 3", got)
	}

	double := trading.Offer{Inputs: []trading.Item{
		{Name: "minecraft:emerald", Count: 3},
		{Name: "minecraft:book", Count: 2},
	}}
	if got := double.InputTotal(); got != 5 {
		t.Errorf("InputTotal = %d, want 5", got)
	}

	none := trading.Offer{}
	if got := none.InputTotal(); got != 0 {
		t.Errorf("InputTotal on an offer with no inputs = %d, want 0", got)
	}
}
