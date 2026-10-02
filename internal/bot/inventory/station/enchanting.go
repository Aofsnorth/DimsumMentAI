package station

import (
	"context"
	"errors"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// ErrNoEnchanter is returned when an enchanting action is attempted before the
// Enchanter seam is wired. It is a wiring failure, not a gameplay one, and it
// is deliberately loud: the alternative is a manager that reports an
// enchantment nobody applied.
var ErrNoEnchanter = errors.New("station: no enchanter wired; PlayerEnchantOptions is not handled by internal/bot")

// enchantTimeout is the default budget for the table to offer options and then
// return the enchanted item. A LAN host answers well inside this.
const enchantTimeout = 3 * time.Second

// Enchanter is the seam between this package and the bot's handling of the
// enchanting table.
//
// gophertunnel does expose packet.PlayerEnchantOptions and
// protocol.EnchantmentOption, so there is no need to invent a packet here.
// What is missing is the bot side: internal/bot has no handler for that packet
// and no method that submits the CraftRecipe stack request against
// protocol.ContainerEnchantingInput, which is what actually buys the
// enchantment. Hand-rolling a fake packet would be a bug, so the smallest
// interface the manager needs is declared instead, and the concrete
// *bot.Bot wiring is a follow-up outside this package's write scope.
type Enchanter interface {
	// EnchantOptions returns the options the server last offered in
	// PlayerEnchantOptions, in the order the packet listed them. An empty
	// slice means the table has nothing to sell right now.
	EnchantOptions() []protocol.EnchantmentOption

	// ExperienceLevel reports the bot's current XP level, which is the budget
	// the option costs are charged against.
	ExperienceLevel() int

	// PlaceLapisInEnchantingTable moves count lapis into the table's material
	// slot. An enchant costs one lapis, and the server refuses the option
	// outright when the slot is empty.
	PlaceLapisInEnchantingTable(windowID byte, count int) error

	// ApplyEnchant selects the option at index, spending its level cost and
	// the lapis. It must return an error when the server rejects the request.
	ApplyEnchant(windowID byte, optionIndex int) error
}

// EnchantResult is what an enchant actually achieved, as reported by the
// server-facing seam rather than by the plan.
type EnchantResult struct {
	// Option is the option the manager applied.
	Option protocol.EnchantmentOption
	// OptionIndex is its index in the offered list.
	OptionIndex int
	// LevelsSpent is the option's level cost, as charged.
	LevelsSpent int
	// LapisSpent is how much lapis the table took.
	LapisSpent int
	// ItemName is the item the server returned to the table's input slot.
	ItemName string
	// Taken reports that the enchanted item was moved into the bot's
	// inventory. Without it the enchant is not a result the bot holds.
	Taken bool
}

// EnchantLevel is the highest single enchantment level an option grants. It is
// the tie-break between two options of equal cost: spending the same XP for
// Sharpness II over Sharpness I is the obvious choice.
func EnchantLevel(opt protocol.EnchantmentOption) int {
	best := 0
	for _, group := range opt.Enchantments.Enchantments {
		for _, ench := range group {
			if int(ench.Level) > best {
				best = int(ench.Level)
			}
		}
	}
	return best
}

// SelectEnchantOption picks which offered option to buy.
//
// The rule is "the strongest one the bot can afford": an option whose level
// cost exceeds the bot's XP is rejected by the server, so a manager that
// picked anyway would report an enchant that never happened. Among equally
// affordable options the higher enchantment level wins, and a tie on both goes
// to the cheaper option so the bot keeps more XP for the next one.
func SelectEnchantOption(options []protocol.EnchantmentOption, level int) (int, protocol.EnchantmentOption, bool) {
	bestIndex := -1
	var best protocol.EnchantmentOption
	bestLevel := -1

	for index, opt := range options {
		cost := int(opt.Cost)
		if cost > level {
			continue
		}
		power := EnchantLevel(opt)
		switch {
		case bestIndex < 0:
		case power > bestLevel:
		case power == bestLevel && cost < int(best.Cost):
		default:
			continue
		}
		bestIndex, best, bestLevel = index, opt, power
	}

	if bestIndex < 0 {
		return -1, protocol.EnchantmentOption{}, false
	}
	return bestIndex, best, true
}

// EnchantItem enchants itemName at a nearby enchanting table and reports the
// confirmed result.
//
// True means the server accepted the option, charged the levels and the lapis,
// and the bot took the enchanted item into its inventory. Every other outcome
// is a false with the reason logged.
func (m *Manager) EnchantItem(ctx context.Context, itemName string) (EnchantResult, bool) {
	if m.enchanter == nil {
		m.logger.Warn("EnchantItem: refusing to invent an enchantment", "item", itemName, "err", ErrNoEnchanter)
		return EnchantResult{}, false
	}

	item, ok := m.findInInventory(itemName)
	if !ok {
		m.logger.Warn("EnchantItem: item not in inventory", "item", itemName)
		return EnchantResult{}, false
	}
	// An enchant costs one lapis. Checking here means the failure is a clear
	// "no lapis" rather than an opaque server rejection three steps later.
	lapis, hasLapis := m.findInInventory("lapis_lazuli")
	if !hasLapis || lapis.count <= 0 {
		m.logger.Warn("EnchantItem: no lapis in inventory", "item", itemName)
		return EnchantResult{}, false
	}

	pos, ok := m.FindNearbyStation(IsEnchantingTableBlock)
	if !ok {
		m.logger.Warn("EnchantItem: no enchanting table nearby", "item", itemName)
		return EnchantResult{}, false
	}

	session, err := m.openStation(ctx, pos)
	if err != nil {
		m.logger.Warn("EnchantItem: could not open the enchanting table", "err", err)
		return EnchantResult{}, false
	}
	defer m.closeStation(session)

	// The tool goes in the table's input slot. destStackNetID 0 is the
	// empty-slot convention the container session already uses.
	if err := m.bot.PlaceIntoContainerSlotIn(EnchantContainerID, EnchantInputSlot, 0, item.slot, 1); err != nil {
		m.logger.Warn("EnchantItem: could not place the item in the table", "item", itemName, "err", err)
		return EnchantResult{}, false
	}
	if err := m.enchanter.PlaceLapisInEnchantingTable(session.windowID, 1); err != nil {
		m.logger.Warn("EnchantItem: could not place lapis in the table", "err", err)
		return EnchantResult{}, false
	}

	// Remember what the input slot is holding *before* the enchant, so the
	// confirmation below has something to compare against.
	//
	// The old predicate was `count > 0`, which is satisfied the instant the tool
	// lands in the slot three lines above — so it returned true before the server
	// had done anything at all, and a rejected enchant was reported as a
	// successful one. That is precisely the fabricated success this package exists
	// to avoid, and it survived because the fake bot in the tests fills the slot
	// the way a real one does.
	//
	// A stack network ID changes whenever the server replaces the stack, so it
	// is the signal that says "the tool in this slot is not the tool that went in".
	before, _ := m.readSlot(EnchantInputSlot)

	budget := m.enchantBudget
	if budget <= 0 {
		budget = enchantTimeout
	}

	index, option, ok := m.chooseOption(ctx, budget)
	if !ok {
		m.logger.Warn("EnchantItem: the server offered nothing affordable",
			"item", itemName, "level", m.enchanter.ExperienceLevel())
		return EnchantResult{}, false
	}

	levelBefore := m.enchanter.ExperienceLevel()
	if err := m.enchanter.ApplyEnchant(session.windowID, index); err != nil {
		m.logger.Warn("EnchantItem: the server rejected the enchant", "item", itemName, "err", err)
		return EnchantResult{Option: option, OptionIndex: index}, false
	}

	// Confirm the enchant landed. The server charges levels for it, and the
	// enchanted item comes back to the table's input slot as a *different stack*,
	// so the check is that the stack network ID moved — not that the slot is
	// non-empty, which it has been since the tool went in.
	enchanted, ok := m.waitForSlot(ctx, EnchantInputSlot, budget, func(view stackView) bool {
		if view.count <= 0 {
			// The slot emptied: the item left the table without coming back
			// enchanted, which is a failure however the request was answered.
			return false
		}
		return view.stackNetID != before.stackNetID
	})
	if !ok {
		m.logger.Warn("EnchantItem: the table never returned an enchanted item", "item", itemName)
		return EnchantResult{Option: option, OptionIndex: index, LevelsSpent: m.levelsSpent(levelBefore)}, false
	}

	result := EnchantResult{
		Option:      option,
		OptionIndex: index,
		LevelsSpent: m.levelsSpent(levelBefore),
		LapisSpent:  1,
		ItemName:    enchanted.name,
	}
	if _, err := m.takeSlot(session, EnchantContainerID, EnchantInputSlot); err != nil {
		m.logger.Warn("EnchantItem: could not take the enchanted item", "err", err)
		result.ItemName = enchanted.name
		return result, false
	}
	result.Taken = true
	m.logger.Info("enchanted item", "item", enchanted.name, "option", option.Name, "levels", result.LevelsSpent)
	return result, true
}

// chooseOption waits for the server to offer options and picks one. The wait
// matters: the table sends its options after the item lands in the slot, and
// reading immediately would see the empty packet the server sends on open.
func (m *Manager) chooseOption(ctx context.Context, budget time.Duration) (int, protocol.EnchantmentOption, bool) {
	deadline := time.NewTimer(budget)
	defer deadline.Stop()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	// A table with air in the slot sends an empty list and never follows up
	// with a real one, so an empty read is only waited on briefly before the
	// action gives up rather than burning the whole budget.
	emptyBudget := budget / 2
	emptySince := time.Now()

	for {
		options := m.enchanter.EnchantOptions()
		if len(options) > 0 {
			if index, opt, ok := SelectEnchantOption(options, m.enchanter.ExperienceLevel()); ok {
				return index, opt, true
			}
			// Options arrived but none are affordable; waiting longer cannot
			// change that, the bot's XP is not growing.
			return -1, protocol.EnchantmentOption{}, false
		}
		if time.Since(emptySince) >= emptyBudget {
			return -1, protocol.EnchantmentOption{}, false
		}

		select {
		case <-ctx.Done():
			return -1, protocol.EnchantmentOption{}, false
		case <-deadline.C:
			return -1, protocol.EnchantmentOption{}, false
		case <-ticker.C:
		}
	}
}

// levelsSpent reports how many levels the bot actually lost across an enchant.
// A server that charged nothing did not enchant, so this is measured rather
// than assumed from the option's advertised cost.
func (m *Manager) levelsSpent(levelBefore int) int {
	spent := levelBefore - m.enchanter.ExperienceLevel()
	if spent < 0 {
		return 0
	}
	return spent
}
