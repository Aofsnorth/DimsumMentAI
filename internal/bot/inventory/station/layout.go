package station

import "github.com/sandertv/gophertunnel/minecraft/protocol"

// Container IDs for each station's slots.
//
// These are not the window ID the server assigned, and using the window ID here
// is the bug this file exists to prevent. A server resolves the ContainerID on a
// StackRequestSlotInfo against these constants, and an ID it does not recognise
// is refused — so a transfer addressed by window ID fails while the code around
// it reads as though it worked. A chest is the one case where the two coincide,
// which is why the chest helpers can keep using the window ID.
const (
	// BrewContainerID addresses the three bottle slots and the ingredient slot.
	// A brewed potion comes back to its bottle, so the take uses this too.
	BrewContainerID = byte(protocol.ContainerBrewingStandInput)

	// BrewFuelContainerID is separate from the bottles: blaze powder is not a
	// bottle and the server will not accept it in one.
	BrewFuelContainerID = byte(protocol.ContainerBrewingStandFuel)

	// AnvilContainerID addresses the tool being repaired or renamed.
	AnvilContainerID = byte(protocol.ContainerAnvilInput)

	// AnvilMaterialContainerID addresses the second input: the repair material,
	// or a name tag. It is a different container from the first, which is why
	// both anvil slots cannot share one ID.
	AnvilMaterialContainerID = byte(protocol.ContainerAnvilMaterial)

	// AnvilOutputContainerID addresses the result the server computes.
	AnvilOutputContainerID = byte(protocol.ContainerAnvilResultPreview)

	// GrindstoneContainerID addresses the item being stripped.
	GrindstoneContainerID = byte(protocol.ContainerGrindstoneInput)

	// GrindstoneOutputContainerID addresses the stripped result.
	GrindstoneOutputContainerID = byte(protocol.ContainerGrindstoneResultPreview)

	// EnchantContainerID addresses the item in the table.
	EnchantContainerID = byte(protocol.ContainerEnchantingInput)

	// EnchantMaterialContainerID addresses the lapis.
	EnchantMaterialContainerID = byte(protocol.ContainerEnchantingMaterial)
)

// Brewing stand window layout. Bedrock numbers a brewing stand as three bottle
// slots, one ingredient slot, and one fuel slot. Writing the nether wart into a
// bottle slot does nothing at all, so the layout lives in named constants that
// the tests pin.
const (
	// BrewBottleSlot0 is the first of the three bottle slots; the others are
	// this one plus one and two.
	BrewBottleSlot0 uint32 = 0

	// BrewBottleSlotCount is how many bottles a brewing stand holds at once.
	BrewBottleSlotCount uint32 = 3

	// BrewIngredientSlot takes the nether wart. The server consumes it and
	// rewrites the bottles; nothing is returned to this slot.
	BrewIngredientSlot uint32 = 3

	// BrewFuelSlot takes the blaze powder.
	BrewFuelSlot uint32 = 4

	// BrewSlotCount is the size of the whole window.
	BrewSlotCount uint32 = 5
)

// Anvil window layout: input 1, input 2 (the repair material or the name tag),
// and the output the server computes.
const (
	AnvilInputSlot    uint32 = 0
	AnvilMaterialSlot uint32 = 1
	AnvilOutputSlot   uint32 = 2
	AnvilSlotCount    uint32 = 3
)

// Grindstone window layout: the item to strip, and the stripped result.
const (
	GrindstoneInputSlot  uint32 = 0
	GrindstoneOutputSlot uint32 = 1
	GrindstoneSlotCount  uint32 = 2
)

// Enchanting table layout. The table does not open a normal window, but it
// still addresses its contents by slot through the special input and material
// containers, and a tool anywhere but slot 0 silently enchants nothing.
const (
	EnchantInputSlot    uint32 = 0
	EnchantMaterialSlot uint32 = 1
)

// BrewBottleSlot maps a zero-based bottle index to its window slot, and reports
// false for anything that is not a bottle slot. The guard matters: handing out
// the ingredient or fuel slot as a bottle is how a stand silently refuses to
// brew.
func BrewBottleSlot(index int) (uint32, bool) {
	if index < 0 || uint32(index) >= BrewBottleSlotCount {
		return 0, false
	}
	return BrewBottleSlot0 + uint32(index), true
}

// IsBrewingStandBlock reports whether a block name is a brewing stand.
func IsBrewingStandBlock(name string) bool {
	return normalizeBlockName(name) == "brewing_stand"
}

// IsEnchantingTableBlock reports whether a block name is an enchanting table.
func IsEnchantingTableBlock(name string) bool {
	return normalizeBlockName(name) == "enchanting_table"
}

// IsAnvilBlock reports whether a block name is an anvil at any damage level. A
// bot that only knows "anvil" will skip the chipped and half-repaired ones
// sitting on the ground, which are the ones worth using.
func IsAnvilBlock(name string) bool {
	switch normalizeBlockName(name) {
	case "anvil", "chipped_anvil", "damaged_anvil":
		return true
	}
	return false
}

// IsGrindstoneBlock reports whether a block name is a grindstone.
func IsGrindstoneBlock(name string) bool {
	return normalizeBlockName(name) == "grindstone"
}
