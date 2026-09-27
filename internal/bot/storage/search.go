package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Opener opens a container at a position and returns the live session.
type Opener func(ctx context.Context, pos protocol.BlockPos) (Container, error)

// SearchResult is what a completed search found.
type SearchResult struct {
	// Found is true when an item was actually taken.
	Found bool
	// Name and Taken describe what was taken and how much.
	Name  string
	Taken int
	// OpenedCount is how many chests were opened. A player searching a room
	// does open several before finding the thing, and reporting that honestly
	// is what makes "tidak ada" credible.
	OpenedCount int
	// SignsRead lists the sign text that guided the search, in the order it
	// was read. This is the "petunjuk" the bot followed.
	SignsRead []string
	// Reason explains an empty result in a form a player can act on.
	Reason string
}

// Searcher runs a labelled-storage search: it reads the signage first, opens
// the chest that signage points at, and only then works through the remaining
// chests one at a time.
type Searcher struct {
	svc  *Service
	open Opener
}

// NewSearcher builds a Searcher bound to a service and an opener.
func NewSearcher(svc *Service, open Opener) *Searcher {
	return &Searcher{svc: svc, open: open}
}

// Find opens visible chests one at a time until it finds the item.
//
// The order is the whole design, and it is the order a person uses:
//
//  1. Read the signage. A room labelled "bahan" is a plan, not a detail — it
//     says which chest to open first and stops the bot from blind-opening
//     everything.
//  2. Open the chest that signage points at.
//  3. Only then fall through to the remaining chests, nearest first.
//
// Two things it deliberately does NOT do:
//
//   - It never opens a chest in parallel or "scans" several at once. A player
//     can only have one chest open, and a bot that reports having checked six
//     chests at the same instant is visibly not a person.
//   - It never stops at the first empty chest. An empty chest is information,
//     not a verdict: the answer to "is there oak in here" is about every chest
//     in the room, so the search continues until the chests run out.
func (s *Searcher) Find(ctx context.Context, wanted string, count int) (SearchResult, error) {
	result := SearchResult{Name: wanted}

	chests := s.svc.FindContainers()
	if len(chests) == 0 {
		result.Reason = "tidak ada chest yang kelihatan di sini"
		return result, nil
	}

	// Signage first. The signs are what turn "open twenty chests" into "open
	// the one that says materials".
	signs := s.svc.FindSigns()
	s.svc.LabelChests(chests, signs)
	ordered := OrderByLabelHint(chests, wanted)

	for _, sign := range signs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		// Read a sign only when it is close enough to actually read, and only
		// when it looks like it could be about what we are looking for. Reading
		// every sign in the room would be its own kind of tell.
		if sign.Distance > 6 {
			continue
		}
		if len(result.SignsRead) >= 2 {
			break
		}
		if result.Reason == "" && !labelRelated(sign.Text, wanted) {
			continue
		}
		text, err := s.svc.ReadSign(ctx, sign)
		if err != nil {
			continue
		}
		result.SignsRead = append(result.SignsRead, text)
	}

	for _, chest := range ordered {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		container, err := s.svc.Open(ctx, chest, s.open)
		if err != nil {
			// A chest that would not open is a fact about that chest, not
			// about the room. Keep looking.
			continue
		}
		result.OpenedCount++

		taken, err := s.svc.TakeItem(ctx, container, wanted, count)
		container.Close()
		if err == nil && taken > 0 {
			result.Found = true
			result.Taken = taken
			return result, nil
		}
	}

	if !result.Found {
		result.Reason = fmt.Sprintf("udah buka %d chest, %s-nya nggak ada", result.OpenedCount, wanted)
	}
	return result, nil
}

// StoreItem puts an item into the best visible chest, the way a player tidies
// up: find somewhere sensible, walk over, put it away.
func (s *Searcher) StoreItem(ctx context.Context, wanted string, count int) (int, error) {
	chests := s.svc.FindContainers()
	if len(chests) == 0 {
		return 0, fmt.Errorf("tidak ada chest yang kelihatan di sini")
	}
	s.svc.LabelChests(chests, s.svc.FindSigns())
	ordered := OrderByLabelHint(chests, wanted)

	var lastErr error
	for _, chest := range ordered {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		container, err := s.svc.Open(ctx, chest, s.open)
		if err != nil {
			lastErr = err
			continue
		}
		stored, err := s.svc.StoreItem(ctx, container, wanted, count)
		container.Close()
		if err == nil {
			return stored, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, fmt.Errorf("tidak ada chest yang bisa dipakai")
}

// labelRelated reports whether sign text plausibly refers to what was asked
// for. It is deliberately loose: a sign that mentions any meaningful word of
// the query is worth reading, because a sign the bot skipped because of a
// wording difference is a chest it will end up opening blindly.
func labelRelated(signText, wanted string) bool {
	lower := strings.ToLower(signText)
	for _, word := range strings.Fields(strings.ToLower(wanted)) {
		if len(word) < 3 {
			continue
		}
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}
