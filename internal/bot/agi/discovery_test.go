package agi

import (
	"strings"
	"testing"
	"time"
)

// The goal catalogue this bot grew up with is a survival catalogue: oak_log,
// chest, bed, night. On a server with custom addons that list is wrong; on a
// single-block world it is entirely empty — no wood, no chest, no night, nothing
// to sleep in. A brain built on it cannot play there, and the failure is
// invisible: the goals filter themselves out, the menu empties, and the bot
// stands still looking broken.
//
// These tests pin the replacement: a vocabulary built from what was actually
// seen, which is what makes the same brain work in a world it has never met.

// TestTheVocabularyIsWhateverTheServerHas is the universality claim. Nothing
// here knows what a survival world contains.
func TestTheVocabularyIsWhateverTheServerHas(t *testing.T) {
	t.Parallel()

	v := NewVocabulary()
	// A world from a server this code has never heard of.
	v.NoteBlock("myaddon:void_crystal")
	v.NoteBlock("myaddon:void_crystal")
	v.NoteBlock("ancient_debris")
	v.NoteItem("myaddon:null_ingot")

	if !v.Has("void_crystal") {
		t.Error("a namespaced block from an unknown addon was not recorded")
	}
	if !v.Has("myaddon:void_crystal") {
		t.Error("the same block recorded with and without its namespace is not one term")
	}
	if !v.Has("null_ingot") {
		t.Error("an item from an unknown addon was not recorded")
	}
	if v.Has("oak_log") {
		t.Error("the vocabulary invented a block nobody saw")
	}
	if n, _ := v.Count("void_crystal"); n != 2 {
		t.Errorf("seen twice but counted %d times", n)
	}
}

// TestNamespacesAndCasingDoNotMultiplyTheWorld. Without normalisation the same
// block arrives three ways and the vocabulary fills with copies of one thing,
// while the model is shown a list that looks like a rich world and is not.
func TestNamespacesAndCasingDoNotMultiplyTheWorld(t *testing.T) {
	t.Parallel()

	v := NewVocabulary()
	for _, name := range []string{"minecraft:Oak_Log", "oak_log", "Minecraft:oak_log", " oak log "} {
		v.NoteBlock(name)
	}
	blocks := v.Blocks()
	if len(blocks) != 1 {
		t.Errorf("recorded %v as %d distinct terms, want 1", blocks, len(blocks))
	}
}

// TestAirIsNotADiscovery. Recording it would put "air" at the top of every list
// and make the model think the world is made of air.
func TestAirIsNotADiscovery(t *testing.T) {
	t.Parallel()

	v := NewVocabulary()
	for _, name := range []string{"minecraft:air", "cave_air", "void_air", "", "  ", "none", "unknown"} {
		v.NoteBlock(name)
	}
	if len(v.Blocks()) != 0 {
		t.Errorf("recorded %v as discoveries", v.Blocks())
	}
	if v.Size() != 0 {
		t.Errorf("size = %d for an empty world, want 0", v.Size())
	}
}

// TestTheMostSeenComesFirst. A block seen once is scenery; the same block seen
// forty times is a resource, and telling the difference is how the bot works out
// what to do without being told.
func TestTheMostSeenComesFirst(t *testing.T) {
	t.Parallel()

	v := NewVocabulary()
	v.NoteBlock("stone")
	v.NoteBlock("dirt")
	for i := 0; i < 9; i++ {
		v.NoteBlock("copper_ore")
	}

	blocks := v.Blocks()
	if len(blocks) != 3 {
		t.Fatalf("got %v, want 3 terms", blocks)
	}
	if blocks[0] != "copper_ore" {
		t.Errorf("first = %q, want the block seen ten times", blocks[0])
	}
}

// TestTheOrderIsStable. A list that reshuffles every tick is unreadable in a
// prompt, and a model reasoning over a list that changed since last time has no
// way to tell a new discovery from a reshuffle.
func TestTheOrderIsStable(t *testing.T) {
	t.Parallel()

	v := NewVocabulary()
	v.NoteBlock("stone")
	for i := 0; i < 5; i++ {
		v.NoteBlock("dirt")
	}
	v.NoteBlock("ore")

	first := strings.Join(v.Blocks(), ",")
	for i := 0; i < 20; i++ {
		if again := strings.Join(v.Blocks(), ","); again != first {
			t.Fatalf("order changed between reads: %q then %q", first, again)
		}
	}
}

// TestTheVocabularyIsCapped. A long recording on a large world sees thousands of
// distinct blocks, mostly variants nobody will ever act on. What matters is that
// the state text stays inside the model's budget: a vocabulary so long it
// crowds out the objective stops being useful at exactly the moment the
// recording gets interesting.
func TestTheVocabularyIsCapped(t *testing.T) {
	t.Parallel()

	v := NewVocabulary()
	for i := 0; i < maxVocabulary*3; i++ {
		v.NoteBlock("block_" + string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}
	if got := len(v.Blocks()); got > maxVocabulary {
		t.Errorf("recorded %d terms, want the cap of %d", got, maxVocabulary)
	}
	// The cap must not be a silent drop of everything: the first terms seen are
	// the ones kept, and they are still queryable.
	if !v.Has("block_aa") {
		t.Error("the cap threw away the first thing the bot ever saw")
	}
}

// TestMergeReadsTheTextTheSnapshotAlreadyBuilt. The world scan has already
// happened by the time a snapshot exists; doing it again to feed the vocabulary
// would cost the bot a full scan on every single tick.
func TestMergeReadsTheTextTheSnapshotAlreadyBuilt(t *testing.T) {
	t.Parallel()

	v := NewVocabulary()
	v.Merge(Snapshot{
		NearBlocks: "stone, dirt, copper_ore",
		VisibleMob: "zombie, skeleton",
		Inventory:  "copper_ingot, bread",
	})

	if !v.Has("copper_ore") {
		t.Error("a block in the summary did not reach the vocabulary")
	}
	if !v.Has("zombie") {
		t.Error("a mob in the summary did not reach the vocabulary")
	}
	if !v.Has("bread") {
		t.Error("an item in the summary did not reach the vocabulary")
	}
	if v.Has("oak_log") {
		t.Error("merge invented something the snapshot did not contain")
	}
}

// TestDescribeSaysSomethingWhenTheWorldIsEmpty. The state text is read on every
// tick; a blank there would be a blank in every prompt, and "nothing observed
// yet" is both shorter and truer than an empty string.
func TestDescribeSaysSomethingWhenTheWorldIsEmpty(t *testing.T) {
	t.Parallel()

	if got := NewVocabulary().Describe(); got != "nothing observed yet" {
		t.Errorf("Describe on an empty vocabulary = %q", got)
	}

	v := NewVocabulary()
	v.NoteBlock("stone")
	v.NoteItem("pickaxe")
	got := v.Describe()
	if !strings.Contains(got, "stone") || !strings.Contains(got, "pickaxe") {
		t.Errorf("Describe = %q, want both the blocks and the carrying", got)
	}
}

// TestTheStateTextCarriesTheVocabulary is the wiring, and it is the whole point.
// A model reasoning about "build a house" is useless unless it also knows there
// is no wood — the goal and the world's contents are read together or not at all.
func TestTheStateTextCarriesTheVocabulary(t *testing.T) {
	t.Parallel()

	v := NewVocabulary()
	v.NoteBlock("netherrack")

	snap := Snapshot{
		HP: 20, Hunger: 20, Coords: "X:0 Y:64 Z:0", Now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		GoalSummary:  "dig down",
		Vocabulary:   v,
		VisibleMob:   "none",
		NearBlocks:   "netherrack",
		VisibleSigns: nil,
	}

	state := describeState(snap)
	if !strings.Contains(state, "netherrack") {
		t.Errorf("the state text does not mention the world's contents:\n%s", state)
	}
	if !strings.Contains(state, "dig down") {
		t.Errorf("the state text lost the goal:\n%s", state)
	}
}
