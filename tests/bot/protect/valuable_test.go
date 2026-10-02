package protect_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/protect"
)

// --- The valuable-block list ---

// TestDefaultValuableBlocksCoverStorageAndTools is the shape of the built-in
// list. These are the blocks whose loss is not recoverable by mining again:
// containers and what is in them, the mineral blocks a stash is made of, the
// books, and the tools good enough to be worth not losing.
func TestDefaultValuableBlocksCoverStorageAndTools(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	for _, name := range []string{
		// storage, and by extension everything inside it
		"chest", "trapped_chest", "ender_chest", "barrel", "dropper", "dispenser",
		"brewing_stand", "furnace", "blast_furnace", "smoker", "anvil",
		"chipped_anvil", "damaged_anvil", "enchanting_table", "beacon",
		"respawn_anchor", "conduit", "lodestone",
		// the mineral blocks a stash is built from
		"diamond_block", "emerald_block", "netherite_block", "gold_block",
		"iron_block", "lapis_block", "redstone_block", "ancient_debris",
		// books
		"bookshelf", "enchanted_book", "written_book", "writable_book", "lectern",
		// tools worth more than the swing that loses them
		"diamond_pickaxe", "netherite_pickaxe", "diamond_axe", "netherite_axe",
		"diamond_shovel", "netherite_shovel", "diamond_sword", "netherite_sword",
		"shield", "elytra", "totem_of_undying",
	} {
		if got := p.Allowed(protect.Break, positionFarFromAnyZone(), name); got.OK {
			t.Errorf("%q was breakable by default", name)
		}
	}
}

// TestDefaultValuablesAreBreakableByAConfigThatSaysSo keeps the list from
// calcifying into "unbreakable forever". Every entry is a decision, and a
// decision the caller can overrule is a decision rather than a constant.
func TestDefaultValuablesAreBreakableByAConfigThatSaysSo(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{ValuableBlocks: []string{}})

	for _, name := range []string{"diamond_block", "chest", "diamond_pickaxe", "bookshelf"} {
		if got := p.Allowed(protect.Break, positionFarFromAnyZone(), name); !got.OK {
			t.Errorf("%q stayed protected after the list was cleared: %s", name, got.Reason)
		}
	}
}

// TestValuableNamesAreNormalised. Callers get block names from several places
// and not all of them bother with the namespace or the case: the world model
// hands back "minecraft:diamond_block", inventories hand back "Diamond_Block",
// and a policy that only matches the bare lowercase form protects nothing on
// either of those.
func TestValuableNamesAreNormalised(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	for _, name := range []string{
		"diamond_block",
		"minecraft:diamond_block",
		"minecraft:Diamond_Block",
		"  DIAMOND_BLOCK  ",
	} {
		if got := p.Allowed(protect.Break, positionFarFromAnyZone(), name); got.OK {
			t.Errorf("%q was not recognised as the same block", name)
		}
	}
}

// TestValuableSuffixRulesCatchTheLongTail. Bedrock has seventeen shulker boxes
// and an unbounded supply of server-registered storage, and no hand-written
// list stays current. The suffix rules are the same bet storage.go makes.
func TestValuableSuffixRulesCatchTheLongTail(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	for _, name := range []string{
		"undyed_shulker_box", "purple_shulker_box", "light_gray_shulker_box",
		"minecraft:orange_shulker_box", "wooden_chest", "iron_chest",
		"netherite_block",
	} {
		if got := p.Allowed(protect.Break, positionFarFromAnyZone(), name); got.OK {
			t.Errorf("%q was breakable by default", name)
		}
	}
}

// TestSuffixRulesDoNotOverreach guards the other side, because a protection
// policy that is too eager is just a bot that stops working. "sponge" is not a
// mineral block, "goldfish" is not a gold block, and neither may be refused.
func TestSuffixRulesDoNotOverreach(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	for _, name := range []string{
		"sponge", "wet_sponge", "hay_block", "bed_block", "goldfish",
		"tnt", "note_block", "crafting_table", "book", "chiseled_bookshelf",
		"oak_log", "stone", "cobblestone", "dirt", "sand", "gravel", "iron_bars",
	} {
		if got := p.Allowed(protect.Break, positionFarFromAnyZone(), name); !got.OK {
			t.Errorf("%q was refused by a suffix rule: %s", name, got.Reason)
		}
	}
}

// TestRawOreIsNotValuableByDefault is a deliberate omission, so it is pinned
// rather than left to be discovered.
//
// A mining bot exists to break ore. A valuable-block list containing
// diamond_ore does not protect anything — it switches the miner off, and the
// blocks the miner did break are already gone by then. What is worth protecting
// is the ore *after* it has been smelted and stored, which is the mineral block
// the bot put down itself. A caller who wants the raw ore as well opts in by
// extending the list.
func TestRawOreIsNotValuableByDefault(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	for _, name := range []string{"diamond_ore", "deepslate_diamond_ore", "gold_ore", "iron_ore", "coal_ore"} {
		if got := p.Allowed(protect.Break, positionFarFromAnyZone(), name); !got.OK {
			t.Errorf("%q is protected by default, which would stop the miner: %s", name, got.Reason)
		}
	}
}

// TestValuableBlocksMayStillBeBuilt. Placing a diamond block in your own chest
// is the entire point of having one.
func TestValuableBlocksMayStillBeBuilt(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	got := p.Allowed(protect.Build, positionFarFromAnyZone(), "diamond_block")
	if !got.OK {
		t.Errorf("building a diamond block was refused: %s", got.Reason)
	}
}

// TestDefaultValuableBlocksIsACopy. A caller who wants to extend the list must
// be able to append to the result without editing the defaults underneath
// every other policy in the process.
func TestDefaultValuableBlocksIsACopy(t *testing.T) {
	t.Parallel()

	first := protect.DefaultValuableBlocks()
	first[0] = "mutated"

	if protect.DefaultValuableBlocks()[0] == "mutated" {
		t.Error("DefaultValuableBlocks handed out its own backing array")
	}
}

// TestExtendingTheDefaultsIsTheDocumentedWayToAddRawOre is the counterpart to
// TestRawOreIsNotValuableByDefault: the opt-in has to actually work.
func TestExtendingTheDefaultsIsTheDocumentedWayToAddRawOre(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{
		ValuableBlocks: append(protect.DefaultValuableBlocks(), "diamond_ore"),
	})

	if got := p.Allowed(protect.Break, positionFarFromAnyZone(), "diamond_ore"); got.OK {
		t.Error("an explicitly added raw ore was still breakable")
	}
	if got := p.Allowed(protect.Break, positionFarFromAnyZone(), "diamond_block"); got.OK {
		t.Error("extending the list dropped the defaults")
	}
}

// TestIsValuableIsAQueryAndNotAPolicy. Callers that need to sort a list of
// blocks — a storage sweep deciding what to pick up — need the same answer the
// policy uses, and should not have to fake a position to ask.
func TestIsValuableIsAQueryAndNotAPolicy(t *testing.T) {
	t.Parallel()

	p := protect.New(protect.Config{})

	if !p.IsValuable("minecraft:chest") {
		t.Error("IsValuable said a chest is not valuable")
	}
	if p.IsValuable("dirt") {
		t.Error("IsValuable said dirt is valuable")
	}
	if p.IsValuable("") {
		t.Error("IsValuable said the empty string is valuable")
	}
}

// TestActionNamesArePrintable. Actions end up in logs and in Reason strings, so
// an Action that formats as its number is a Decision that reads as a bug report
// about the policy rather than about the block.
func TestActionNamesArePrintable(t *testing.T) {
	t.Parallel()

	for action, want := range map[protect.Action]string{
		protect.Break: "break",
		protect.Build: "build",
	} {
		if got := action.String(); got != want {
			t.Errorf("Action(%d).String() = %q, want %q", action, got, want)
		}
	}

	got := homePolicy(t).Allowed(protect.Build, positionInsideHome(), "cobblestone")
	if !strings.Contains(got.Reason, "build") {
		t.Errorf("reason %q does not name the action that was refused", got.Reason)
	}
}
