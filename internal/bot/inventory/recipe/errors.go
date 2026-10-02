package recipe

import (
	"errors"
	"sort"
)

var (
	// errCannotReach is returned when the bot could not walk to the station.
	errCannotReach = errors.New("could not walk to the station")
	// errNoWindow is returned when the server never opened a window for the
	// click. It is the failure a fixed sleep would have papered over.
	errNoWindow = errors.New("server did not open the station window")
)

// clickError explains a click the interactor refused. The reason comes from the
// interactor, so it is wrapped rather than replaced — "the smithing table did
// not respond" is a different problem from "out of reach".
type clickError struct {
	reason string
}

func (e *clickError) Error() string {
	if e.reason == "" {
		return "click station: refused"
	}
	return "click station: " + e.reason
}

// sortInventory orders a snapshot by slot index.
//
// The bot's bag is a map, so its iteration order is random. Sorting is what
// makes "the first slot holding an upgrade template" mean the same thing on
// every call, and a plan that is stable is a plan that can be tested.
func sortInventory(inv Inventory) {
	sort.Slice(inv, func(a, b int) bool { return inv[a].Slot < inv[b].Slot })
}
