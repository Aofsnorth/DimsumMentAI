package trading

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

// Decoding packet.UpdateTrade.SerialisedOffers.
//
// The packet documents that field only as "a network NBT serialised compound of
// offers that the villager has" — the wire format is the vendor's own and
// changes between game versions. DecodeOffers therefore understands the shape
// vanilla writes and refuses everything else. It never guesses: a blob it
// cannot read is an error, because a plausible-looking default offer list is
// how a bot ends up handing emeralds to a villager for something it never
// offered.
//
// An offer is understood when it carries a sell item (what the villager gives)
// and at least one buy item (what it wants). Rows missing either are dropped
// and the readable ones are still returned, so one malformed entry does not
// make a whole villager unreadable.

// Keys the vanilla trade table has used for the same piece of an offer. More
// than one spelling appears across versions, so all of them are read and the
// first that is present wins.
var (
	offerListKeys   = []string{"Offers", "offers"}
	offerBuyListKey = []string{"buyItems", "BuyItems"}
	offerBuyKey     = []string{"buyItem", "BuyItem"}
	offerSellKey    = []string{"sellItem", "SellItem"}
	offerUsesKey    = []string{"uses", "Uses"}
	offerMaxUsesKey = []string{"maxUses", "MaxUses", "maxCount", "MaxCount"}
	offerXPCostKey  = []string{"xpCost", "XpCost", "xp_cost"}
	offerXPLevKey   = []string{"xp", "Xp", "xp_cost_level"}
	offerReqLvlKey  = []string{"requiredLevel", "RequiredLevel", "xpLevel", "level"}

	itemIDKey    = []string{"item", "Item", "id", "Id"}
	itemCountKey = []string{"count", "Count", "amount"}
)

// ErrNoOffersBlob says the server sent nothing to read. It is deliberately not
// nil: "no blob" and "a villager offering nothing" are different states, and
// only the second one means the villager is genuinely empty-handed.
var ErrNoOffersBlob = errors.New("update trade carried no offers blob")

// DecodeOffers reads the serialised trade table out of an UpdateTrade packet.
//
// It returns an error for anything it cannot read, and an empty slice with no
// error for a villager that really has nothing on sale — the one honest zero.
func DecodeOffers(serialised []byte) ([]Offer, error) {
	if len(serialised) == 0 {
		return nil, ErrNoOffersBlob
	}

	var root map[string]any
	if err := nbt.NewDecoderWithEncoding(bytes.NewReader(serialised), nbt.LittleEndian).Decode(&root); err != nil {
		return nil, fmt.Errorf("decode serialised offers: %w", err)
	}

	rows, err := offerRows(root)
	if err != nil {
		return nil, err
	}

	offers := make([]Offer, 0, len(rows))
	for index, row := range rows {
		offer, ok := decodeOffer(index, row)
		if !ok {
			continue
		}
		offers = append(offers, offer)
	}
	return offers, nil
}

// offerRows finds the list of offer compounds. The usual shape is a compound
// with an Offers list; some hosts put the list at the root or map offer
// compounds by index, so those are read too rather than being called unreadable.
func offerRows(root map[string]any) ([]map[string]any, error) {
	for _, key := range offerListKeys {
		switch value := root[key].(type) {
		case []any:
			return compoundsOf(value), nil
		case map[string]any:
			return []map[string]any{value}, nil
		}
	}

	// No list key. The root may itself be one offer, or a map of offers keyed by
	// position. Anything that does not look like an offer is not one.
	rows := make([]map[string]any, 0, len(root))
	for _, key := range sortedOfferKeys(root) {
		row, ok := root[key].(map[string]any)
		if !ok || !looksLikeOffer(row) {
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, errors.New("serialised offers carried no offer list this decoder recognises")
	}
	return rows, nil
}

func compoundsOf(list []any) []map[string]any {
	rows := make([]map[string]any, 0, len(list))
	for _, entry := range list {
		if row, ok := entry.(map[string]any); ok {
			rows = append(rows, row)
		}
	}
	return rows
}

func looksLikeOffer(row map[string]any) bool {
	if item, ok := firstCompound(row, offerSellKey); ok {
		return itemID(item) != ""
	}
	return false
}

// decodeOffer turns one compound into an Offer. The second return is false for
// a row that says nothing usable — no output, or no input to pay with.
func decodeOffer(index int, row map[string]any) (Offer, bool) {
	output, ok := firstCompound(row, offerSellKey)
	if !ok {
		return Offer{}, false
	}
	outputItem, ok := decodeItem(output)
	if !ok {
		return Offer{}, false
	}

	inputs := decodeInputs(row)
	if len(inputs) == 0 {
		return Offer{}, false
	}
	if uint32(len(inputs)) > TradeWindowSlots-1 {
		// A trade window has two input slots. An offer needing three cannot be
		// staged, so keeping it would only produce an offer that looks
		// affordable and then fails at the window.
		inputs = inputs[:TradeWindowSlots-1]
	}

	offer := Offer{
		Index:         index,
		Inputs:        inputs,
		Output:        outputItem,
		Uses:          int(nbtInt(firstValue(row, offerUsesKey))),
		MaxUses:       int(nbtInt(firstValue(row, offerMaxUsesKey))),
		RequiredLevel: int32(nbtInt(firstValue(row, offerReqLvlKey))),
	}
	// xpCost is unambiguous. xp is only read when xpCost is absent, and there the
	// meaning is host-dependent — on some it is what the trade grants rather
	// than costs. Reading it as a cost makes the bot refuse the trade, which is
	// the safe direction: a missed trade beats a spent trade the server refuses.
	offer.XPCost = int32(nbtInt(firstValue(row, offerXPCostKey)))
	if offer.XPCost == 0 {
		offer.XPCost = int32(nbtInt(firstValue(row, offerXPLevKey)))
	}
	return offer, true
}

func decodeInputs(row map[string]any) []Item {
	var inputs []Item
	for _, key := range offerBuyListKey {
		list, ok := row[key].([]any)
		if !ok {
			continue
		}
		for _, entry := range list {
			if compound, ok := entry.(map[string]any); ok {
				if item, ok := decodeItem(compound); ok {
					inputs = append(inputs, item)
				}
			}
		}
	}
	if len(inputs) > 0 {
		return inputs
	}

	if compound, ok := firstCompound(row, offerBuyKey); ok {
		if item, ok := decodeItem(compound); ok {
			return []Item{item}
		}
	}
	return nil
}

// decodeItem reads one item compound. A bare item id with no count is one item,
// which is how the format writes a single unit; reading it as zero would make
// such an offer look like it gives nothing.
func decodeItem(compound map[string]any) (Item, bool) {
	name := itemID(compound)
	if name == "" {
		return Item{}, false
	}
	count := int(nbtInt(firstValue(compound, itemCountKey)))
	if count <= 0 {
		count = 1
	}
	return Item{Name: name, Count: count}, true
}

func itemID(compound map[string]any) string {
	value, ok := firstValue(compound, itemIDKey).(string)
	if !ok {
		return ""
	}
	return value
}

func firstValue(m map[string]any, keys []string) any {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			return value
		}
	}
	return nil
}

func firstCompound(m map[string]any, keys []string) (map[string]any, bool) {
	for _, key := range keys {
		if value, ok := m[key].(map[string]any); ok {
			return value, true
		}
	}
	return nil, false
}

// nbtInt reads any of the numeric NBT tag types. The same field arrives as
// int32 from one build and int64 from another, and treating an unreadable value
// as zero is how an offer silently loses its use count.
func nbtInt(value any) int64 {
	switch v := value.(type) {
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case uint64:
		return int64(v)
	case float32:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}
