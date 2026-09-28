// Recognising a world that is one block.
//
// A single-block world is not a survival world with less in it. There is no
// wood, no chest, no night, nothing to sleep in and nowhere to walk. Every rule
// the brain has about what to do assumes terrain, and a bot that cannot tell the
// difference spends its first ten minutes failing the same way: reaching for
// oak_log in a world that has never contained a tree.
//
// Detecting it is cheap and worth doing early, because what it changes is not
// the bot's skill — it is the menu. There is one thing to do here, the bot
// stands on it, and the block changes when it does.

package agi

import (
	"strings"
)

// OneBlockConfidence is how strongly the world looks like a single block.
//
// It is a confidence rather than a boolean because the evidence is thin and a
// hard answer would flip on one unusual frame. A bot in a cleared building
// briefly looks like this, and treating a sweep of floor tiles as a
// single-block world would make it stand still and stare at the ceiling.
type OneBlockConfidence int

const (
	// NotOneBlock is the ordinary case: there is a world here.
	NotOneBlock OneBlockConfidence = iota
	// PossiblyOneBlock is a hint, not a conclusion.
	PossiblyOneBlock
	// DefinitelyOneBlock means the bot is standing on the only thing there is.
	DefinitelyOneBlock
)

// String names the confidence for a log line.
func (c OneBlockConfidence) String() string {
	switch c {
	case PossiblyOneBlock:
		return "possibly one block"
	case DefinitelyOneBlock:
		return "one block"
	default:
		return "not one block"
	}
}

// oneBlockThreshold is how many distinct blocks have to be in view before the
// world stops looking like a single block.
//
// Two is chosen over one on purpose. A bot standing on grass in the middle of a
// field sees only the block it is on; adding the one it is about to step onto
// takes that to two, and a single-block world never gets there.
const oneBlockThreshold = 2

// DetectOneBlock reads the world from the block summary a snapshot already
// built.
//
// The input is the same text the state description already carries rather than a
// fresh scan, because the scan has already happened this tick and doing it twice
// to answer a yes/no question is a cost the bot pays on every decision.
func DetectOneBlock(nearBlocks string) OneBlockConfidence {
	distinct, readable := readableBlockNames(nearBlocks)
	if !readable {
		// The summary was not a list of block names, so there is no evidence at
		// all. "I do not know" must not be recorded as "there is nothing here":
		// PossiblyOneBlock confirms into DefinitelyOneBlock on the next reading,
		// and a confirmed one-block world pins the bot to mining the block under
		// it in place forever. An unreadable summary is evidence against the
		// conclusion, not for it.
		return NotOneBlock
	}

	// Air is not a block in reach, so it cannot count toward the total. A bot
	// standing in the open sees air and would otherwise look like a single-block
	// world on every hillside.
	names := make([]string, 0, len(distinct))
	for _, name := range distinct {
		switch name {
		case "air", "cave_air", "void_air", "unknown":
			continue
		}
		names = append(names, name)
	}

	switch n := len(names); {
	case n == 0:
		// Nothing in reach at all. This is the single-block world's signature,
		// but it is also what a bot standing in the middle of a large empty
		// plain looks like, so it is a hint rather than a conclusion.
		return PossiblyOneBlock
	case n == 1:
		// Exactly one kind of block, and the bot is standing on it. A sweep of
		// identical floor tiles looks identical and is not a single-block world.
		return PossiblyOneBlock
	default:
		return NotOneBlock
	}
}

// ConfirmOneBlock is the second opinion, taken after the bot has stood still for
// a while.
//
// The first reading is a hint because "one block in reach" is also "a small
// room". Standing on it and finding the same answer twice is the difference. A
// real single-block world does not change when you look at it again; a room has
// walls the bot was not scanning past.
func ConfirmOneBlock(first, second OneBlockConfidence) OneBlockConfidence {
	if first == NotOneBlock || second == NotOneBlock {
		return NotOneBlock
	}
	return DefinitelyOneBlock
}

// OneBlockPlan renders the way of playing a single-block world.
//
// It is short and concrete because the bot has exactly one verb here: break the
// block it is on, look at what turned up, and decide whether that is worth
// building on. There is no exploring to be done and nothing to gather, and a bot
// that keeps trying is a bot that looks stuck on camera.
func OneBlockPlan(under string) string {
	under = normaliseTerm(under)
	if under == "" {
		under = "unknown"
	}
	var sb strings.Builder
	sb.WriteString("This world is a single block.\n")
	sb.WriteString("The block under you is now: " + under + "\n")
	sb.WriteString("There is nothing else here. There is no wood, no chest, no night, nothing to walk to.\n\n")
	sb.WriteString("The only thing to do is break the block you are standing on, see what it turns into,\n")
	sb.WriteString("and decide whether that is worth putting your next block on top of. Build upward.\n")
	sb.WriteString("If what appears is worthless, break the next one and keep going — do not stand there.\n")
	return sb.String()
}
