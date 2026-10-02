package station

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// anvilTimeout is the default budget for the anvil to compute its result. It
// is far longer than a real anvil needs, because the cost is paid in levels
// the bot cannot get back: a slow server is better waited out than retried.
const anvilTimeout = 5 * time.Second

// maxRenameLength is the anvil's own cap on a custom name. A longer name is
// refused by the server, so the bot should not spend the levels finding out.
const maxRenameLength = 35

// AnvilRepairMaterial maps a tool to the item an anvil needs to repair it.
//
// A repair with the wrong material does nothing at all, and a bot that guesses
// costs itself a rare item every time it tries. The table is keyed on the
// material tier and then on the tool word, so every iron tool repairs with an
// ingot without the list having to name each one.
var anvilRepairMaterials = []struct {
	prefix   string
	material string
}{
	{"netherite_", "netherite_scrap"},
	{"diamond_", "diamond"},
	{"golden_", "golden_apple"},
	{"gold_", "golden_apple"},
	{"iron_", "iron_ingot"},
	{"chainmail_", "iron_ingot"},
	{"stone_", "cobblestone"},
	{"wooden_", "oak_planks"},
	{"leather_", "leather"},
	{"turtle_", "turtle_scute"},
}

// standaloneRepairMaterials covers the tools whose name does not carry a tier
// prefix: shears and a fishing rod are named plainly.
var standaloneRepairMaterials = map[string]string{
	"shears":                   "iron_ingot",
	"fishing_rod":              "stick",
	"flint_and_steel":          "iron_ingot",
	"carrot_on_a_stick":        "stick",
	"warped_fungus_on_a_stick": "stick",
}

// AnvilRepairMaterial returns the item needed to repair the named tool, or
// empty when the item cannot be repaired on an anvil at all.
func AnvilRepairMaterial(toolName string) string {
	name := NormalizeItemName(toolName)
	if name == "" {
		return ""
	}
	if material, ok := standaloneRepairMaterials[name]; ok {
		return material
	}
	for _, entry := range anvilRepairMaterials {
		if strings.HasPrefix(name, entry.prefix) {
			return entry.material
		}
	}
	return ""
}

// ValidateRename checks a custom name against the anvil's own rules, so a
// hopeless rename is refused before a window is opened and levels are spent.
func ValidateRename(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return errors.New("a custom name cannot be blank")
	}
	if count := len([]rune(trimmed)); count > maxRenameLength {
		return fmt.Errorf("a custom name is %d characters, the anvil allows %d", count, maxRenameLength)
	}
	for _, r := range trimmed {
		// The section sign is the chat formatting escape; a custom name
		// carrying one is refused by the client and stripped by others, which
		// is worse than either outcome.
		if r == '§' {
			return errors.New("a custom name cannot contain the section sign")
		}
		if unicode.IsControl(r) {
			return fmt.Errorf("a custom name cannot contain the control character %q", r)
		}
	}
	return nil
}

// AnvilResult is what an anvil actually produced, read back from the server's
// own output slot.
type AnvilResult struct {
	// OutputName is the name the server gave the result. For a rename this is
	// the custom name, which is how the rename is confirmed rather than
	// assumed.
	OutputName string
	// OutputCount is how many items came out.
	OutputCount int
	// Taken reports that the result was moved into the bot's inventory.
	Taken bool
}

// RepairItem repairs itemName at a nearby anvil using the material the anvil
// requires, and reports the confirmed result.
//
// True means the server produced a repaired tool and the bot took it. An anvil
// that produces nothing — no material, a tool at full durability, a server
// that simply never answers — is a false with the reason logged.
func (m *Manager) RepairItem(ctx context.Context, itemName string) (AnvilResult, bool) {
	tool, ok := m.findInInventory(itemName)
	if !ok {
		m.logger.Warn("RepairItem: tool not in inventory", "item", itemName)
		return AnvilResult{}, false
	}
	material := AnvilRepairMaterial(itemName)
	if material == "" {
		m.logger.Warn("RepairItem: the item cannot be repaired on an anvil", "item", itemName)
		return AnvilResult{}, false
	}
	ingot, ok := m.findInInventory(material)
	if !ok {
		m.logger.Warn("RepairItem: repair material not in inventory", "item", itemName, "material", material)
		return AnvilResult{}, false
	}

	pos, ok := m.FindNearbyStation(IsAnvilBlock)
	if !ok {
		m.logger.Warn("RepairItem: no anvil nearby", "item", itemName)
		return AnvilResult{}, false
	}

	session, err := m.openStation(ctx, pos)
	if err != nil {
		m.logger.Warn("RepairItem: could not open the anvil", "err", err)
		return AnvilResult{}, false
	}
	defer m.closeStation(session)

	if err := m.loadAnvil(session, tool, ingot); err != nil {
		m.logger.Warn("RepairItem: could not load the anvil", "item", itemName, "err", err)
		return AnvilResult{}, false
	}

	return m.collectAnvil(ctx, session, "repair", itemName)
}

// RenameItem renames itemName to newName at a nearby anvil using a name tag,
// and reports the confirmed result.
//
// The confirmation is the server's own output name: if the anvil hands back the
// tool under its old name the rename was refused, and reporting the requested
// one would be a fabrication the caller would then display to a player.
func (m *Manager) RenameItem(ctx context.Context, itemName, newName string) (AnvilResult, bool) {
	if err := ValidateRename(newName); err != nil {
		m.logger.Warn("RenameItem: refusing an invalid custom name", "item", itemName, "err", err)
		return AnvilResult{}, false
	}
	want := strings.TrimSpace(newName)

	tool, ok := m.findInInventory(itemName)
	if !ok {
		m.logger.Warn("RenameItem: tool not in inventory", "item", itemName)
		return AnvilResult{}, false
	}
	tag, ok := m.findInInventory("name_tag")
	if !ok {
		m.logger.Warn("RenameItem: no name tag in inventory", "item", itemName)
		return AnvilResult{}, false
	}

	pos, ok := m.FindNearbyStation(IsAnvilBlock)
	if !ok {
		m.logger.Warn("RenameItem: no anvil nearby", "item", itemName)
		return AnvilResult{}, false
	}

	session, err := m.openStation(ctx, pos)
	if err != nil {
		m.logger.Warn("RenameItem: could not open the anvil", "err", err)
		return AnvilResult{}, false
	}
	defer m.closeStation(session)

	if err := m.loadAnvil(session, tool, tag); err != nil {
		m.logger.Warn("RenameItem: could not load the anvil", "item", itemName, "err", err)
		return AnvilResult{}, false
	}

	result, ok := m.collectAnvil(ctx, session, "rename", itemName)
	if !ok {
		return result, false
	}
	if result.OutputName != want {
		m.logger.Warn("RenameItem: the anvil did not apply the name",
			"item", itemName, "want", want, "got", result.OutputName)
		return AnvilResult{OutputName: result.OutputName, OutputCount: result.OutputCount}, false
	}
	m.logger.Info("renamed item", "item", itemName, "name", want)
	return result, true
}

// loadAnvil puts the item in the first input slot and the material or name tag
// in the second. Both placements are server-validated ItemStackRequests.
func (m *Manager) loadAnvil(session stationSession, item, second invStack) error {
	if err := m.bot.PlaceIntoContainerSlotIn(AnvilContainerID, AnvilInputSlot, 0, item.slot, 1); err != nil {
		return fmt.Errorf("place the item in the anvil: %w", err)
	}
	if err := m.bot.PlaceIntoContainerSlotIn(AnvilMaterialContainerID, AnvilMaterialSlot, 0, second.slot, 1); err != nil {
		return fmt.Errorf("place the second input in the anvil: %w", err)
	}
	return nil
}

// collectAnvil waits for the anvil's output slot and takes it. The output is
// the server's own computation, so waiting on it is the whole confirmation —
// there is nothing left to guess.
func (m *Manager) collectAnvil(ctx context.Context, session stationSession, action, itemName string) (AnvilResult, bool) {
	budget := m.anvilBudget
	if budget <= 0 {
		budget = anvilTimeout
	}

	output, ok := m.waitForSlot(ctx, AnvilOutputSlot, budget, func(view stackView) bool {
		return view.count > 0
	})
	if !ok {
		m.logger.Warn("anvil: no output before the budget", "action", action, "item", itemName)
		return AnvilResult{}, false
	}

	result := AnvilResult{OutputName: output.name, OutputCount: output.count}
	if _, err := m.takeSlot(session, AnvilOutputContainerID, AnvilOutputSlot); err != nil {
		m.logger.Warn("anvil: could not take the output", "action", action, "err", err)
		return result, false
	}
	result.Taken = true
	return result, true
}
