package storage

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	// signReach is how close the bot must stand to read a sign comfortably.
	// A player walks up to a sign; it is not read from across the room.
	signReach = 4.0

	// signLabelRadius is how far from a chest a sign still counts as its
	// label. Signage in a storage room is written above or beside the row of
	// chests it names, so the text is often a couple of blocks away rather
	// than attached to the block itself.
	signLabelRadius = 3.5
)

// Sign is a sign the bot can see, with the text it carries.
type Sign struct {
	Pos      protocol.BlockPos
	Text     string
	Distance float32
}

// CleanSignText strips Bedrock colour codes and collapses the line structure
// into one searchable string. Signage a player reads is decoration plus words,
// and both the Jev state text and the label matcher want the words.
//
// The section sign arrives as the two-byte UTF-8 sequence C2 A7, and the byte
// after it is the format code. Both have to go: dropping only the two-byte
// sign would leave "§lBAHAN" as "lBAHAN", and a leftover code letter makes a
// label unmatchable.
func CleanSignText(text string) string {
	var sb strings.Builder
	sb.Grow(len(text))
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == 0xC2 && i+1 < len(text) && text[i+1] == 0xA7:
			// Two-byte section sign: skip it and its format code.
			i += 2
		case text[i] == 0xA7:
			// Bare section sign byte: skip it and its format code.
			i++
		case text[i] == '\n' || text[i] == '\r' || text[i] == '\t':
			sb.WriteByte(' ')
		default:
			sb.WriteByte(text[i])
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

// FindSigns returns the signs with text around the bot, nearest first.
//
// Only signs the bot can see and whose text actually arrived are returned. A
// sign whose text never came down with the chunk is not an empty sign, it is
// an unknown one, and offering it would have the bot claim it read something
// it never saw.
func (s *Service) FindSigns() []Sign {
	origin := s.bot.GetCoords()
	bx := int32(math.Floor(float64(origin.X())))
	by := int32(math.Floor(float64(origin.Y())))
	bz := int32(math.Floor(float64(origin.Z())))

	out := make([]Sign, 0, 4)
	for dx := int32(-8); dx <= 8; dx++ {
		for dy := int32(-3); dy <= 3; dy++ {
			for dz := int32(-8); dz <= 8; dz++ {
				pos := protocol.BlockPos{bx + dx, by + dy, bz + dz}
				if !s.isSignBlock(pos) {
					continue
				}
				text, ok := s.bot.SignText(pos.X(), pos.Y(), pos.Z())
				if !ok {
					continue
				}
				center := blockCenter(pos)
				if !s.visibleFrom(origin, center, pos) {
					continue
				}
				clean := CleanSignText(text)
				if clean == "" {
					continue
				}
				out = append(out, Sign{
					Pos:      pos,
					Text:     clean,
					Distance: center.Sub(origin).Len(),
				})
			}
		}
	}
	sortByDistance(out, func(i int) float32 { return out[i].Distance })
	return out
}

func (s *Service) isSignBlock(pos protocol.BlockPos) bool {
	name, ok := s.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		return false
	}
	normalised := normalise(name)
	if strings.Contains(normalised, "sign") {
		return true
	}
	// Banner-style labels are the other way a room gets named.
	return strings.Contains(normalised, "banner")
}

// ReadSign walks to a sign, turns to face it, and reads its text back.
//
// The walk and the turn are what make this read as a person: a player who wants
// to know what a chest is for walks over and looks at the sign. A bot that
// reads signage from across the room is not looking at anything, and the
// timing of the turn is the tell that viewers actually see.
func (s *Service) ReadSign(ctx context.Context, sign Sign) (string, error) {
	center := blockCenter(sign.Pos)
	if err := s.approachPoint(ctx, center, signReach); err != nil {
		return "", fmt.Errorf("gagal mendekati sign di %s: %w", blockKey(sign.Pos), err)
	}
	s.bot.LookAt(mgl32.Vec3{center.X(), center.Y() + 0.35, center.Z()})
	// A glance, not an instant read: the pause is what a reader's eyes do, and
	// it also guarantees the aim has reached the server before anything reads
	// the text.
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(350 * time.Millisecond):
	}
	text, ok := s.bot.SignText(sign.Pos.X(), sign.Pos.Y(), sign.Pos.Z())
	if !ok {
		return "", fmt.Errorf("teks sign di %s belum termuat", blockKey(sign.Pos))
	}
	clean := CleanSignText(text)
	if clean == "" {
		return "", fmt.Errorf("sign di %s kosong", blockKey(sign.Pos))
	}
	return clean, nil
}

// LabelChests attaches the sign text that plausibly labels each chest.
//
// The rule is proximity plus visibility: a sign within a few blocks of a chest
// and in the same sightline is that chest's label. This is the difference
// between a bot that knows "bahannya" is over there and one that has to open
// twenty chests in the dark to find out.
func (s *Service) LabelChests(chests []Chest, signs []Sign) {
	for i := range chests {
		center := blockCenter(chests[i].Pos)
		best := ""
		bestDist := float32(signLabelRadius)
		for _, sign := range signs {
			dist := blockCenter(sign.Pos).Sub(center).Len()
			if dist > bestDist {
				continue
			}
			// A sign behind a wall does not label the chest in front of it.
			// Both endpoints are excluded from the occlusion walk: the sign's
			// own cell is solid, so testing it as an occluder would make every
			// sign fail to label the chest it is mounted on.
			if !s.lineOfSightBetween(blockCenter(sign.Pos), center, sign.Pos, chests[i].Pos) {
				continue
			}
			if dist < bestDist {
				bestDist = dist
				best = sign.Text
			}
		}
		chests[i].Label = best
	}
}

// MatchLabel scores how well a chest's label answers what the bot was asked
// for. A substring hit is a strong signal; a shared word is a weaker one, which
// is enough to order a search but not to stop it.
func MatchLabel(label, wanted string) int {
	labelLower := strings.ToLower(label)
	wantedLower := strings.ToLower(wanted)
	if labelLower == "" {
		return 0
	}
	if strings.Contains(labelLower, wantedLower) || strings.Contains(wantedLower, labelLower) {
		return 3
	}
	for _, word := range strings.Fields(wantedLower) {
		if len(word) < 3 {
			continue
		}
		if strings.Contains(labelLower, word) {
			return 1
		}
	}
	return 0
}

// OrderByLabelHint puts labelled chests that match what was asked for first,
// while keeping every unlabelled chest in the list behind them.
//
// Nothing is ever dropped: a label is a hint a player acts on, not a filter
// that makes the bot decide a chest cannot hold the item. Dropping chests on
// the strength of a guess is how a search silently misses the one chest that
// had the thing.
func OrderByLabelHint(chests []Chest, wanted string) []Chest {
	ordered := make([]Chest, 0, len(chests))
	rest := make([]Chest, 0, len(chests))
	for _, chest := range chests {
		if MatchLabel(chest.Label, wanted) > 0 {
			ordered = append(ordered, chest)
		} else {
			rest = append(rest, chest)
		}
	}
	return append(ordered, rest...)
}

// approachPoint walks until the bot is within reach of a world point.
func (s *Service) approachPoint(ctx context.Context, point mgl32.Vec3, reach float32) error {
	if point.Sub(s.bot.GetCoords()).Len() <= reach {
		s.bot.StopMovement()
		return nil
	}
	stand := s.standCell(point)
	s.bot.NavigateToBlock(stand[0], stand[1], stand[2], 1.0)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if point.Sub(s.bot.GetCoords()).Len() <= reach+0.5 {
			s.bot.StopMovement()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	s.bot.StopMovement()
	if point.Sub(s.bot.GetCoords()).Len() <= reach+1.0 {
		return nil
	}
	return fmt.Errorf("tidak bisa mendekati %v", point)
}

func sortByDistance[T any](items []T, at func(i int) float32) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && at(j) < at(j-1); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
