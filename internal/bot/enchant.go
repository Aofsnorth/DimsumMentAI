package bot

import (
	"errors"
	"fmt"
	"sync"

	"bedrock-ai/internal/bot/inventory/station"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The enchanting table's server-facing half.
//
// internal/bot/inventory/station knows how to walk to a table, put a tool and a
// lapis into it, wait for a result, and take the item back — but it has no way
// to know what the table is offering. That knowledge exists in exactly one place
// on the wire: packet.PlayerEnchantOptions, which the server sends whenever the
// table's contents change, and which the vanilla server sends *empty* the moment
// the table opens. Before this file the bot had no handler for that packet, so
// the options were never recorded, the manager's chooseOption always timed out,
// and the enchanting table was dead at runtime no matter how well the station
// package was written.
//
// What the protocol actually offers is worth stating plainly, because it decides
// how much this file may claim:
//
//   - protocol.EnchantmentOption (gophertunnel/minecraft/protocol/enchant.go)
//     carries Cost, Name, the enchantments themselves, and RecipeNetworkID.
//   - The server identifies which of the three buttons was pressed *solely* by
//     RecipeNetworkID: "When enchanting, the client will submit this network ID
//     in a ItemStackRequest packet with the CraftRecipe action, so that the
//     server knows which enchantment was selected." There is no packet that says
//     "option 2 of 3" and no other field that identifies the choice.
//   - The server's answer is a normal ItemStackResponse, whose status says only
//     accepted or rejected. Its per-slot payload (StackResponseSlotInfo) carries
//     a slot, a count, a stack network ID, a custom name and a durability
//     correction — and no item identity and no enchantment list at all.
//
// So an accepted response proves the server processed the request; it does not
// carry the enchantments that resulted. ApplyEnchant is documented on exactly
// that basis, and the manager keeps its own read-back as the separate check.

// ErrNoBot is returned by the enchanting seam when it is asked to do something
// with no bot behind it. It is a wiring failure, not a gameplay one.
var ErrNoBot = errors.New("bot: enchanting table seam has no bot")

// LapisItemName is the item an enchanting table charges for every option. The
// server refuses an option outright when the material slot is empty, so this is
// the one item the table cannot be used without.
const LapisItemName = "lapis_lazuli"

// enchantState is the last thing the server offered at an enchanting table.
//
// It carries no mutex of its own beyond the one below, and nothing else: the
// options are the whole of the table's server state, and the table overwrites
// them wholesale on every slot update rather than editing them in place.
type enchantState struct {
	mu      sync.Mutex
	options []protocol.EnchantmentOption
}

// enchantStates holds one enchanting-table state per bot.
//
// This is a package-level registry rather than a field on Bot for a structural
// reason, not a preference: Bot's struct lives in bot.go, and the natural home
// for this state is a field there next to xpLevel and TradeWindows. The entries
// are bounded by the number of live bots and are dropped by ResetEnchantState on
// every session change, so a long-running reconnect loop does not accumulate
// them. The follow-up is one field on Bot and the deletion of this map.
var enchantStates sync.Map // map[*Bot]*enchantState

// enchantStateFor returns the bot's enchanting state, creating it on first use.
//
// Creation is lazy rather than done in initSubsystems so that a packet arriving
// before the subsystem wiring is finished is still recorded rather than dropped.
func enchantStateFor(b *Bot) *enchantState {
	if state, ok := enchantStates.Load(b); ok {
		return state.(*enchantState)
	}
	state, _ := enchantStates.LoadOrStore(b, &enchantState{})
	return state.(*enchantState)
}

// RecordEnchantOptions stores the options the server last offered, in the order
// the packet listed them.
//
// The order is the packet's, not sorted: the manager selects by index into this
// list, so reordering it here would silently change which option an index buys.
// A nil or empty slice is recorded as "nothing on offer" rather than ignored,
// because that is exactly what the vanilla server sends when the table opens with
// air in the slot, and the manager waits for a real list rather than acting on a
// stale one.
func RecordEnchantOptions(b *Bot, options []protocol.EnchantmentOption) {
	if b == nil {
		return
	}
	stored := make([]protocol.EnchantmentOption, len(options))
	copy(stored, options)

	state := enchantStateFor(b)
	state.mu.Lock()
	state.options = stored
	state.mu.Unlock()
}

// ReadEnchantOptions returns a copy of the options the server last offered.
//
// The copy is not defensive decoration. The caller holds the slice for as long
// as it takes to select and apply an option, while the packet handler can write
// a new list at any moment, and a shared backing array would be a data race that
// surfaces as an option quietly changing its cost between selection and use.
func ReadEnchantOptions(b *Bot) []protocol.EnchantmentOption {
	if b == nil {
		return nil
	}
	state := enchantStateFor(b)
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.options) == 0 {
		return nil
	}
	out := make([]protocol.EnchantmentOption, len(state.options))
	copy(out, state.options)
	return out
}

// ResetEnchantState drops the offered options for a bot.
//
// A recipe network ID is only meaningful in the world that issued it, so this
// runs on every session change alongside the rest of the per-world state. Left
// in place it would let the manager ask a freshly joined server to craft recipe
// numbers it never sent.
func ResetEnchantState(b *Bot) {
	if b == nil {
		return
	}
	enchantStates.Delete(b)
}

// EnchantOptionAt resolves the option a manager picked back out of the offered
// list, refusing anything the server did not offer.
//
// The index is not clamped and not defaulted. It came from
// station.SelectEnchantOption over the same list, so an out-of-range value means
// the two halves disagree about what was on offer, and quietly buying a different
// enchantment than the one that was selected is the worst possible response.
func EnchantOptionAt(options []protocol.EnchantmentOption, index int) (protocol.EnchantmentOption, error) {
	if index < 0 || index >= len(options) {
		return protocol.EnchantmentOption{}, fmt.Errorf("bot: enchant option %d is outside the %d options the server offered", index, len(options))
	}
	opt := options[index]
	// RecipeNetworkID is the only thing that tells the server which option was
	// chosen, and 0 is the ID space ordinary crafting recipes share. Sending it
	// would ask for a craft rather than an enchant, so an option without one is
	// refused here while it can still be refused cheaply.
	if opt.RecipeNetworkID == 0 {
		return protocol.EnchantmentOption{}, fmt.Errorf("bot: enchant option %d (%q) has no recipe network ID, so the server cannot be told which option it is", index, opt.Name)
	}
	return opt, nil
}

// BuildApplyEnchantActions builds the stack request that buys one enchantment.
//
// It is a single CraftRecipe action and nothing else, which is worth being
// explicit about. The tool and the lapis were already moved into the table by
// their own transfers and confirmed by their own responses; repeating either
// here would spend a second lapis and desync the table. The option's
// RecipeNetworkID is the entire message: the server already knows what is in its
// own input slot, and it reads the ID to learn which button was pressed.
//
// NumberOfCrafts is set to 1 for the same reason. The field is documented as
// boilerplate for ordinary recipes, and an enchant is bought one level at a time.
func BuildApplyEnchantActions(option protocol.EnchantmentOption) []protocol.StackRequestAction {
	return []protocol.StackRequestAction{
		&protocol.CraftRecipeStackRequestAction{
			RecipeNetworkID: option.RecipeNetworkID,
			NumberOfCrafts:  1,
		},
	}
}

// EnchantTable is the bot side of station.Enchanter.
//
// It is a value wrapper rather than methods on *Bot because the interface asks
// for ExperienceLevel() int, and *Bot already has ExperienceLevel() (int32, bool)
// for the trading seam. Two methods of one name cannot coexist on one type, and
// the two-valued form is the better one to keep: "never observed" must not read
// as zero. The wrapper narrows it, and encodes never-observed as a level no
// option is affordable at.
type EnchantTable struct{ B *Bot }

// EnchantOptions implements station.Enchanter.
func (e EnchantTable) EnchantOptions() []protocol.EnchantmentOption {
	return ReadEnchantOptions(e.B)
}

// ExperienceLevel implements station.Enchanter.
//
// The interface has no "unknown" case, so one is encoded in the value: a bot
// whose level the server has never sent reports -1, which is below the cost of
// every possible option and therefore makes station.SelectEnchantOption return
// nothing. Reporting 0 instead would be a fabricated reading that lets a bot
// with no known level select a cheap option and report the result as real.
func (e EnchantTable) ExperienceLevel() int {
	if e.B == nil {
		return -1
	}
	level, seen := e.B.ExperienceLevel()
	if !seen {
		return -1
	}
	return int(level)
}

// compile-time proof the seam is satisfied, so a change to either side fails at
// build time rather than at the first enchantment.
var _ station.Enchanter = EnchantTable{}
