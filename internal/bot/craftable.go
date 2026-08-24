package bot

import (
	"sort"
	"strconv"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// CraftableItem represents a recipe the bot can currently craft.
type CraftableItem struct {
	Name        string // e.g. "wooden_sword"
	RecipeNetID uint32
	Output      protocol.ItemStack
	CanCraft    bool   // true if bot has all ingredients
	Missing     string // description of missing ingredients
	GridSize    string // "2x2" or "3x3"
}

// ListCraftableItems returns all recipes the bot can craft with current inventory + crafting table status.
func (b *Bot) ListCraftableItems(hasCraftingTable bool) []CraftableItem {
	b.Mu.Lock()
	inv := b.InventoryMap
	names := b.ItemNames
	recipes := b.RecipesByNetID
	b.Mu.Unlock()

	result := make([]CraftableItem, 0, InitialCraftableCapacity)

	for recipeNetID, recipe := range recipes {
		// Skip if requires crafting table but bot doesn't have one
		if recipe.Block == "crafting_table" && !hasCraftingTable {
			continue
		}

		outputName := names[recipe.Output.NetworkID]
		if outputName == "" {
			outputName = "unknown"
		}

		// Normalize name: remove minecraft: prefix
		outputName = strings.TrimPrefix(outputName, "minecraft:")

		// Determine grid size
		gridSize := "2x2"
		if recipe.Block == "crafting_table" {
			gridSize = "3x3"
		}

		// Check if bot has ingredients
		canCraft, missing := b.canCraftRecipe(recipe, inv, names)

		result = append(result, CraftableItem{
			Name:        outputName,
			RecipeNetID: recipeNetID,
			Output:      recipe.Output,
			CanCraft:    canCraft,
			Missing:     missing,
			GridSize:    gridSize,
		})
	}

	// Sort: craftable first, then alphabetically
	sort.Slice(result, func(i, j int) bool {
		if result[i].CanCraft != result[j].CanCraft {
			return result[i].CanCraft
		}
		return result[i].Name < result[j].Name
	})

	return result
}

// canCraftRecipe reports whether the bot's inventory satisfies every ingredient
// of recipe. It reuses the same identity resolution and name matching as the
// authoritative crafting path (resolveIngredientIdentity + itemNameMatches, as
// used by planIngredientConsumption) so the "craftable" list stays consistent
// with what CraftItem will actually consume.
func (b *Bot) canCraftRecipe(recipe RecipeInfo, inv map[uint32]protocol.ItemStack, names map[int32]string) (bool, string) {
	needed := make(map[string]int)

	for _, ing := range recipe.Ingredients {
		count := int(ing.Count)
		if count == 0 {
			count = 1
		}

		targetName, networkID := resolveIngredientIdentity(ing.Descriptor, names)
		// Descriptors the bot cannot resolve (MoLang, complex alias) are left
		// for the server to validate; treat them as satisfied here.
		if targetName == "" && networkID == 0 {
			continue
		}

		matched := false
		for _, stack := range inv {
			itemName := names[stack.NetworkID]
			var ok bool
			if targetName != "" {
				ok = itemName != "" && itemNameMatches(itemName, targetName)
			} else {
				ok = stack.NetworkID == networkID
			}
			if ok && int(stack.Count) >= count {
				matched = true
				break
			}
		}

		if !matched {
			needed[missingIngredientName(targetName, networkID, names)] += count
		}
	}

	if len(needed) == 0 {
		return true, ""
	}

	// Build missing string. Use decimal formatting (strconv.Itoa) so counts
	// of 10+ render correctly — the old string(rune('0'+count)) only worked
	// for single digits.
	var parts []string
	for name, count := range needed {
		parts = append(parts, name+" x"+strconv.Itoa(count))
	}
	return false, strings.Join(parts, ", ")
}

// missingIngredientName renders a human-readable name for an unmet ingredient
// from the identity resolved by resolveIngredientIdentity.
func missingIngredientName(targetName string, networkID int32, names map[int32]string) string {
	if targetName != "" {
		return FormatItemName(targetName)
	}
	if name := names[networkID]; name != "" {
		return FormatItemName(name)
	}
	return "bahan_tidak_diketahui"
}
