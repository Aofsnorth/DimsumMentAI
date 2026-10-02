package agi_test

import (
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
)

// The block summary the bot actually receives is a comma-separated list of
// names, because that is what perception.VisibleBlocks renders. The tests in
// this package predate that and hand-build snapshots with a bare name, which
// means the format contract was never under test at all.
//
// That is not a neutral gap. Every rule below reads block names, and for a
// while the one production value they were fed was a rendered sentence —
// "Clickable: none. Terrain: grass_block(12) oak_log(3)" — with prose around a
// space-separated list. A comma split returns that whole sentence as ONE block
// name, so the bot counted one block in reach, concluded it was standing on a
// one-block world, and mined the block under it in place forever without ever
// walking anywhere. The tests passed throughout, because the tests were not
// using the production format.

// renderedSummary is the prose form, kept here so the tests below state exactly
// what the bot must survive.
const renderedSummary = "Clickable: none. Terrain: grass_block(12) oak_log(3) stone(2)"

// TestTheRenderedSummaryIsNotOneBlock is the bug itself. A one-block world has
// one kind of block in view. This summary has three, and counting it as one is
// what froze the bot in place.
func TestTheRenderedSummaryIsNotOneBlock(t *testing.T) {
	t.Parallel()

	if got := agi.DetectOneBlock(renderedSummary); got != agi.NotOneBlock {
		t.Fatalf("a world with three kinds of block in view read as %v, want not one block", got)
	}
}

// TestTheRenderedSummaryNeverCountsAsASingleBlock is the belt to the braces
// above: even confirmed twice, a rendered sentence must not produce a
// single-block world, because that is the state the bot then never leaves.
func TestTheRenderedSummaryNeverCountsAsASingleBlock(t *testing.T) {
	t.Parallel()

	first := agi.DetectOneBlock(renderedSummary)
	if got := agi.ConfirmOneBlock(first, first); got == agi.DefinitelyOneBlock {
		t.Fatal("a rendered block summary confirmed a single-block world")
	}
}

// TestProseIsNotABlockName is the root cause, named directly. A term carrying
// prose, a count or a bearing is a rendered sentence, and no rule that reasons
// about block names may treat it as one.
func TestProseIsNotABlockName(t *testing.T) {
	t.Parallel()

	// Bare names survive; rendered sentences do not.
	for _, term := range []string{"oak_log", "crafting_table", "dirt"} {
		if !agi.IsBareTerm(term) {
			t.Errorf("isBareTerm(%q) = false, want true", term)
		}
		if got := agi.SplitList(term); len(got) != 1 || got[0] != term {
			t.Errorf("splitList(%q) = %v, want one term", term, got)
		}
	}
	for _, term := range []string{
		"Clickable: none. Terrain: grass_block(12)",
		"chest (4m N)",
		"grass_block(12) oak_log(3)",
	} {
		if agi.IsBareTerm(term) {
			t.Errorf("isBareTerm(%q) = true, want false", term)
		}
		if got := agi.SplitList(term); len(got) != 0 {
			t.Errorf("splitList(%q) = %v, want it dropped", term, got)
		}
	}
}

// TestAnUnreadableSummaryIsNotAnEmptyWorld is the second half of the fix, and
// the half that actually unfreezes the bot. Dropping a rendered sentence leaves
// nothing behind, and "nothing in view" is a hint toward a one-block world that
// confirms on the next reading. An unreadable summary has to read as no evidence
// at all, or the bot walks back into the state it was stuck in.
func TestAnUnreadableSummaryIsNotAnEmptyWorld(t *testing.T) {
	t.Parallel()

	names, readable := agi.ReadableBlockNames(renderedSummary)
	if readable {
		t.Errorf("a rendered sentence was accepted as a list of %d names: %v", len(names), names)
	}

	// A genuine empty world is still an honest absence, and stays a hint.
	names, readable = agi.ReadableBlockNames("none")
	if !readable {
		t.Error(`"none" was treated as unreadable; it is the honest empty case`)
	}
	if len(names) != 0 {
		t.Errorf(`"none" yielded names %v`, names)
	}
	if got := agi.DetectOneBlock("none"); got != agi.PossiblyOneBlock {
		t.Errorf("an empty world read as %v, want a hint", got)
	}
}

// TestARealSummaryCountsItsNames is the other direction: the format the bot
// genuinely receives must still work. A guard that rejects everything would pass
// the test above and leave the bot blind.
func TestARealSummaryCountsItsNames(t *testing.T) {
	t.Parallel()

	if got := agi.DetectOneBlock("grass_block, oak_log, stone"); got != agi.NotOneBlock {
		t.Fatalf("a three-block world read as %v, want not one block", got)
	}
	if got := agi.DetectOneBlock("grass_block"); got != agi.PossiblyOneBlock {
		t.Fatalf("a one-block world read as %v, want a hint", got)
	}
}

// TestTheCurriculumDoesNotOfferWorkInAnEmptyWorld is the visible symptom. With
// nothing in view, "gather wood" and "go mine" cannot succeed, and offering
// them is how a bot spends ten minutes failing the same way.
func TestTheCurriculumDoesNotOfferWorkInAnEmptyWorld(t *testing.T) {
	t.Parallel()

	empty := agi.Snapshot{HP: 20, Hunger: 20, FreeSlots: 36, NearBlocks: "none"}
	for _, activity := range agi.Curriculum(empty) {
		if activity == "gather" || activity == "mine" {
			t.Errorf("offered %q in a world with no blocks in view", activity)
		}
	}
}

// TestTheCurriculumOffersWorkWhenBlocksAreVisible is the other side: the gate
// must open when there is genuinely something to gather.
func TestTheCurriculumOffersWorkWhenBlocksAreVisible(t *testing.T) {
	t.Parallel()

	world := agi.Snapshot{HP: 20, Hunger: 20, FreeSlots: 36, NearBlocks: "oak_log, dirt"}
	if got := agi.Curriculum(world); !hasActivity(got, "gather") {
		t.Errorf("did not offer gathering with logs and dirt in view: %v", got)
	}
}

// TestTheVocabularyRecordsNamesAndNotSentences is the fourth consumer, and the
// one that poisons the model prompt. A vocabulary holding
// "Clickable: none. Terrain: ..." is a vocabulary describing a world that does
// not exist, and the model is shown it every tick.
func TestTheVocabularyRecordsNamesAndNotSentences(t *testing.T) {
	t.Parallel()

	v := agi.NewVocabulary()
	v.Merge(agi.Snapshot{NearBlocks: renderedSummary})

	if v.Has("Clickable: none. Terrain: grass_block(12) oak_log(3) stone(2)") {
		t.Error("the rendered sentence was recorded as a block name")
	}
	if v.Has("grass_block(12)") {
		t.Error("a block name carrying a count was recorded")
	}
}

// TestTheVocabularyRecordsTheFormatItActuallyReceives is the other half. Merge is
// handed a comma-separated name list in production, and that has to land as
// individual names — a guard that rejected everything would pass the test above
// and leave the model with a world containing nothing at all.
func TestTheVocabularyRecordsTheFormatItActuallyReceives(t *testing.T) {
	t.Parallel()

	v := agi.NewVocabulary()
	v.Merge(agi.Snapshot{NearBlocks: "grass_block, oak_log, stone"})

	for _, name := range []string{"grass_block", "oak_log", "stone"} {
		if !v.Has(name) {
			t.Errorf("%q was not recorded; vocabulary is %q", name, v.Describe())
		}
	}
}

// TestAGoalIsNotOfferedInAnEmptyWorld covers the goal filter, which had the
// same non-empty test and the same consequence.
func TestAGoalIsNotOfferedInAnEmptyWorld(t *testing.T) {
	t.Parallel()

	goals := agi.GoalsFor(agi.AvailableGoals(agi.Snapshot{HP: 20, Hunger: 20, FreeSlots: 36, NearBlocks: "none"}))
	for _, name := range goals {
		if name == "gather_wood" || name == "stock_up" {
			t.Errorf("offered goal %q with nothing to gather", name)
		}
	}
}

// TestAPromptAsksForRealWork is the whole point of the episode: the planner is
// told the goal, and a plan built from a vocabulary of one invented block is a
// plan to do nothing.
func TestAPromptAsksForRealWork(t *testing.T) {
	t.Parallel()

	r := newGoalRunner(t, "build a house", 0)
	snap := agi.Snapshot{
		HP: 20, Hunger: 20, Now: time.Now(),
		Coords:         "X:0 Y:64 Z:0",
		NearBlocks:     renderedSummary,
		NearBlocksText: renderedSummary,
		VisibleMob:     "none",
		Inventory:      "Oak Log x17",
	}
	r.Vocabulary().Merge(snap)

	message := r.PlannerMessageFor(snap)
	if !strings.Contains(message, renderedSummary) {
		t.Errorf("the planner was not shown the readable block scan:\n%s", message)
	}
	if strings.Contains(r.Vocabulary().Describe(), "Clickable:") {
		t.Errorf("the vocabulary describes a world made of sentences: %s", r.Vocabulary().Describe())
	}
}
