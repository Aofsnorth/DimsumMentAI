package trading

import (
	"sort"
	"strings"
)

// Item is one stack named by an offer or held by the bot. Name is kept exactly
// as it arrived — a namespaced identifier where that is what the server sent —
// so a failure message can be traced back to the wire.
type Item struct {
	Name  string
	Count int
}

// Offer is one row of a villager's trade list, as the server described it.
//
// Nothing here is invented locally. Every field comes either from the decoded
// packet.UpdateTrade blob or from the trade window the server then opened; a
// manager that has neither reports that it cannot trade rather than filling
// these in with plausible values.
type Offer struct {
	// Index is this row's position in the server's list. It is what the manager
	// stages and reports against, so it is the position the server gave it and
	// never a re-numbering of a filtered slice.
	Index int

	// Inputs are the items the villager wants, first input first. A trade window
	// has two input slots, so a longer list is truncated at decode time rather
	// than becoming an offer that cannot be staged.
	Inputs []Item

	// Output is what the villager hands back once the inputs match.
	Output Item

	// Uses is how many times this row has been traded already, and MaxUses how
	// many times it may be in total. MaxUses 0 means unlimited, which is the
	// vanilla convention and not "no uses left".
	Uses    int
	MaxUses int

	// XPCost is the number of experience levels this offer consumes, and
	// RequiredLevel the level the villager's tier needs before the offer is
	// even shown. Both are zero for an ordinary trade.
	XPCost        int32
	RequiredLevel int32
}

// SpentOut reports whether the villager has no uses of this offer left. It is
// the guard against picking a row the server is about to refuse.
func (o Offer) SpentOut() bool {
	return o.MaxUses > 0 && o.Uses >= o.MaxUses
}

// InputItem returns the first input, or the zero Item when there is none. The
// staging code indexes the first input without a bounds check on every call, so
// this is the accessor that keeps that safe.
func (o Offer) InputItem() Item {
	if len(o.Inputs) == 0 {
		return Item{}
	}
	return o.Inputs[0]
}

// InputItemCount is the first input's count, or zero.
func (o Offer) InputItemCount() int {
	return o.InputItem().Count
}

// InputTotal is how many items the offer consumes in all, across both inputs.
// It is the denominator of the value ranking, and zero for an offer with no
// inputs — which is why such an offer can never be ranked, and CanAfford
// rejects it first.
func (o Offer) InputTotal() int {
	total := 0
	for _, in := range o.Inputs {
		total += in.Count
	}
	return total
}

// WantsLevel reports whether this offer costs experience levels at all. An
// ordinary trade does not, and must stay tradeable on a bot whose level nothing
// observes.
func (o Offer) WantsLevel() bool {
	return o.XPCost > 0 || o.RequiredLevel > 0
}

// Budget is what the bot can actually pay with at the moment of the decision.
//
// Have and Reserve are keyed by normalised item name, because the runtime
// names the bot is given carry a namespace and an underscore convention that
// varies per host. XPLevel is only read when XPObserved is true: an unobserved
// level is not a level of zero, and treating it as one is how a bot spends
// emeralds on a trade the server is about to refuse.
type Budget struct {
	// Have is how many of each item the bot is carrying.
	Have map[string]int

	// Reserve is how many of each item must stay put for something else. A trade
	// that would take the last emerald is not affordable.
	Reserve map[string]int

	// XPLevel is the bot's current experience level.
	XPLevel int32

	// XPObserved reports whether XPLevel is real. False means unknown.
	XPObserved bool
}

// Available is how many of an item the bot may spend: what it holds, less what
// is reserved. An unknown item has nothing available.
func (b Budget) Available(name string) int {
	key := NormalizeItemName(name)
	if key == "" {
		return 0
	}
	have := b.Have[key]
	if b.Reserve != nil {
		have -= b.Reserve[key]
	}
	if have < 0 {
		return 0
	}
	return have
}

// CanAfford reports whether this offer can be paid for right now.
//
// The checks are ordered so the cheap structural ones run first: an offer with
// no output is not a trade, and one with no input is not a purchase. After that
// every input has to be covered by the budget, and an offer that costs levels
// can only proceed when the level is genuinely observable and high enough.
func CanAfford(offer Offer, budget Budget) bool {
	if offer.Output.Name == "" || offer.Output.Count <= 0 {
		return false
	}
	if len(offer.Inputs) == 0 {
		return false
	}
	if offer.SpentOut() {
		return false
	}
	for _, in := range offer.Inputs {
		if in.Name == "" || in.Count <= 0 {
			return false
		}
		if budget.Available(in.Name) < in.Count {
			return false
		}
	}
	if offer.WantsLevel() {
		if !budget.XPObserved {
			// The cost is unknown, and an unknown cost is not one the bot can
			// pay. It refuses rather than finding out from the server.
			return false
		}
		if budget.XPLevel < offer.RequiredLevel {
			return false
		}
	}
	return true
}

// PickOffer chooses which offer to trade on. It is pure: no connection, no
// clock, no mutation of the budget — the same inputs always give the same
// answer, which is what makes it testable and what stops a decision from
// depending on map iteration order.
//
// want filters the candidates and may be nil, meaning anything affordable is
// fine. Among the survivors the offer with the most output per input spent
// wins, and a tie goes to the lower Index so the choice is reproducible rather
// than dependent on how a slice happened to be built.
func PickOffer(offers []Offer, budget Budget, want func(Offer) bool) (Offer, bool) {
	best, found := Offer{}, false
	bestValue := 0.0

	for _, offer := range offers {
		if want != nil && !want(offer) {
			continue
		}
		if !CanAfford(offer, budget) {
			continue
		}
		value := float64(offer.Output.Count) / float64(offer.InputTotal())
		switch {
		case !found:
		case value > bestValue:
		case value == bestValue && offer.Index < best.Index:
		default:
			continue
		}
		best, found, bestValue = offer, true, value
	}
	return best, found
}

// NormalizeItemName reduces a runtime item identifier to the bare name: lower
// case, no namespace, underscores for spaces. It is idempotent, so a name that
// has already been through it is a usable map key against one that has not.
func NormalizeItemName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimPrefix(n, "minecraft:")
	n = strings.ReplaceAll(n, " ", "_")
	return strings.TrimSpace(n)
}

// sortedOfferKeys returns a compound's keys in a stable order, so a decoder
// that has to fall back to scanning a map produces the same offers every time.
func sortedOfferKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
