package blockentity

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxSignLines is how many lines a Bedrock sign face holds. Four is not a
	// convention here, it is the shape of the block: a fifth line is either
	// dropped by the server or drawn outside the sign, and either way the text
	// the bot reported is not the text anyone sees.
	MaxSignLines = 4

	// MaxSignLineLen is the longest line a sign accepts.
	//
	// Fifteen characters is the limit the vanilla sign editor enforces, and it
	// is counted in characters rather than bytes: "BÉTI" is five characters and
	// six bytes, and a byte count rejects perfectly good Indonesian labels. A
	// rune count is the only one that matches what a player sees on the wall.
	MaxSignLineLen = 15
)

// colourPrefix is the Bedrock formatting code introducer (U+00A7, "§").
//
// It is two bytes in UTF-8, C2 A7, and the byte after it is the format code.
// Both are validated together, because a § with no code after it renders as
// literal text and a § followed by a letter that is not a code renders as that
// letter — both of which look like a bot that cannot spell.
const colourPrefix = '§'

// validColourCodes are the format codes Bedrock accepts after a §.
//
// These are the vanilla codes: 0-9 and a-f are the sixteen text colours, g is
// minecoin gold, k-o are the format toggles (obfuscated, bold, strikethrough,
// underline, italic), and r resets. Anything else is not a code, and writing
// one produces a sign with a stray character in it.
const validColourCodes = "0123456789abcdefgklmnor"

// ValidateSignText checks sign lines against what a sign can actually hold, and
// returns them padded to exactly MaxSignLines.
//
// It is a pure function, with no bot and no server, because every rule here is
// knowable in advance and none of them should be discovered by writing a bad
// sign onto a player's wall. The rules, in the order they bite:
//
//   - more than MaxSignLines lines is refused, since the extras have nowhere to go
//   - a line longer than MaxSignLineLen is refused rather than truncated,
//     because a silently shortened label is a wrong label
//   - a line may not contain a line break, which would shift every line below it
//   - control characters are refused
//   - a colour prefix must be followed by a real format code
//
// Trailing blank lines are legal and are how a one-word label is written, so
// short input is padded rather than rejected. Content is otherwise returned
// exactly as given, including surrounding spaces: the caller asked for that
// text, and quietly tidying it would break an exact-match lookup later.
func ValidateSignText(lines []string) ([]string, error) {
	if len(lines) > MaxSignLines {
		return nil, fmt.Errorf("a sign has %d lines, got %d", MaxSignLines, len(lines))
	}

	out := make([]string, MaxSignLines)
	for i, line := range lines {
		if err := validateSignLine(line); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		out[i] = line
	}
	return out, nil
}

// validateSignLine checks one line against the per-line rules.
func validateSignLine(line string) error {
	if n := utf8.RuneCountInString(line); n > MaxSignLineLen {
		return fmt.Errorf("%d characters, the limit is %d", n, MaxSignLineLen)
	}
	return checkSignRunes([]rune(line))
}

// checkSignRunes walks the characters of a line, which is where the colour
// prefix has to be understood: a § is only legal as the first half of a §code
// pair, and the code after it is part of the line's width whether it is
// meaningful or not.
func checkSignRunes(runes []rune) error {
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == colourPrefix:
			// The code byte has to be there and has to be a real code. A
			// trailing §, or a § followed by something that is not a code,
			// renders as literal text on the sign.
			if i+1 >= len(runes) {
				return fmt.Errorf("ends with a colour code marker but no colour code")
			}
			code := runes[i+1]
			if !strings.ContainsRune(validColourCodes, code) {
				return fmt.Errorf("§%c is not a Bedrock colour code", code)
			}
			// Skip the code: it is a formatting instruction, and re-examining
			// it as its own character would misread a letter that happens to
			// be in the control-character range.
			i++
		case r == '\n' || r == '\r':
			return fmt.Errorf("contains a line break; each line is one line of the sign")
		case unicode.IsControl(r):
			return fmt.Errorf("contains a control character")
		}
	}
	return nil
}
