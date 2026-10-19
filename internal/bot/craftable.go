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
	// Snapshot, do not alias.
	//
	// These were assigned rather than copied, so the unlock on the next line
	// released the lock while the iteration below was still walking live maps.
	// The packet read loop writes InventoryMap on every inventory transaction,
	// and the runtime's map-corruption detector does not care that the writer
	// holds the right lock — it only sees two goroutines on one map, and it
	// kills the process. That is unrecoverable: no recover() catches it, and the
	// session dies mid-world with the goroutine dump as the only explanation.
	b.Mu.Lock()
	inv := make(map[uint32]protocol.ItemStack, len(b.InventoryMap))
	for slot, stack := range b.InventoryMap {
		inv[slot] = stack
	}
	names := make(map[int32]string, len(b.ItemNames))
	for id, name := range b.ItemNames {
		names[id] = name
	}
	recipes := make(map[uint32]RecipeInfo, len(b.RecipesByNetID))
	for netID, recipe := range b.RecipesByNetID {
		recipes[netID] = recipe
	}
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
		canCraft, missing := b.CanCraftRecipe(recipe, inv, names)

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

// CanCraftRecipe reports whether the bot's inventory satisfies every ingredient
// of recipe. It reuses the same identity resolution and name matching as the
// authoritative crafting path (ResolveIngredientIdentity + ItemNameMatches, as
// used by PlanIngredientConsumption) so the "craftable" list stays consistent
// with what CraftItem will actually consume.
func (b *Bot) CanCraftRecipe(recipe RecipeInfo, inv map[uint32]protocol.ItemStack, names map[int32]string) (bool, string) {
	needed := make(map[string]int)

	// Ingredients are consumed from a shared pool, not matched stack by stack.
	//
	// The old check asked whether ONE stack held the whole count, so a recipe
	// needing 4 planks failed against two stacks of 2, and two ingredients that
	// resolved to the same item could both claim the same stack. Summing per item
	// and deducting what each ingredient takes fixes both directions.
	type poolItem struct {
		name      string
		networkID int32
		remaining int
	}
	pool := make([]poolItem, 0, len(inv))
	for _, stack := range inv {
		pool = append(pool, poolItem{
			name:      names[stack.NetworkID],
			networkID: stack.NetworkID,
			remaining: int(stack.Count),
		})
	}

	for _, ing := range recipe.Ingredients {
		count := int(ing.Count)
		if count == 0 {
			count = 1
		}

		targetName, networkID := ResolveIngredientIdentity(ing.Descriptor, names)
		// Descriptors the bot cannot resolve (MoLang, complex alias) are left
		// for the server to validate; treat them as satisfied here.
		if targetName == "" && networkID == 0 {
			continue
		}

		need := count
		for i := range pool {
			if need == 0 {
				break
			}
			if pool[i].remaining <= 0 {
				continue
			}
			matches := false
			if targetName != "" {
				matches = pool[i].name != "" && ItemNameMatches(pool[i].name, targetName)
			} else {
				matches = pool[i].networkID == networkID
			}
			if !matches {
				continue
			}
			take := need
			if pool[i].remaining < take {
				take = pool[i].remaining
			}
			pool[i].remaining -= take
			need -= take
		}

		if need > 0 {
			// Report the recipe's requirement, not the shortfall: the message is
			// "what this recipe needs", and it has always been phrased that way.
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
// from the identity resolved by ResolveIngredientIdentity.
func missingIngredientName(targetName string, networkID int32, names map[int32]string) string {
	if targetName != "" {
		return FormatItemName(targetName)
	}
	if name := names[networkID]; name != "" {
		return FormatItemName(name)
	}
	return "bahan_tidak_diketahui"
}
