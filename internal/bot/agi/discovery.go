// What this server actually has.
//
// The goal catalogue this bot grew up with is a survival catalogue: oak_log,
// chest, bed, night, mobs. On a server with custom addons that list is either
// wrong or empty, and on a single-block world it is entirely empty — there is no
// wood, no chest, no night, and nothing to sleep in. A brain built on that
// catalogue cannot play there, and the failure is invisible: the goals filter
// themselves out, the menu empties, and the bot stands still looking broken.
//
// So the bot keeps its own vocabulary instead, built from what it has actually
// seen. It is the difference between a brain that knows a fixed world and one
// that walks into a room.
//
// The rule throughout is that seeing is not knowing. A block in view is a fact;
// what it is FOR is a guess, and the guesses go to the model rather than into
// the catalogue. This file only records what is there and hands it over.

package agi

import (
	"sort"
	"strings"
	"sync"
)

// MaxVocabulary caps how many names are remembered per kind.
//
// A long recording on a large world sees thousands of distinct blocks — mostly
// variants nobody will ever act on. The cap keeps the state text small enough to
// stay inside the model's budget, which matters more than completeness: a
// vocabulary so long it crowds out the objective is a vocabulary that stops
// being useful at exactly the moment the recording gets interesting.
const MaxVocabulary = 48

// Vocabulary is what the bot has observed this session.
type Vocabulary struct {
	mu     sync.Mutex
	blocks map[string]int
	items  map[string]int
	// order preserves first-sight order, which is the order a human would list
	// them in and a much better prompt than alphabetical noise.
	blockOrder []string
	itemOrder  []string
}

// NewVocabulary returns an empty vocabulary.
func NewVocabulary() *Vocabulary {
	return &Vocabulary{
		blocks: make(map[string]int),
		items:  make(map[string]int),
	}
}

// normaliseTerm is what every name goes through before it is recorded.
//
// Without it the same block arrives as "minecraft:oak_log" and "oak_log" and
// "Oak Log" and the vocabulary fills with three copies of one thing while the
// model is shown a list that looks like a rich world and is not. Spaces become
// underscores for the same reason and for the same reason interact.normalise
// does it: item display names are the most human-readable form of a term and
// they are also the least machine-comparable.
func normaliseTerm(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return strings.ReplaceAll(strings.TrimSpace(name), " ", "_")
}

// NoteBlock records a block the bot can see.
func (v *Vocabulary) NoteBlock(name string) {
	v.note(v.blocks, &v.blockOrder, name)
}

// NoteItem records an item the bot is carrying.
func (v *Vocabulary) NoteItem(name string) {
	v.note(v.items, &v.itemOrder, name)
}

func (v *Vocabulary) note(into map[string]int, order *[]string, name string) {
	term := normaliseTerm(name)
	if term == "" || term == "none" || term == "unknown" {
		return
	}
	// Air is not a discovery. Recording it would put "air" at the top of every
	// list and make the model think the world is made of air.
	if term == "air" || term == "cave_air" || term == "void_air" {
		return
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if _, known := into[term]; known {
		into[term]++
		return
	}
	if len(into) >= MaxVocabulary {
		return
	}
	into[term] = 1
	*order = append(*order, term)
}

// Blocks returns the distinct block names seen, most-seen first.
func (v *Vocabulary) Blocks() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return rank(v.blocks, v.blockOrder)
}

// Items returns the distinct item names seen, most-seen first.
func (v *Vocabulary) Items() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return rank(v.items, v.itemOrder)
}

// rank orders names by how often they were seen, falling back to first-sight
// order so the result is stable and a term is never silently reordered between
// two ticks — a list that reshuffles every tick is unreadable in a prompt.
func rank(counts map[string]int, order []string) []string {
	out := make([]string, len(order))
	copy(out, order)
	sort.SliceStable(out, func(i, j int) bool {
		return counts[out[i]] > counts[out[j]]
	})
	return out
}

// Count reports how many times a term was seen, and whether it was seen at all.
func (v *Vocabulary) Count(name string) (int, bool) {
	term := normaliseTerm(name)
	v.mu.Lock()
	defer v.mu.Unlock()
	n, ok := v.blocks[term]
	if !ok {
		n, ok = v.items[term]
	}
	return n, ok
}

// Has reports whether the bot has ever seen a term.
func (v *Vocabulary) Has(name string) bool {
	_, ok := v.Count(name)
	return ok
}

// Size is how many distinct terms are known, for the state text.
func (v *Vocabulary) Size() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.blocks) + len(v.items)
}

// Describe renders the vocabulary for a prompt.
//
// The counts are the useful part, not the names. A block seen once is scenery;
// the same block seen forty times is a resource, and telling the difference is
// how the bot works out what to do without being told.
func (v *Vocabulary) Describe() string {
	if v == nil {
		return "nothing observed yet"
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	blocks := rank(v.blocks, v.blockOrder)
	items := rank(v.items, v.itemOrder)

	var sb strings.Builder
	if len(blocks) == 0 && len(items) == 0 {
		return "nothing observed yet"
	}
	if len(blocks) > 0 {
		sb.WriteString("blocks here: ")
		sb.WriteString(strings.Join(blocks, ", "))
	}
	if len(items) > 0 {
		if sb.Len() > 0 {
			sb.WriteString(". ")
		}
		sb.WriteString("carrying: ")
		sb.WriteString(strings.Join(items, ", "))
	}
	return sb.String()
}

// Merge folds a snapshot's block and item text into the vocabulary.
//
// The input is the same comma-joined text the state description already builds,
// rather than a fresh world scan, because the scan has already happened and
// doing it twice per tick is a waste the bot pays for on every single decision.
func (v *Vocabulary) Merge(snap Snapshot) {
	for _, name := range SplitList(snap.NearBlocks) {
		v.NoteBlock(name)
	}
	for _, name := range SplitList(snap.VisibleMob) {
		v.NoteBlock(name)
	}
	for _, name := range SplitList(snap.Inventory) {
		v.NoteItem(name)
	}
}

// SplitList turns the comma-joined summary text into terms.
//
// A term that is not a bare name is dropped. This is not tidiness: the one
// caller that decides whether the world is a single block counts DISTINCT terms,
// so a term like "Clickable: none. Terrain: grass_block(12)" counts as one block
// and convinces the bot it is standing on the only block in a one-block world —
// in an ordinary field. The prose rendering of the scan is still handed to the
// models; it just must not reach the code that reasons about block names.
func SplitList(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		term := strings.TrimSpace(p)
		if term == "" {
			continue
		}
		// A name is one word. Anything carrying prose, a count, a distance or a
		// bearing is a rendered sentence, not a block.
		if !IsBareTerm(term) {
			continue
		}
		out = append(out, term)
	}
	return out
}

// IsBareTerm reports whether a term is a single block or item name, with no
// rendered detail attached.
func IsBareTerm(term string) bool {
	if strings.ContainsAny(term, " \t()") {
		return false
	}
	return true
}
