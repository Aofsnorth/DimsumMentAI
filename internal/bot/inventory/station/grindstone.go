package station

import (
	"context"
	"strings"
	"time"
)

// grindstoneTimeout is the default budget for the grindstone to produce its
// result. A grindstone is instant in vanilla, so anything slower than this is
// a server that is not going to answer.
const grindstoneTimeout = 3 * time.Second

// grindstoneMaterials are the items a grindstone hands back when it strips an
// enchantment. The exact refund depends on the enchantment — lapis for most, an
// enchanted book for a single one — so the check is "did any material appear",
// not "did exactly this appear".
var grindstoneMaterials = []string{
	"lapis_lazuli",
	"enchanted_book",
}

// IsGrindstoneMaterial reports whether an item name is something a grindstone
// can return. Reporting the wrong refund is how a bot tells its caller it
// earned something it did not.
func IsGrindstoneMaterial(name string) bool {
	for _, material := range grindstoneMaterials {
		if NormalizeItemName(name) == material {
			return true
		}
	}
	return false
}

// GrindstoneResult is what a grindstone actually produced.
type GrindstoneResult struct {
	// OutputName is the name the server gave the stripped item.
	OutputName string
	// OutputCount is how many items came out.
	OutputCount int
	// Materials is the material the server returned alongside the item. It is
	// read back from the bot's own inventory, so it is a fact rather than an
	// assumption about what a grindstone usually gives.
	Materials string
	// MaterialsReturned reports that the refund was actually observed.
	MaterialsReturned bool
	// Taken reports that the stripped item was moved into the inventory.
	Taken bool
}

// DisenchantItem strips the enchantments from itemName at a nearby grindstone
// and reports the confirmed result.
//
// True means the server produced the stripped item and the bot took it. An item
// with nothing to strip produces no output, and that is a false, not a
// successful no-op.
func (m *Manager) DisenchantItem(ctx context.Context, itemName string) (GrindstoneResult, bool) {
	item, ok := m.findInInventory(itemName)
	if !ok {
		m.logger.Warn("DisenchantItem: item not in inventory", "item", itemName)
		return GrindstoneResult{}, false
	}
	// A grindstone can only strip enchantments from tools, weapons, and armour.
	// Offering it a block wastes a window and an item slot on a station that
	// will answer with nothing.
	if !isDisenchantable(item.name) {
		m.logger.Warn("DisenchantItem: the item has no enchantments to strip", "item", item.name)
		return GrindstoneResult{}, false
	}

	pos, ok := m.FindNearbyStation(IsGrindstoneBlock)
	if !ok {
		m.logger.Warn("DisenchantItem: no grindstone nearby", "item", itemName)
		return GrindstoneResult{}, false
	}

	// The refund is watched from the inventory rather than assumed, so it is
	// snapshotted before the strip.
	before := m.materialCounts()

	session, err := m.openStation(ctx, pos)
	if err != nil {
		m.logger.Warn("DisenchantItem: could not open the grindstone", "err", err)
		return GrindstoneResult{}, false
	}
	defer m.closeStation(session)

	if err := m.bot.PlaceIntoContainerSlotIn(GrindstoneContainerID, GrindstoneInputSlot, 0, item.slot, 1); err != nil {
		m.logger.Warn("DisenchantItem: could not place the item in the grindstone", "item", itemName, "err", err)
		return GrindstoneResult{}, false
	}

	budget := m.grindstoneBudget
	if budget <= 0 {
		budget = grindstoneTimeout
	}

	output, ok := m.waitForSlot(ctx, GrindstoneOutputSlot, budget, func(view stackView) bool {
		return view.count > 0
	})
	if !ok {
		m.logger.Warn("DisenchantItem: the grindstone produced nothing", "item", itemName)
		return GrindstoneResult{}, false
	}

	result := GrindstoneResult{OutputName: output.name, OutputCount: output.count}
	if _, err := m.takeSlot(session, GrindstoneOutputContainerID, GrindstoneOutputSlot); err != nil {
		m.logger.Warn("DisenchantItem: could not take the stripped item", "item", itemName, "err", err)
		return result, false
	}
	result.Taken = true

	if refund, returned := m.detectRefund(before); returned {
		result.Materials = refund
		result.MaterialsReturned = true
	}
	m.logger.Info("disenchanted item", "item", output.name, "materials", result.Materials)
	return result, true
}

// materialCounts tallies the grindstone refund materials the bot is holding
// right now, keyed by item name. The tallies are compared before and after a
// strip to see what the server actually handed back.
func (m *Manager) materialCounts() map[string]int {
	counts := make(map[string]int, len(grindstoneMaterials))
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()
	for _, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		name := NormalizeItemName(names[stack.NetworkID])
		if IsGrindstoneMaterial(name) {
			counts[name] += int(stack.Count)
		}
	}
	return counts
}

// detectRefund reports which material grew between two tallies.
func (m *Manager) detectRefund(before map[string]int) (string, bool) {
	for name, count := range m.materialCounts() {
		if count > before[name] {
			return name, true
		}
	}
	return "", false
}

// disenchantableWords are the tool and armour words a grindstone can strip.
// Enchanting a pickaxe works; enchanting a block does not, and a grindstone
// offered a block answers with nothing at all.
var disenchantableWords = []string{
	"sword", "pickaxe", "axe", "shovel", "hoe", "helmet", "chestplate",
	"leggings", "boots", "bow", "crossbow", "trident", "elytra", "shield",
	"mace", "brush",
}

// isDisenchantable reports whether an item name is something a grindstone can
// strip enchantments from.
func isDisenchantable(name string) bool {
	n := NormalizeItemName(name)
	if n == "" {
		return false
	}
	for _, word := range disenchantableWords {
		if strings.Contains(n, word) {
			return true
		}
	}
	return false
}
