// Package bot provides the core Minecraft bot implementation.
package bot

import (
	"fmt"
	"strings"

	"bedrock-ai/internal/safecast"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// IngredientPick is one resolved recipe ingredient: the inventory slot it will
// be taken from, how many items to take, and which recipe ingredient it serves.
type IngredientPick struct {
	Slot            uint32
	Count           int
	IngredientIndex int
}

type IngredientSourceSnapshot struct {
	stack    protocol.ItemStack
	consumed int
}

func SnapshotIngredientSources(inventory map[uint32]protocol.ItemStack, picks []IngredientPick) map[uint32]IngredientSourceSnapshot {
	sources := make(map[uint32]IngredientSourceSnapshot, len(picks))
	for _, pick := range picks {
		source, exists := sources[pick.Slot]
		if !exists {
			source.stack = inventory[pick.Slot]
		}
		source.consumed += pick.Count
		sources[pick.Slot] = source
	}
	return sources
}

// snapshotIngredientSourcesFromRecipe builds ingredient source snapshots from
// the recipe's ingredient descriptors rather than from pick plans, for use with
// AutoCraft where we don't plan individual grid placements.
func snapshotIngredientSourcesFromRecipe(inv map[uint32]protocol.ItemStack, names map[int32]string, ingredients []protocol.ItemDescriptorCount, crafts int) map[uint32]IngredientSourceSnapshot {
	sources := make(map[uint32]IngredientSourceSnapshot)
	for _, ing := range ingredients {
		targetName, networkID := ResolveIngredientIdentity(ing.Descriptor, names)
		if targetName == "" && networkID == 0 {
			continue
		}
		needed := int(ing.Count) * crafts
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			itemName := names[item.NetworkID]
			matched := false
			if targetName != "" {
				matched = itemName != "" && ItemNameMatches(itemName, targetName)
			} else {
				matched = item.NetworkID == safecast.To[int32](networkID)
			}
			if !matched {
				continue
			}
			if _, exists := sources[slot]; exists {
				continue
			}
			sources[slot] = IngredientSourceSnapshot{stack: item, consumed: needed}
			break
		}
	}
	return sources
}

// ReconcileCraftIngredientCounts repairs missing client-side predictions after
// an accepted craft. ItemStackResponse remains authoritative: counts already at
// or below the expected value are preserved.
func ReconcileCraftIngredientCounts(inventory map[uint32]protocol.ItemStack, stackNetworkIDs map[uint32]int32, sources map[uint32]IngredientSourceSnapshot) {
	for slot, source := range sources {
		expected := int(source.stack.Count) - source.consumed
		if expected < 0 {
			continue
		}

		current, exists := inventory[slot]
		if !exists || current.NetworkID != source.stack.NetworkID || int(current.Count) <= expected {
			continue
		}
		if expected == 0 {
			delete(inventory, slot)
			delete(stackNetworkIDs, slot)
			continue
		}
		current.Count = uint16(expected)
		inventory[slot] = current
	}
}

// PlanIngredientConsumption resolves each recipe ingredient to inventory slots
// containing matching items and computes per-slot consume counts. Returns an
// error if any ingredient cannot be satisfied for `times` repetitions.
//
// Bedrock servers sometimes use different runtime IDs for recipe ingredients and
// item stacks (e.g. oak_log in the oak_planks recipe may have a different
// network ID than oak_log in the inventory). We therefore match primarily by
// item name, falling back to strict network ID equality only when the name
// cannot be resolved.
func consumeMatchingSlots(inv map[uint32]protocol.ItemStack, itemNames map[int32]string, remaining map[uint32]int, need int, match func(itemName string, itemNetID int32) bool) ([]IngredientPick, int) {
	picks := make([]IngredientPick, 0)
	for slot, item := range inv {
		if remaining[slot] <= 0 {
			continue
		}
		itemName := itemNames[item.NetworkID]
		if !match(itemName, item.NetworkID) {
			continue
		}
		take := remaining[slot]
		if take > need {
			take = need
		}
		picks = append(picks, IngredientPick{Slot: slot, Count: take})
		remaining[slot] -= take
		need -= take
		if need <= 0 {
			break
		}
	}
	return picks, need
}

func PlanIngredientConsumption(inv map[uint32]protocol.ItemStack, itemNames map[int32]string, ingredients []protocol.ItemDescriptorCount, times int) ([]IngredientPick, error) {
	// Track per-slot remaining count as we consume so multiple ingredients
	// can share a slot without overcounting.
	remaining := make(map[uint32]int, len(inv))
	for slot, item := range inv {
		remaining[slot] = int(item.Count)
	}

	picks := make([]IngredientPick, 0, len(ingredients))
	for ingredientIndex, ing := range ingredients {
		need := int(ing.Count) * times
		if need <= 0 {
			continue
		}

		targetName, networkID := ResolveIngredientIdentity(ing.Descriptor, itemNames)
		if targetName == "" && networkID == 0 {
			// Non-default/tag/MoLang descriptors that we can't resolve. The
			// server will handle consumption itself for these.
			continue
		}

		var matched []IngredientPick
		if targetName != "" {
			matched, need = consumeMatchingSlots(inv, itemNames, remaining, need, func(itemName string, itemNetID int32) bool {
				return itemName != "" && ItemNameMatches(itemName, targetName)
			})
		} else {
			matched, need = consumeMatchingSlots(inv, itemNames, remaining, need, func(itemName string, itemNetID int32) bool {
				return itemNetID == networkID
			})
		}
		for i := range matched {
			matched[i].IngredientIndex = ingredientIndex
		}
		picks = append(picks, matched...)

		if need > 0 {
			return nil, fmt.Errorf("not enough %s for %d crafts", FormatItemName(targetName), times)
		}
	}
	return picks, nil
}

// ResolveIngredientIdentity extracts a human-readable name and/or a network ID
// from an item descriptor. DefaultItemDescriptor now carries the namespaced item
// identifier instead of a network ID, so the identifier is matched against the
// names the bot knows. ItemTagItemDescriptor returns the tag name. Other
// descriptors return empty values and are treated as server-handled.
func ResolveIngredientIdentity(descriptor protocol.ItemDescriptor, itemNames map[int32]string) (string, int32) {
	switch desc := descriptor.(type) {
	case *protocol.DefaultItemDescriptor:
		if desc.Name == "" {
			return "", 0
		}
		if name, networkID, ok := lookupNameByIdentifier(itemNames, desc.Name); ok {
			return name, networkID
		}
		// The bot does not know this item yet; fall back to the identifier so the
		// caller can still report a meaningful name in errors.
		return desc.Name, 0
	case *protocol.ItemTagItemDescriptor:
		return desc.Tag, 0
	default:
		return "", 0
	}
}

// lookupNameByIdentifier resolves a namespaced identifier to a known item name
// and its network ID. Map iteration order is random, so candidates are collected
// and the lowest network ID wins: that keeps the result stable across calls for
// recipes that reference the same item through more than one runtime ID.
func lookupNameByIdentifier(itemNames map[int32]string, identifier string) (string, int32, bool) {
	var (
		bestName string
		bestID   int32
		found    bool
	)
	for networkID, candidate := range itemNames {
		if !itemNameMatchesIdentifier(candidate, identifier) {
			continue
		}
		if !found || networkID < bestID {
			bestName, bestID, found = candidate, networkID, true
		}
	}
	return bestName, bestID, found
}

// itemNameMatchesIdentifier reports whether an inventory item name refers to the
// namespaced identifier used by item descriptors (minecraft:oak_planks). Both
// sides are normalised, so an item name that already carries the namespace
// prefix still matches the bare identifier from a descriptor.
func itemNameMatchesIdentifier(itemName, identifier string) bool {
	normalise := func(v string) string {
		v, _ = strings.CutPrefix(strings.TrimSpace(v), "minecraft:")
		return strings.ToLower(strings.ReplaceAll(v, " ", "_"))
	}
	return normalise(itemName) == normalise(identifier)
}

// StackRequestItemFromStack converts an item stack into the name-addressed form
// used by craft-result stack request actions. Item stacks are identified by
// network ID rather than name, so the caller supplies the resolved name. Names
// that cannot be described this way are rejected.
func StackRequestItemFromStack(stack protocol.ItemStack, name string) (protocol.StackRequestItem, bool) {
	if name == "" {
		return protocol.StackRequestItem{}, false
	}
	identifier := name
	if !strings.Contains(identifier, ":") {
		identifier = "minecraft:" + identifier
	}
	return protocol.StackRequestItem{
		Identifier:     identifier,
		MetadataValue:  stack.MetadataValue,
		BlockRuntimeID: stack.BlockRuntimeID,
		Count:          stack.Count,
		NBTData:        stack.NBTData,
		CanBePlacedOn:  stack.CanBePlacedOn,
		CanBreak:       stack.CanBreak,
		BlockingTick:   stack.BlockingTick,
	}, true
}

// canonicalItemNames maps recipe ingredient names that differ from the
// inventory item names but represent the same functional item. Bedrock servers
// sometimes label logs as "wood" in recipes while the inventory item is "log".
var canonicalItemNames = map[string]string{
	"oak wood":      "oak log",
	"spruce wood":   "spruce log",
	"birch wood":    "birch log",
	"jungle wood":   "jungle log",
	"acacia wood":   "acacia log",
	"dark oak wood": "dark oak log",
	"dark_oak wood": "dark oak log",
	"mangrove wood": "mangrove log",
	"cherry wood":   "cherry log",
	"pale oak wood": "pale oak log",
	"bamboo wood":   "bamboo log",
}

// normalizeEquivalentName resolves known aliases to a canonical name so that
// recipe ingredients can be matched against the inventory even when the server
// uses different display/runtime names. Underscores and spaces are treated as
// equivalent.
func normalizeEquivalentName(name string) string {
	name = strings.ToLower(strings.TrimPrefix(name, "minecraft:"))
	name = strings.ReplaceAll(name, "_", " ")
	if canon, ok := canonicalItemNames[name]; ok {
		return canon
	}
	return name
}

// ItemNameMatches reports whether an inventory item name satisfies a recipe
// ingredient. It accepts exact matches, prefixed variants ("minecraft:oak_log"
// vs "oak_log"), and shared prefixes (e.g. any "*_log" for a generic "log"
// ingredient). The comparison is case-insensitive.
//
// Plank variants are treated as interchangeable: a recipe requiring
// "warped_planks" is satisfied by "oak_planks" (and vice-versa), since
// Bedrock crafting tables accept any plank type for stick/plank recipes.
func ItemNameMatches(itemName, ingredientName string) bool {
	itemName = normalizeEquivalentName(itemName)
	ingredientName = normalizeEquivalentName(ingredientName)
	if itemName == ingredientName {
		return true
	}
	if genericTagMatch(itemName, ingredientName) || genericTagMatch(ingredientName, itemName) {
		return true
	}
	// Any plank variant satisfies any other plank variant requirement.
	if strings.HasSuffix(itemName, "planks") && strings.HasSuffix(ingredientName, "planks") {
		return true
	}
	return false
}

// genericTagMatch reports whether tag is a single-word generic material tag
// ("log", "planks", "stone") that itemName ends with on a word boundary,
// so a generic "log" ingredient is satisfied by "oak log". The previous
// bidirectional substring containment let "oak log" satisfy a "dark oak log"
// requirement ("dark oak log" contains "oak log"), making the bot craft with
// ingredients it does not own.
func genericTagMatch(itemName, tag string) bool {
	if strings.Contains(tag, " ") {
		return false
	}
	return strings.HasSuffix(itemName, " "+tag)
}

// findFirstEmptyPlayerSlot returns the lowest player-inventory slot (0-35) that
// is empty in the bot's local inventory map.
func findFirstEmptyPlayerSlot(inv map[uint32]protocol.ItemStack) (uint32, bool) {
	for slot := uint32(0); slot < 36; slot++ {
		item, occupied := inv[slot]
		if !occupied || item.Count == 0 {
			return slot, true
		}
	}
	return 0, false
}

// IngredientName returns a server item name (or tag) for a recipe ingredient
// descriptor. Used by chain-crafting to decide which intermediate item to
// craft when an ingredient is missing from the inventory.
func (b *Bot) IngredientName(ing protocol.ItemDescriptorCount) string {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	name, _ := ResolveIngredientIdentity(ing.Descriptor, b.ItemNames)
	return name
}

// CountItemLike counts inventory items whose name matches the given ingredient
// name using the same tolerant rule crafting uses (substring/tag aware).
func (b *Bot) CountItemLike(name string) int {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	total := 0
	for _, item := range b.InventoryMap {
		if item.Count == 0 {
			continue
		}
		itemName := b.ItemNames[item.NetworkID]
		if itemName != "" && ItemNameMatches(itemName, name) {
			total += int(item.Count)
		}
	}
	return total
}

func (b *Bot) GetRecipes() map[string]uint32 {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	// Create a shallow copy to be thread-safe
	copyMap := make(map[string]uint32, len(b.Recipes))
	for k, v := range b.Recipes {
		copyMap[k] = v
	}
	return copyMap
}

// GetRecipeCandidates returns every recipe network ID that produces itemName
// (case-insensitive, with/without the minecraft: prefix). Many items have one
// recipe per wood variant; callers pick whichever candidate they can satisfy.
func (b *Bot) GetRecipeCandidates(itemName string) []uint32 {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	name := strings.ToLower(strings.TrimSpace(itemName))
	if ids, ok := b.RecipeCandidates[name]; ok && len(ids) > 0 {
		out := make([]uint32, len(ids))
		copy(out, ids)
		return out
	}
	if ids, ok := b.RecipeCandidates["minecraft:"+name]; ok && len(ids) > 0 {
		out := make([]uint32, len(ids))
		copy(out, ids)
		return out
	}
	// Fall back to the single Recipes entry when candidates weren't recorded.
	if id, ok := b.Recipes[name]; ok {
		return []uint32{id}
	}
	if id, ok := b.Recipes["minecraft:"+name]; ok {
		return []uint32{id}
	}
	return nil
}

func (b *Bot) GetRecipesByNetID() map[uint32]RecipeInfo {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	copyMap := make(map[uint32]RecipeInfo, len(b.RecipesByNetID))
	for k, v := range b.RecipesByNetID {
		copyMap[k] = v
	}
	return copyMap
}
