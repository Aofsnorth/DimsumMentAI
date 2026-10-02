package trading_test

import (
	"fmt"
	"testing"

	"bedrock-ai/internal/bot/inventory/trading"
)

// emeraldOffer is the shape nearly every villager trade has: one input, one
// output. Tests build variations of it rather than a fresh literal each time so
// a change to the Offer shape is one edit, not thirty.
func emeraldOffer(count int) trading.Offer {
	return trading.Offer{
		Inputs:  []trading.Item{{Name: "minecraft:emerald", Count: count}},
		Output:  trading.Item{Name: "minecraft:bread", Count: 8},
		MaxUses: 16,
	}
}

func affordableBudget(have map[string]int) trading.Budget {
	return trading.Budget{Have: have}
}

// TestPickOfferNeverInventsAnOffer is the rule the whole package hangs on. With
// no offers observed there is nothing to trade, and the answer is "no" — never a
// plausible-looking default that the caller then reports as a completed trade.
func TestPickOfferNeverInventsAnOffer(t *testing.T) {
	t.Parallel()

	got, ok := trading.PickOffer(nil, affordableBudget(map[string]int{"emerald": 64}), nil)
	if ok {
		t.Errorf("PickOffer invented %+v from an empty offer list", got)
	}

	got, ok = trading.PickOffer([]trading.Offer{}, affordableBudget(nil), nil)
	if ok {
		t.Errorf("PickOffer invented %+v from an empty offer list", got)
	}
}

// TestPickOfferRefusesAnOfferItCannotAfford is the emerald gate. A villager the
// bot has nothing to pay with is not a trade, however good the deal.
func TestPickOfferRefusesAnOfferItCannotAfford(t *testing.T) {
	t.Parallel()

	offers := []trading.Offer{emeraldOffer(1)}
	budget := affordableBudget(map[string]int{"emerald": 0})

	if _, ok := trading.PickOffer(offers, budget, nil); ok {
		t.Error("PickOffer chose an offer the bot cannot pay for")
	}

	// One emerald short is still short: 3 emeralds cannot buy a 4-emerald
	// offer, and 4 buys it exactly.
	costly := emeraldOffer(4)
	if _, ok := trading.PickOffer([]trading.Offer{costly}, affordableBudget(map[string]int{"emerald": 3}), nil); ok {
		t.Error("PickOffer chose a 4-emerald offer with 3 emeralds")
	}
	if _, ok := trading.PickOffer([]trading.Offer{costly}, affordableBudget(map[string]int{"emerald": 4}), nil); !ok {
		t.Error("PickOffer refused a 4-emerald offer with exactly 4 emeralds")
	}
}

// TestPickOfferHonoursTheWantFilter is the point of the want predicate. A bot
// asked for a map must not come back with bread, even when the bread is the
// better deal by every other measure.
func TestPickOfferHonoursTheWantFilter(t *testing.T) {
	t.Parallel()

	bread := emeraldOffer(1)
	book := emeraldOffer(1)
	book.Output = trading.Item{Name: "minecraft:book", Count: 1}
	bread.Index, book.Index = 0, 1

	wantBook := func(o trading.Offer) bool { return trading.NormalizeItemName(o.Output.Name) == "book" }

	got, ok := trading.PickOffer([]trading.Offer{bread, book}, affordableBudget(map[string]int{"emerald": 64}), wantBook)
	if !ok {
		t.Fatal("PickOffer found nothing wanted, though a book offer was affordable")
	}
	if got.Output.Name != "minecraft:book" {
		t.Errorf("PickOffer returned %q; the want predicate only accepts a book", got.Output.Name)
	}
}

// TestPickOfferRefusesWhenNothingIsWanted keeps the filter honest in the other
// direction: an offer the bot cannot use is not a fallback.
func TestPickOfferRefusesWhenNothingIsWanted(t *testing.T) {
	t.Parallel()

	offers := []trading.Offer{emeraldOffer(1)}
	budget := affordableBudget(map[string]int{"emerald": 64})

	if _, ok := trading.PickOffer(offers, budget, func(trading.Offer) bool { return false }); ok {
		t.Error("PickOffer returned an offer the want predicate rejected")
	}
}

// TestPickOfferPrefersTheBetterDeal is the ranking rule. Output per input spent
// is what decides between two affordable offers; index is only the tie-break.
func TestPickOfferPrefersTheBetterDeal(t *testing.T) {
	t.Parallel()

	// Offer 0: 1 emerald for 1 bread. Offer 1: 1 emerald for 8 bread.
	poor := emeraldOffer(1)
	poor.Output = trading.Item{Name: "minecraft:bread", Count: 1}
	rich := emeraldOffer(1)
	rich.Output = trading.Item{Name: "minecraft:bread", Count: 8}
	poor.Index, rich.Index = 0, 1

	got, ok := trading.PickOffer([]trading.Offer{poor, rich}, affordableBudget(map[string]int{"emerald": 64}), nil)
	if !ok {
		t.Fatal("PickOffer found nothing affordable")
	}
	if got.Output.Count != 8 {
		t.Errorf("PickOffer chose the %d-bread offer over the 8-bread one", got.Output.Count)
	}

	// The expensive-but-generous offer wins on value even though the cheap one
	// is affordable too and comes first.
	bundle := emeraldOffer(4)
	bundle.Output = trading.Item{Name: "minecraft:bread", Count: 64}
	got, ok = trading.PickOffer([]trading.Offer{poor, bundle}, affordableBudget(map[string]int{"emerald": 64}), nil)
	if !ok {
		t.Fatal("PickOffer found nothing affordable")
	}
	if got.Output.Count != 64 {
		t.Errorf("PickOffer chose %d bread over the 64-bread offer", got.Output.Count)
	}
}

// TestPickOfferIsDeterministicOnTies. Two offers with identical value must not
// swap places between calls, or a test that passed once proves nothing.
func TestPickOfferIsDeterministicOnTies(t *testing.T) {
	t.Parallel()

	first := emeraldOffer(1)
	first.Index = 3
	second := emeraldOffer(1)
	second.Index = 7
	budget := affordableBudget(map[string]int{"emerald": 64})

	for i := 0; i < 20; i++ {
		got, ok := trading.PickOffer([]trading.Offer{first, second}, budget, nil)
		if !ok {
			t.Fatal("PickOffer found nothing affordable")
		}
		if got.Index != 3 {
			t.Fatalf("PickOffer chose index %d on call %d; ties break to the lowest index", got.Index, i)
		}
	}
}

// TestPickOfferIndexesTheOffersItWasGiven guards a subtle rewrite. Sorting a
// copy is fine; re-indexing one offer is not, because the index is what the
// caller stages and reports against.
func TestPickOfferIndexesTheOffersItWasGiven(t *testing.T) {
	t.Parallel()

	a := emeraldOffer(1)
	a.Output = trading.Item{Name: "minecraft:bread", Count: 1}
	a.Index = 5
	b := emeraldOffer(1)
	b.Output = trading.Item{Name: "minecraft:bread", Count: 1}
	b.Index = 9

	got, ok := trading.PickOffer([]trading.Offer{a, b}, affordableBudget(map[string]int{"emerald": 64}), nil)
	if !ok {
		t.Fatal("PickOffer found nothing affordable")
	}
	if got.Index != 5 || got.Index != a.Index {
		t.Errorf("PickOffer returned index %d, want the offer's own index %d", got.Index, a.Index)
	}
}

// TestCanAffordCountsEveryInput. A two-item trade needs both halves. Accepting
// the offer because the bot holds the first input is how emeralds get spent on
// nothing.
func TestCanAffordCountsEveryInput(t *testing.T) {
	t.Parallel()

	offer := trading.Offer{
		Inputs: []trading.Item{
			{Name: "minecraft:emerald", Count: 2},
			{Name: "minecraft:wheat", Count: 3},
		},
		Output: trading.Item{Name: "minecraft:bread", Count: 1},
	}

	if trading.CanAfford(offer, affordableBudget(map[string]int{"emerald": 10, "wheat": 2})) {
		t.Error("CanAfford accepted a trade with only 2 of 3 wheat")
	}
	if !trading.CanAfford(offer, affordableBudget(map[string]int{"emerald": 10, "wheat": 3})) {
		t.Error("CanAfford refused a trade with every input covered exactly")
	}
}

// TestCanAffordKeepsTheReserve back. The bot's emeralds are shared with every
// other plan; a trade that empties the purse is not affordable.
func TestCanAffordKeepsTheReserve(t *testing.T) {
	t.Parallel()

	offer := emeraldOffer(1)
	budget := trading.Budget{
		Have:    map[string]int{"emerald": 5},
		Reserve: map[string]int{"emerald": 5},
	}
	if trading.CanAfford(offer, budget) {
		t.Error("CanAfford spent emeralds that were reserved for something else")
	}

	budget.Reserve = map[string]int{"emerald": 4}
	if !trading.CanAfford(offer, budget) {
		t.Error("CanAfford refused a trade that stayed inside the reserve")
	}
}

// TestCanAffordSkipsASpentOutOffer. A villager whose last use is gone still
// lists the offer; picking it produces a refusal from the server.
func TestCanAffordSkipsASpentOutOffer(t *testing.T) {
	t.Parallel()

	budget := affordableBudget(map[string]int{"emerald": 64})

	spent := emeraldOffer(1)
	spent.Uses, spent.MaxUses = 16, 16
	if !spent.SpentOut() {
		t.Error("SpentOut = false for an offer at 16/16 uses")
	}
	if trading.CanAfford(spent, budget) {
		t.Error("CanAfford chose an offer the villager has no uses left for")
	}

	partial := emeraldOffer(1)
	partial.Uses, partial.MaxUses = 15, 16
	if partial.SpentOut() {
		t.Error("SpentOut = true for an offer with one use left")
	}
	if !trading.CanAfford(partial, budget) {
		t.Error("CanAfford refused an offer that still has a use left")
	}
}

// TestAnUnlimitedOfferIsNeverSpentOut guards the vanilla convention that a zero
// maxUses means "as many times as you like". Reading zero as "no uses left"
// makes every wandering-trader-style offer permanently unbuyable.
func TestAnUnlimitedOfferIsNeverSpentOut(t *testing.T) {
	t.Parallel()

	unlimited := emeraldOffer(1)
	unlimited.MaxUses = 0
	if unlimited.SpentOut() {
		t.Error("SpentOut = true for an unlimited offer (MaxUses 0)")
	}
	if !trading.CanAfford(unlimited, affordableBudget(map[string]int{"emerald": 64})) {
		t.Error("CanAfford refused an unlimited affordable offer")
	}
}

// TestCanAffordRefusesAnXPTradeWhenXPIsNotObserved is the honesty rule for XP.
// An unobserved level is not a level of zero, and it is certainly not enough
// for a trade that costs 25. Guessing here would spend emeralds on a trade the
// server is about to refuse.
func TestCanAffordRefusesAnXPTradeWhenXPIsNotObserved(t *testing.T) {
	t.Parallel()

	offer := emeraldOffer(1)
	offer.XPCost = 25
	offer.RequiredLevel = 20

	budget := trading.Budget{Have: map[string]int{"emerald": 64}, XPObserved: false}
	if trading.CanAfford(offer, budget) {
		t.Error("CanAfford accepted an XP-costing trade without being able to see the XP level")
	}
}

// TestCanAffordChecksTheXPLevelWhenItIsObserved is the other half: once the level
// is real it is actually used, in both directions.
func TestCanAffordChecksTheXPLevelWhenItIsObserved(t *testing.T) {
	t.Parallel()

	offer := emeraldOffer(1)
	offer.XPCost = 25
	offer.RequiredLevel = 20

	enough := trading.Budget{Have: map[string]int{"emerald": 64}, XPLevel: 30, XPObserved: true}
	if !trading.CanAfford(offer, enough) {
		t.Error("CanAfford refused an XP trade the bot's observed level covers")
	}

	poor := trading.Budget{Have: map[string]int{"emerald": 64}, XPLevel: 3, XPObserved: true}
	if trading.CanAfford(offer, poor) {
		t.Error("CanAfford accepted an XP trade with only 3 levels")
	}

	locked := trading.Budget{Have: map[string]int{"emerald": 64}, XPLevel: 19, XPObserved: true}
	if trading.CanAfford(offer, locked) {
		t.Error("CanAfford accepted a trade requiring level 20 from a level-19 bot")
	}
}

// TestAnXPFreeTradeIsAffordableWithoutXP stops the previous rule from becoming
// a blanket refusal: ordinary trades cost no levels and must still work on a
// bot whose XP level nothing observes.
func TestAnXPFreeTradeIsAffordableWithoutXP(t *testing.T) {
	t.Parallel()

	budget := trading.Budget{Have: map[string]int{"emerald": 64}, XPObserved: false}
	if !trading.CanAfford(emeraldOffer(1), budget) {
		t.Error("CanAfford refused a plain emerald trade because XP was unobserved")
	}
}

// TestAnOfferWithNoOutputIsNeverChosen. A malformed offer must not become a
// "successful" trade that hands over emeralds for air.
func TestAnOfferWithNoOutputIsNeverChosen(t *testing.T) {
	t.Parallel()

	empty := trading.Offer{Inputs: []trading.Item{{Name: "minecraft:emerald", Count: 1}}}
	budget := affordableBudget(map[string]int{"emerald": 64})

	if trading.CanAfford(empty, budget) {
		t.Error("CanAfford accepted an offer with no output item")
	}
	if _, ok := trading.PickOffer([]trading.Offer{empty}, budget, nil); ok {
		t.Error("PickOffer chose an offer with no output item")
	}
}

// TestAnOfferWithNoInputIsNeverChosen is the mirror: a free offer would drain
// the villager's uses without the bot paying anything.
func TestAnOfferWithNoInputIsNeverChosen(t *testing.T) {
	t.Parallel()

	giveaway := trading.Offer{Output: trading.Item{Name: "minecraft:diamond", Count: 1}}
	budget := affordableBudget(map[string]int{"emerald": 64})

	if trading.CanAfford(giveaway, budget) {
		t.Error("CanAfford accepted an offer with no input item")
	}
}

// TestNormalizeItemName matches the way the rest of the codebase names items:
// lower case, no namespace, underscores for spaces.
func TestNormalizeItemName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"minecraft:emerald":        "emerald",
		"Emerald":                  "emerald",
		"  minecraft:Bread  ":      "bread",
		"minecraft:cooked_beef":    "cooked_beef",
		"minecraft:enchanted book": "enchanted_book",
		"":                         "",
	}
	for in, want := range cases {
		if got := trading.NormalizeItemName(in); got != want {
			t.Errorf("NormalizeItemName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNormalizeItemNameIsIdempotent keeps a normalised name usable as a map key
// against a name that was normalised twice — which is exactly what happens when
// a decoder normalises and a budget builder normalises again.
func TestNormalizeItemNameIsIdempotent(t *testing.T) {
	t.Parallel()

	once := trading.NormalizeItemName("minecraft:Cooked Beef")
	if twice := trading.NormalizeItemName(once); twice != once {
		t.Errorf("NormalizeItemName is not idempotent: %q then %q", once, twice)
	}
}

// TestPickOfferIsPureAcrossCalls. The decision layer takes a snapshot and gives
// an answer; it does not consume the budget as it goes, so the same inputs
// always give the same offer.
func TestPickOfferIsPureAcrossCalls(t *testing.T) {
	t.Parallel()

	offers := []trading.Offer{emeraldOffer(1)}
	budget := affordableBudget(map[string]int{"emerald": 64})

	first, _ := trading.PickOffer(offers, budget, nil)
	for i := 0; i < 5; i++ {
		got, ok := trading.PickOffer(offers, budget, nil)
		if !ok {
			t.Fatal("PickOffer stopped finding the offer after a previous call")
		}
		if fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("PickOffer is stateful: call %d returned %+v, first returned %+v", i, got, first)
		}
	}
	if budget.Have["emerald"] != 64 {
		t.Errorf("PickOffer mutated the caller's budget: emeralds now %d", budget.Have["emerald"])
	}
}
