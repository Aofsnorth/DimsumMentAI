package blockentity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	// SignEntityID is the block-entity id a sign carries in its NBT.
	//
	// It is the id the world cache looks for when it parses sign text out of
	// chunk data (internal/bot/world accepts "Sign" and "HangingSign"), so a
	// write that claims to be a sign and carries a different id is invisible to
	// the bot's own reader as well as to the server.
	SignEntityID = "Sign"

	// HangingSignEntityID is the id of the sign variant hung from a block.
	HangingSignEntityID = "HangingSign"
)

// DefaultSignColour is the text colour index a sign is written in when the
// caller does not pick one.
//
// The sixteen Bedrock text colours are indexed 0-15, black through white, and
// black is 0. Signs in a storage room are read at a glance from across the room,
// so the default is deliberately not a colour: black on the default sign
// background is the one that stays legible under every lighting a build can
// produce.
const DefaultSignColour int32 = 0

// WriteStatus is how far a write actually got.
//
// The three values exist because "the packet was sent" and "the sign says the
// new thing" are different facts, and a bot that conflates them writes signage
// it has not seen and reports it as done. StatusSent is the honest name for the
// first: the request went out, the server said nothing back, and nobody should
// assume the sign now reads that way.
type WriteStatus string

const (
	// StatusConfirmed means the server echoed the new text back and it matched.
	StatusConfirmed WriteStatus = "confirmed"
	// StatusSent means the packet was written and no echo arrived before the
	// confirm timeout. The write may well have landed; it was not observed.
	StatusSent WriteStatus = "sent-unconfirmed"
	// StatusRejected means the write was refused before anything was sent,
	// because the text does not fit on a sign.
	StatusRejected WriteStatus = "rejected"
)

// SignResult is the outcome of a sign write, and the distinction it carries is
// the point of the whole package.
type SignResult struct {
	// Status is how far the write got. See WriteStatus.
	Status WriteStatus
	// ConfirmedText is the text the server echoed back. It is empty unless
	// Status is StatusConfirmed, because filling it from the text that was sent
	// would make an unobserved write look like an observed one.
	ConfirmedText string
	// Pos is the sign that was written.
	Pos protocol.BlockPos
}

// Confirmed reports whether the server was observed to show the new text.
func (r *SignResult) Confirmed() bool {
	return r != nil && r.Status == StatusConfirmed
}

// WriteSign writes text to the sign at pos and reports what the server did with
// it.
//
// The text is validated first, so a sign that cannot hold what it was given is
// refused before any packet goes out. The bot then looks at the sign, because a
// bot that writes signage without turning to face it is writing from across the
// room and looks like one.
//
// What comes back is the honest part. On a host that re-broadcasts the block
// entity, the new text shows up in SignText and the result is StatusConfirmed
// with the text the server actually shows. On a host that accepts the write and
// stays silent, the result is StatusSent and ConfirmedText is empty: the packet
// was sent, and that is all that is known. That is not reported as a written
// sign, because a caller told "written" here goes on to tell a human the room is
// labelled, and if the server dropped it the label is simply not there.
//
// A transport failure is a hard error, not an unconfirmed write: a packet that
// never left the bot has not been sent to anybody.
func (w *Writer) WriteSign(ctx context.Context, pos protocol.BlockPos, lines []string) (*SignResult, error) {
	// Validation before anything else, so a bad sign is never written.
	validated, err := ValidateSignText(lines)
	if err != nil {
		return &SignResult{Status: StatusRejected, Pos: pos}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Turn to face the sign. The look is eased over roughly a second in the
	// bot, and the write goes out after a beat so the head has arrived.
	w.bot.LookAt(signAimPoint(pos))
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(180 * time.Millisecond):
	}

	data := signNBT(pos, nonEmptyLines(validated), DefaultSignColour)
	pk := &packet.BlockActorData{
		Position: pos,
		NBTData:  data,
	}
	if err := w.bot.WritePacket(pk); err != nil {
		return nil, fmt.Errorf("could not send the sign write: %w", err)
	}
	w.logger.Debug("sign write sent",
		"pos", blockKey(pos),
		"lines", len(nonEmptyLines(validated)),
		"bytes", len(data),
	)

	confirmed, observed := w.awaitSignText(ctx, pos, validated)
	if !observed {
		return &SignResult{Status: StatusSent, Pos: pos}, nil
	}
	return &SignResult{
		Status:        StatusConfirmed,
		ConfirmedText: confirmed,
		Pos:           pos,
	}, nil
}

// awaitSignText polls the sign until the server's own view of it matches, and
// reports what that view says and whether it was seen at all.
//
// The comparison is on cleaned text because what the server echoes back has
// been through a round trip that may include formatting the writer did not put
// there, and because the confirmation is about the words on the sign rather than
// the bytes that produced them.
func (w *Writer) awaitSignText(ctx context.Context, pos protocol.BlockPos, want []string) (string, bool) {
	// The expectation is the joined lines, so a sign written with fewer than
	// four lines is compared against the text it was actually given and not
	// against four empty lines.
	expected := strings.Join(nonEmptyLines(want), "\n")

	deadline := time.Now().Add(w.confirmTimeout)
	for {
		if text, ok := w.bot.SignText(pos.X(), pos.Y(), pos.Z()); ok {
			if signTextMatches(text, expected) {
				return text, true
			}
		}
		if ctx.Err() != nil {
			return "", false
		}
		if time.Now().After(deadline) {
			return "", false
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(confirmPollInterval):
		}
	}
}

// signNBT builds the block-entity payload for a sign.
//
// The lines are joined with newlines, and only the lines that carry text are
// included: the padding a short sign was validated up to is padding for this
// package's own bookkeeping, not something to send. Writing "BAHAN\n\n\n" puts
// three empty lines on a sign that has room for one line of words, and the
// trailing breaks come back on the echo and make the sign look broken.
//
// The keys are the ones Bedrock reads on a modern sign: FrontText is a compound
// holding Text and TextColor, and Text/TextColor are also written flat for
// builds that predate the two-sided form. Both are written because the reader
// in internal/bot/world accepts either, and a host that only honours the older
// flat form would otherwise show a blank sign to everyone.
//
// The x/y/z fields travel with the text. A BlockActorData whose NBT carries a
// text but no position is an update the server attributes to no block at all,
// and it is discarded.
func signNBT(pos protocol.BlockPos, lines []string, colour int32) map[string]any {
	text := strings.Join(lines, "\n")
	colours := make([]int32, MaxSignLines)
	for i := range colours {
		colours[i] = colour
	}

	return map[string]any{
		"id": SignEntityID,
		"x":  pos.X(),
		"y":  pos.Y(),
		"z":  pos.Z(),
		"FrontText": map[string]any{
			"Text":      text,
			"TextColor": colour,
		},
		"BackText": map[string]any{
			"Text":      text,
			"TextColor": colour,
		},
		// Flat form, for the older block-entity layout.
		"Text":      text,
		"TextColor": colours,
	}
}

// signTextMatches compares what the server echoed against what was written.
//
// The comparison ignores Bedrock formatting codes and surrounding whitespace on
// both sides. The server's copy has been through a round trip, and a
// confirmation that fails because the host normalised a space would report
// "unconfirmed" for a sign that is in fact written and visible.
func signTextMatches(observed, expected string) bool {
	return cleanSignText(observed) == cleanSignText(expected)
}

// cleanSignText strips colour codes and collapses whitespace, so two renderings
// of the same words compare equal.
func cleanSignText(text string) string {
	var sb strings.Builder
	sb.Grow(len(text))
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == colourPrefix && i+1 < len(runes):
			// Drop the prefix and its code together.
			i++
		case r == colourPrefix:
			// A trailing prefix with no code is not text.
		case r == '\n' || r == '\r' || r == '\t':
			sb.WriteRune(' ')
		default:
			sb.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

// nonEmptyLines drops the padding a short sign was filled out with, so the
// expectation is the words that were actually written.
func nonEmptyLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// signAimPoint is where the head goes to read a sign: the middle of the face,
// slightly below eye height, which is where a player looks.
func signAimPoint(pos protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(pos.X()) + 0.5,
		float32(pos.Y()) + 0.45,
		float32(pos.Z()) + 0.5,
	}
}

// blockKey renders a position for a log line or an error.
func blockKey(pos protocol.BlockPos) string {
	return fmt.Sprintf("%d,%d,%d", pos.X(), pos.Y(), pos.Z())
}
