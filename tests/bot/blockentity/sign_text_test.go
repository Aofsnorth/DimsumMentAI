package blockentity_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/blockentity"
)

// --- Sign text validation (pure function) ---

// TestSignMustHaveFourLines is the shape rule: a Bedrock sign face is four
// lines, and a write that puts five on it is refused by the server after the
// bot has already reported success.
func TestSignMustHaveFourLines(t *testing.T) {
	t.Parallel()

	if got := blockentity.MaxSignLines; got != 4 {
		t.Errorf("MaxSignLines = %d, want 4", got)
	}
}

// TestValidateAcceptsAWellFormedSign is the happy path. Empty trailing lines are
// legal — that is how a one-word label is written on a sign.
func TestValidateAcceptsAWellFormedSign(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		lines []string
	}{
		{"single line", []string{"BAHAN"}},
		{"two lines", []string{"BAHAN", "KAYU"}},
		{"four full lines", []string{"ALAT", "BESI", "EMAS", "PERAK"}},
		{"empty is legal", []string{}},
		{"trailing blanks", []string{"LABEL", "", "", ""}},
		{"all blanks", []string{"", "", "", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := blockentity.ValidateSignText(tc.lines)
			if err != nil {
				t.Fatalf("ValidateSignText(%q) = error %v, want accepted", tc.lines, err)
			}
			if len(got) != 4 {
				t.Fatalf("got %d lines %q, want exactly 4 after padding", len(got), got)
			}
		})
	}
}

// TestValidatePadsToFourLines is what makes a one-word label usable: the caller
// says "BAHAN", the server gets four lines, and the blank ones are real rather
// than a short array the sign has to guess at.
func TestValidatePadsToFourLines(t *testing.T) {
	t.Parallel()

	got, err := blockentity.ValidateSignText([]string{"BAHAN", "KAYU"})
	if err != nil {
		t.Fatalf("ValidateSignText: %v", err)
	}
	want := []string{"BAHAN", "KAYU", "", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q (full result %q)", i, got[i], want[i], got)
		}
	}
}

// TestValidateRejectsTooManyLines is the other half of the shape rule.
func TestValidateRejectsTooManyLines(t *testing.T) {
	t.Parallel()

	_, err := blockentity.ValidateSignText([]string{"a", "b", "c", "d", "e"})
	if err == nil {
		t.Fatal("five lines were accepted; the sign face only has four")
	}
}

// TestValidateRejectsOverlongLines pins the length rule at the value Bedrock
// actually enforces, not a number guessed from memory.
func TestValidateRejectsOverlongLines(t *testing.T) {
	t.Parallel()

	if blockentity.MaxSignLineLen != 15 {
		t.Errorf("MaxSignLineLen = %d, want 15", blockentity.MaxSignLineLen)
	}

	ok := strings.Repeat("A", blockentity.MaxSignLineLen)
	if _, err := blockentity.ValidateSignText([]string{ok}); err != nil {
		t.Errorf("a line of exactly %d characters was rejected: %v", blockentity.MaxSignLineLen, err)
	}

	tooLong := strings.Repeat("A", blockentity.MaxSignLineLen+1)
	if _, err := blockentity.ValidateSignText([]string{tooLong}); err == nil {
		t.Error("a line one character over the limit was accepted")
	}
}

// TestValidateMeasuresRunesNotBytes is the bug that makes a sign silently
// truncate Indonesian labels. "BÉTI" is five characters but six bytes; a byte
// check rejects it and a player sees a wrong label.
func TestValidateMeasuresRunesNotBytes(t *testing.T) {
	t.Parallel()

	// Five multi-byte runes: 10 bytes, 5 characters.
	line := strings.Repeat("é", 5)
	if len(line) != 10 {
		t.Fatalf("test setup wrong: %d bytes", len(line))
	}
	if _, err := blockentity.ValidateSignText([]string{line}); err != nil {
		t.Errorf("a 5-character accented line (10 bytes) was rejected: %v", err)
	}
}

// TestValidateRejectsNewlineInsideALine is what stops one "line" from becoming
// two and shifting everything below it.
func TestValidateRejectsNewlineInsideALine(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"BAHAN\nKAYU", "BAHAN\rKAYU", "\n", "BAHAN\n"} {
		if _, err := blockentity.ValidateSignText([]string{bad}); err == nil {
			t.Errorf("%q was accepted; a line may not carry its own line break", bad)
		}
	}
}

// TestValidateAcceptsColourPrefix is the whole reason the validator has to know
// about the section sign: colour codes are legitimate sign content.
func TestValidateAcceptsColourPrefix(t *testing.T) {
	t.Parallel()

	for _, s := range []string{
		"§cMERAH",
		"§aHIJAU",
		"§lTEBAL",
		"§rRESET",
		"§0hitam",
	} {
		if _, err := blockentity.ValidateSignText([]string{s}); err != nil {
			t.Errorf("%q was rejected: %v", s, err)
		}
	}
}

// TestValidateRejectsDanglingColourPrefix catches the sign that renders with a
// literal section sign and a stray letter, which is how a bot ends up writing
// "§l" onto a chest label and wondering why the label is orange.
func TestValidateRejectsDanglingColourPrefix(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"§", "§x", "§z", "MERAH§"} {
		if _, err := blockentity.ValidateSignText([]string{bad}); err == nil {
			t.Errorf("%q was accepted; a colour prefix with no valid code is a broken sign", bad)
		}
	}
}

// TestValidateRejectsControlCharacters is the charset rule. A sign carrying a
// NUL or an escape is either refused by the server or, worse, written and
// rendered as a blank line that the bot then reports as "empty".
func TestValidateRejectsControlCharacters(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		"BA\x00HAN",
		"BA\tHAN",
		"BA\x1bHAN",
		"\x07",
	} {
		if _, err := blockentity.ValidateSignText([]string{bad}); err == nil {
			t.Errorf("%q was accepted; control characters are not sign text", bad)
		}
	}
}

// TestValidatePreservesTheWordsExactly guards the padding/truncation rules
// from "helpfully" rewriting content. A validator that trims a label turns
// "BAHAN " into "BAHAN" and breaks an exact-match lookup.
func TestValidatePreservesTheWordsExactly(t *testing.T) {
	t.Parallel()

	got, err := blockentity.ValidateSignText([]string{"  BAHAN  "})
	if err != nil {
		t.Fatalf("ValidateSignText: %v", err)
	}
	if got[0] != "  BAHAN  " {
		t.Errorf("line 0 = %q, want the input preserved verbatim", got[0])
	}
}

// TestValidateRejectsEveryNonColourAfterPrefix is the exhaustive form of the
// dangling-prefix rule. Every rune outside the real code set is refused, so a
// future edit that widens the set has to widen it deliberately.
func TestValidateRejectsEveryNonColourAfterPrefix(t *testing.T) {
	t.Parallel()

	const valid = "0123456789abcdefgklmnor"
	for r := rune(0x20); r < rune(0x7F); r++ {
		line := "§" + string(r)
		_, err := blockentity.ValidateSignText([]string{line})
		isValidCode := strings.ContainsRune(valid, r)
		switch {
		case isValidCode && err != nil:
			t.Errorf("§%c was rejected but is a valid Bedrock colour code", r)
		case !isValidCode && err == nil:
			t.Errorf("§%c was accepted but is not a Bedrock colour code", r)
		}
	}
}

// TestValidateAcceptsTheWholeDocumentedColourSet is the positive half: every
// code the vanilla sign editor offers has to be accepted.
func TestValidateAcceptsTheWholeDocumentedColourSet(t *testing.T) {
	t.Parallel()

	for _, c := range "0123456789abcdefgklmnor" {
		if _, err := blockentity.ValidateSignText([]string{"§" + string(c) + "TEKS"}); err != nil {
			t.Errorf("§%c was rejected: %v", c, err)
		}
	}
}

// TestValidateCountsTheColourCodeAsOneCharacter is the width rule the rune
// count has to respect. A colour code plus its letter is two characters on the
// wire, and a player counts it as one prefix, so a sign at the limit should
// still fit once the prefix is applied.
func TestValidateCountsTheColourCodeAsOneCharacter(t *testing.T) {
	t.Parallel()

	// 15 characters: § + c + 13 more.
	line := "§c" + strings.Repeat("A", 13)
	if _, err := blockentity.ValidateSignText([]string{line}); err != nil {
		t.Errorf("a 15-character line with a colour prefix was rejected: %v", err)
	}
}
