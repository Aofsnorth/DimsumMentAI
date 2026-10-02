// Package bucket fills and empties buckets, and fills and drains cauldrons.
//
// A bucket is the clearest case in this codebase of an action that used to be
// reported without ever happening: the milk, tame, and feed routines all
// clicked something and claimed a result. A bucket interaction has two visible
// consequences and this package waits for both:
//
//   - the held item changes — a bucket becomes a water bucket, and back again;
//     this arrives through the server's inventory update, which is to say the
//     ItemStackResponse path the roadmap names;
//   - the target block changes — the water is gone from where it was scooped,
//     or it appears where it was poured.
//
// The cauldron is the same idea with a subtlety that matters: Bedrock gives
// every cauldron state the block name "minecraft:cauldron". The level and the
// liquid live in the block state's properties, and *bot.Bot exposes names only.
// So a level change is confirmed here as a *block state* change — the network
// ID moves, which is real — and the level itself is read only when a
// BlockStateSource is wired. See the note on that interface.
package bucket

import (
	"strings"
)

// BucketKind is what is in the bucket.
type BucketKind int

// The bucket kinds. BucketUnknown is deliberately a real value rather than
// "false": a caller has to be able to tell "this is not a bucket" from "this is
// an empty bucket", because only the second one can be filled.
const (
	BucketUnknown BucketKind = iota
	BucketEmpty
	BucketWater
	BucketLava
	BucketPowderSnow
	BucketFish
	BucketAxolotl
	BucketMilk
)

// bucketItemNames is the item name each kind is carried as. Fish has four
// variants in Bedrock; they behave identically for filling and pouring, so they
// collapse to one kind and the manager matches on the kind rather than trying
// to guess which fish the player happened to catch.
var bucketItemNames = map[BucketKind][]string{
	BucketUnknown:    nil,
	BucketEmpty:      {"bucket"},
	BucketWater:      {"water_bucket"},
	BucketLava:       {"lava_bucket"},
	BucketPowderSnow: {"powder_snow_bucket"},
	BucketFish:       {"cod_bucket", "salmon_bucket", "tropical_fish_bucket", "pufferfish_bucket"},
	BucketAxolotl:    {"axolotl_bucket", "tadpole_bucket"},
	BucketMilk:       {"milk_bucket"},
}

// fillableBlocks maps a block name fragment to what scooping it up yields.
var fillableBlocks = []struct {
	token string
	kind  BucketKind
}{
	{"bubble_column", BucketWater},
	{"powder_snow", BucketPowderSnow},
	{"water", BucketWater},
	{"lava", BucketLava},
}

// fillableEntities maps an entity type to what catching it in a bucket yields.
var fillableEntities = []struct {
	token string
	kind  BucketKind
}{
	{"axolotl", BucketAxolotl},
	{"tropical_fish", BucketFish},
	{"pufferfish", BucketFish},
	{"tadpole", BucketFish},
	{"cod", BucketFish},
	{"salmon", BucketFish},
}

// pourableBlocks are the things a fluid bucket can be poured onto: the empty
// space where a block is not, and the cauldron.
//
// It is a short list on purpose. A bucket poured onto stone flows away and
// leaves the world unchanged, so "the click went out" is not "the water is
// there" — the bot would be pouring into a wall.
var pourableBlocks = []string{
	"air", "cave_air", "void_air", "structure_void",
	"cauldron", "short_grass", "tall_grass", "grass", "fern", "dead_bush",
	"snow", "snow_layer", "powder_snow", "seagrass", "kelp", "kelp_plant",
	"vine", "lily_pad", "reeds", "sugar_cane", "bamboo", "sapling",
	"dandelion", "poppy", "cornflower", "allium", "azalea", "flowering_azalea",
	"torchflower", "pitcher_plant", "big_dripleaf", "small_dripleaf",
	"spore_blossom", "cave_vines", "hanging_roots", "glow_berries",
	"sea_pickle", "coral", "coral_fan", "coral_block", "dead_coral_block",
}

// String names the kind for logs and error messages.
func (k BucketKind) String() string {
	switch k {
	case BucketEmpty:
		return "empty bucket"
	case BucketWater:
		return "water bucket"
	case BucketLava:
		return "lava bucket"
	case BucketPowderSnow:
		return "powder snow bucket"
	case BucketFish:
		return "fish bucket"
	case BucketAxolotl:
		return "axolotl bucket"
	case BucketMilk:
		return "milk bucket"
	default:
		return "unknown bucket"
	}
}

// ItemName returns the canonical item name for the kind, or the empty string
// for a kind that is not a bucket.
func (k BucketKind) ItemName() string {
	names := bucketItemNames[k]
	if len(names) == 0 {
		return ""
	}
	return "minecraft:" + names[0]
}

// IsFilled reports whether the bucket has something in it.
func (k BucketKind) IsFilled() bool {
	return k != BucketEmpty && k != BucketUnknown
}

// CanBePoured reports whether the contents can be put down.
//
// Milk is full and is not pourable, and the distinction is the whole point: a
// routine that treats "filled" as "pourable" clicks a cow with a milk bucket
// forever.
func (k BucketKind) CanBePoured() bool {
	switch k {
	case BucketWater, BucketLava, BucketPowderSnow, BucketFish, BucketAxolotl:
		return true
	default:
		return false
	}
}

// ClassifyBucket reports what a bucket item is holding.
//
// An exact match on the item name, not a substring search. The old
// isEmptyBucket helper matched "contains bucket" and had to enumerate every
// filled variant to exclude it; this table already knows all of them, and an
// unrecognised name is reported as not-a-bucket rather than guessed at.
func ClassifyBucket(name string) BucketKind {
	n := normalise(name)
	if n == "" {
		return BucketUnknown
	}
	for kind, names := range bucketItemNames {
		if kind == BucketUnknown {
			continue
		}
		for _, want := range names {
			if n == want {
				return kind
			}
		}
	}
	return BucketUnknown
}

// FillSource is a thing a bucket can be filled from: a block, or a mob.
//
// It is a struct rather than a bare name because the two cases have genuinely
// different rules. Water, lava, and powder snow come from blocks; fish and
// axolotls come from entities; and a bucket cannot be filled from a block that
// happens to be named "cod".
type FillSource struct {
	// Block is the block name at the target, if the target is a block.
	Block string
	// Entity is the entity type at the target, if the target is an entity.
	Entity string
}

// BlockSource builds a fill source from a block name.
func BlockSource(name string) FillSource { return FillSource{Block: name} }

// EntitySource builds a fill source from an entity type.
func EntitySource(name string) FillSource { return FillSource{Entity: name} }

// IsEntity reports whether the source is a mob rather than a block.
func (s FillSource) IsEntity() bool { return s.Entity != "" }

// Label names the source for a log line or an error message.
func (s FillSource) Label() string {
	if s.IsEntity() {
		return normalise(s.Entity)
	}
	return normalise(s.Block)
}

// BucketFillPlan reports which bucket a target yields, and whether it yields
// anything at all.
//
// The false half matters as much as the true half. "Not found" is the honest
// answer for a stone wall, a cow, and the sky, and returning a plan for any of
// them is how a bot ends up clicking a wall with a bucket and reporting a fill.
func BucketFillPlan(src FillSource) (BucketKind, bool) {
	if src.IsEntity() {
		n := normalise(src.Entity)
		for _, f := range fillableEntities {
			if strings.Contains(n, f.token) {
				return f.kind, true
			}
		}
		return BucketUnknown, false
	}

	n := normalise(src.Block)
	if n == "" {
		return BucketUnknown, false
	}
	for _, f := range fillableBlocks {
		if strings.Contains(n, f.token) {
			return f.kind, true
		}
	}
	return BucketUnknown, false
}

// BucketEmptyPlan reports whether a filled bucket may be emptied at a target.
//
// It returns the reason as well as the verdict, because "you cannot pour lava
// onto a cow" is a much more useful log line than "false".
func BucketEmptyPlan(kind BucketKind, at FillSource) (bool, string) {
	if !kind.CanBePoured() {
		return false, kind.String() + " tidak bisa dituang"
	}
	if at.IsEntity() {
		return false, kind.String() + " tidak bisa dituang ke makhluk"
	}

	n := normalise(at.Block)
	if n == "" {
		return false, "tidak tahu apa yang ada di sana"
	}

	// Living contents need water. A fish dropped on dry land suffocates, and in
	// Bedrock the release is rejected outright.
	if kind == BucketFish || kind == BucketAxolotl {
		if strings.Contains(n, "water") || isCauldronName(n) {
			return true, ""
		}
		return false, kind.String() + " hanya bisa dilepas di air"
	}

	if isCauldronName(n) {
		return true, ""
	}
	for _, want := range pourableBlocks {
		if n == want {
			return true, ""
		}
	}
	return false, "tidak ada ruang kosong di " + n
}

// normalise lower-cases a name, strips the namespace, and folds spaces to
// underscores so "Powder Snow Bucket" and "powder_snow_bucket" are one item.
func normalise(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimPrefix(n, "minecraft:")
	return strings.ReplaceAll(n, " ", "_")
}
