package bot

import (
	"sort"
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

// canCraftRecipe checks if bot has enough ingredients for a recipe.
func (b *Bot) canCraftRecipe(recipe RecipeInfo, inv map[uint32]protocol.ItemStack, names map[int32]string) (bool, string) {
	needed := make(map[string]int)

	for _, ing := range recipe.Ingredients {
		// Count how many of this ingredient we need
		count := int(ing.Count)
		if count == 0 {
			count = 1
		}

		// Find all items that match this descriptor
		matched := false
		for _, stack := range inv {
			itemName := names[stack.NetworkID]
			if matchesDescriptor(ing, itemName) {
				if int(stack.Count) >= count {
					matched = true
					break
				}
			}
		}

		if !matched {
			// Track missing ingredient
			ingName := descriptorToName(ing, names)
			needed[ingName] += count
		}
	}

	if len(needed) == 0 {
		return true, ""
	}

	// Build missing string
	var parts []string
	for name, count := range needed {
		parts = append(parts, name+" x"+string(rune('0'+count)))
	}
	return false, strings.Join(parts, ", ")
}

// matchesDescriptor checks if an item matches a recipe descriptor.
// Uses ItemDescriptor type to match against actual inventory items.
func matchesDescriptor(desc protocol.ItemDescriptorCount, itemName string) bool {
	// TODO: Implement proper descriptor matching using desc.Type
	// For now, use generic name matching until descriptor types are handled
	// This should check desc.ItemID, desc.Metadata, desc.Tag based on desc.Type

	// Fallback: always return false for now to avoid false positives
	// Proper implementation needs:
	// - ItemDescriptor.Type check (Default, MoLang, ItemTag, Deferred)
	// - Match against NetworkID or Tag string
	return false
}

// descriptorToName converts a descriptor to a readable name.
// TODO: Implement proper descriptor→name mapping using desc fields
func descriptorToName(desc protocol.ItemDescriptorCount, names map[int32]string) string {
	// Proper implementation needs desc.Type check and field extraction
	// For now return generic placeholder
	return "bahan_tidak_diketahui"
}
