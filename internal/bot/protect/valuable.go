package protect

import "strings"

// normalise strips the namespace and lowercases a block name, so the tables
// here are keyed on the bare name the rest of the bot uses.
//
// It is not optional politeness. The same block arrives as "diamond_block" from
// a hard-coded target list, "minecraft:diamond_block" from the world model, and
// "Diamond_Block" from an inventory NBT tag, and a policy that matches only one
// of those forms protects nothing on the other two.
func normalise(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.TrimPrefix(n, "minecraft:")
}

// defaultValuableBlocks is the built-in list. It is a package variable rather
// than a literal inside DefaultValuableBlocks so the copy in New is a copy of
// one source.
var defaultValuableBlocks = []string{
	// Storage. The block is cheap; what is inside it is the run's work.
	"chest", "trapped_chest", "ender_chest", "barrel", "hopper", "dropper",
	"dispenser", "brewing_stand", "furnace", "blast_furnace", "smoker",
	"anvil", "chipped_anvil", "damaged_anvil", "enchanting_table", "beacon",
	"conduit", "lodestone", "respawn_anchor",

	// Mineral blocks — the ore, already smelted and made into a stash.
	"diamond_block", "emerald_block", "netherite_block", "gold_block",
	"iron_block", "copper_block", "coal_block", "lapis_block", "redstone_block",
	"ancient_debris",

	// Books. An enchanted book is the one item in the game that cannot be
	// re-made from what the bot is standing next to.
	"bookshelf", "enchanted_book", "written_book", "writable_book", "lectern",

	// Tools good enough to be worth not losing, and the three items that are
	// not replaceable at all.
	//
	// These are items rather than blocks — nothing in the world is a diamond
	// pickaxe. They are here because the policy matches on names, and a caller
	// that is deciding about an item (a drop, or an entity break added later)
	// gets the same answer a caller deciding about a block does.
	"diamond_pickaxe", "netherite_pickaxe", "diamond_axe", "netherite_axe",
	"diamond_shovel", "netherite_shovel", "diamond_sword", "netherite_sword",
	"diamond_hoe", "netherite_hoe", "shield", "elytra", "totem_of_undying",
}

// valuableSuffixes catch the part of the list nobody keeps up to date.
//
// Bedrock has seventeen shulker boxes, and a server can register a storage
// block this client has never heard the name of. The bet is the same one
// storage.go makes with its container suffixes: the tail is long, the shape is
// regular, and a hand-written list is wrong the day the game updates.
//
// It is deliberately short. A protection policy that over-reaches is a bot that
// stops working, so only suffixes with no innocent block behind them are here —
// "hay_block" and "note_block" and "book" stay breakable, which is why there is
// no _block, _boat or _item rule.
var valuableSuffixes = []string{
	"_shulker_box",
	"_chest",
}

// DefaultValuableBlocks returns a fresh copy of the built-in list.
//
// A copy, because the documented way to extend the list is
// append(DefaultValuableBlocks(), "diamond_ore"), and that must not be able to
// edit the defaults underneath every other Policy in the process.
//
// # Why raw ore is not on it
//
// A mining bot exists to break diamond_ore. A valuable list that contains it
// does not protect anything — it switches the miner off, and the ore the miner
// did break is already gone by the time the policy would have mattered. What is
// worth protecting is the ore after it has been smelted, stored and made into
// diamond_block, which is the block the bot placed itself. A caller who wants
// the raw ore too opts in by extending this list.
func DefaultValuableBlocks() []string {
	return append([]string(nil), defaultValuableBlocks...)
}
