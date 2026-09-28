package perception

import (
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// These cover the two shapes one scan is rendered into. The scan geometry —
// the vision cone and the line-of-sight walk — is unchanged and is covered
// elsewhere; what is new here is that the name list and the prose summary come
// from the same pass and cannot disagree about what the bot can see.

// scanOf is a hand-built scan, so the rendering can be tested without a world.
func scanOf(terrain map[string]int, order []string) blockScan {
	return blockScan{terrain: terrain, terrainOrder: order}
}

// TestTheNameListIsCommaSeparatedBareNames is the contract. Code in the brain
// counts these, gates on them and matches on them, so each entry has to be a
// single block name that a comma split can separate. The prose rendering joins
// with spaces and wraps the list in a sentence, which is unreadable to all
// three.
func TestTheNameListIsCommaSeparatedBareNames(t *testing.T) {
	t.Parallel()

	scan := scanOf(map[string]int{"grass_block": 12, "oak_log": 3, "stone": 2},
		[]string{"grass_block", "oak_log", "stone"})
	got := visibleBlockNames(scan, 6)

	if strings.Contains(got, " ") {
		t.Errorf("the name list contains a space, so it is not comma-separable: %q", got)
	}
	if strings.ContainsAny(got, "():") {
		t.Errorf("the name list carries rendered detail: %q", got)
	}
	terms := strings.Split(got, ",")
	if len(terms) != 3 {
		t.Fatalf("name list = %q, want 3 distinct names", got)
	}
	// Most-seen first: the thing the bot is standing in a field of is the thing
	// it needs to know about.
	if strings.TrimSpace(terms[0]) != "grass_block" {
		t.Errorf("most-seen block is %q, want grass_block", terms[0])
	}
}

// TestTheNameListIsNotTheProseSummary is the bug, restated at the source. The
// summary is a sentence; feeding it to the brain's rules returned that whole
// sentence as one block name, which counted as a one-block world and pinned the
// bot in place.
func TestTheNameListIsNotTheProseSummary(t *testing.T) {
	t.Parallel()

	scan := scanOf(map[string]int{"grass_block": 12, "oak_log": 3},
		[]string{"grass_block", "oak_log"})

	names := visibleBlockNames(scan, 6)
	text := renderSummary(scan, 6)

	if names == text {
		t.Error("the name list and the summary are identical; the split is not doing anything")
	}
	for _, name := range strings.Split(names, ",") {
		if strings.Contains(text, strings.TrimSpace(name)+"(") {
			continue // present as a count in the prose, which is expected
		}
		if !strings.Contains(text, strings.TrimSpace(name)) {
			t.Errorf("%q is in the name list but not in the summary; they disagree", name)
		}
	}
}

// TestAnEmptyScanSaysNone is what a single-block world and an empty plain both
// look like, and both consumers rely on the exact word.
func TestAnEmptyScanSaysNone(t *testing.T) {
	t.Parallel()

	if got := visibleBlockNames(scanOf(map[string]int{}, nil), 6); got != "none" {
		t.Errorf("an empty scan produced %q, want %q", got, "none")
	}
}

// TestClickablesComeBeforeTerrain is a readability and usefulness rule. A chest
// or a crafting table is the reason to stop, and burying it under a histogram of
// stone hides the one block worth acting on.
func TestClickablesComeBeforeTerrain(t *testing.T) {
	t.Parallel()

	scan := blockScan{
		terrain:      map[string]int{"stone": 40},
		terrainOrder: []string{"stone"},
		clickables: []clickableBlock{
			{name: "chest", pos: protocol.BlockPos{1, 2, 3}, dist: 4, dir: "N"},
		},
	}
	got := visibleBlockNames(scan, 6)
	if !strings.HasPrefix(got, "chest") {
		t.Errorf("name list = %q, want the clickable first", got)
	}
	if !strings.Contains(got, "stone") {
		t.Errorf("name list = %q, want the terrain included", got)
	}
}

// TestOneStructureIsNamedOnce stops a double chest from reading as two chests,
// which is the same class of error as counting a sentence as a block: a count
// that does not match the world.
func TestOneStructureIsNamedOnce(t *testing.T) {
	t.Parallel()

	scan := blockScan{
		terrain: map[string]int{},
		clickables: []clickableBlock{
			{name: "chest", pos: protocol.BlockPos{0, 2, 0}, dist: 2, dir: "N"},
			{name: "chest", pos: protocol.BlockPos{1, 2, 0}, dist: 2, dir: "N"},
		},
	}
	if got := visibleBlockNames(scan, 6); got != "chest" {
		t.Errorf("a double chest produced %q, want one entry", got)
	}
}

// TestTheLimitIsHonoured keeps the prompt small. A negative remainder means the
// clickables already filled the budget, which must yield no terrain rather than
// a slice that panics or repeats.
func TestTheLimitIsHonoured(t *testing.T) {
	t.Parallel()

	scan := blockScan{
		terrain:      map[string]int{"stone": 9, "dirt": 8, "oak_log": 7},
		terrainOrder: []string{"stone", "dirt", "oak_log"},
		clickables: []clickableBlock{
			{name: "chest", pos: protocol.BlockPos{0, 2, 0}, dist: 2, dir: "N"},
		},
	}

	// Limit 1: the chest fits, and no terrain does.
	if got := visibleBlockNames(scan, 1); got != "chest" {
		t.Errorf("limit 1 produced %q, want just the chest", got)
	}
	// Limit 0 means no cap, so everything is named.
	if got := strings.Count(visibleBlockNames(scan, 0), ",") + 1; got != 4 {
		t.Errorf("limit 0 produced %d names, want all 4", got)
	}
}

// TestTrimCannotProduceANegativeSlice is the edge the limit arithmetic can
// reach: clickables already exceed the budget, so the terrain allowance is
// negative.
func TestTrimCannotProduceANegativeSlice(t *testing.T) {
	t.Parallel()

	if got := trim([]string{"a", "b"}, -3); got != nil {
		t.Errorf("trim with a negative allowance = %v, want nil", got)
	}
	if got := trim([]string{"a", "b"}, 1); len(got) != 1 {
		t.Errorf("trim to 1 = %v, want one entry", got)
	}
	if got := trim([]string{"a", "b"}, 5); len(got) != 2 {
		t.Errorf("trim to 5 = %v, want both entries", got)
	}
}
