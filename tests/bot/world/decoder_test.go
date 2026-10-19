package world_test

import (
	"bytes"
	"testing"

	"bedrock-ai/internal/bot/world"
)

// writeVarint appends an unsigned LEB128 varint, the encoding Bedrock uses for
// the palette count and palette entries.
func writeVarint(buf *bytes.Buffer, v uint32) {
	for v >= 0x80 {
		buf.WriteByte(byte(v) | 0x80)
		v >>= 7
	}
	buf.WriteByte(byte(v))
}

// TestPaletteCountIsBounded is the regression guard for the wire-driven
// allocation.
//
// The palette count is read from the packet and used directly as a make() length:
//
//	palette := make([]uint32, paletteCount)
//
// A varint may legally encode up to 2^31-1, so one corrupt or hostile SubChunk
// could ask for tens of gigabytes (or trip "makeslice: len out of range") and
// take the bot down. A sub-chunk has 4096 cells, so any count above 4096 is
// invalid and must be rejected before allocating.
func TestPaletteCountIsBounded(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	buf.WriteByte(2) // bitsPerBlock = 1 → 128 uint32 words follow

	// 128 words of block data.
	buf.Write(make([]byte, 128*4))

	// paletteCount = 0x7FFFFFFF
	writeVarint(&buf, 0x7FFFFFFF)

	err := world.DecodePalettedStorageForTest(&buf)
	if err == nil {
		t.Fatal("a palette count of 0x7FFFFFFF was accepted; the decoder would allocate gigabytes")
	}
}

// TestSmallPaletteStillDecodes keeps the bound from rejecting valid storages.
func TestSmallPaletteStillDecodes(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	buf.WriteByte(2) // bitsPerBlock = 1
	buf.Write(make([]byte, 128*4))
	writeVarint(&buf, 2) // two palette entries
	writeVarint(&buf, 1)
	writeVarint(&buf, 2)

	if err := world.DecodePalettedStorageForTest(&buf); err != nil {
		t.Fatalf("a valid two-entry palette was rejected: %v", err)
	}
}

// TestStripFormattingDropsTheCodeByte is the regression guard for sign text.
//
// A UTF-8 § is C2 A7 and is followed by one formatting code byte. The old loop
// advanced by only one position for the C2 A7 pair, so "§a" decoded to "a": a
// sign whose only content was a colour code looked like it had text.
func TestStripFormattingDropsTheCodeByte(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"\u00a7a":             "",
		"\u00a7aShop\u00a7r":  "Shop",
		"\u00a7l\u00a7cRumah": "Rumah",
		"no codes":            "no codes",
		"\u00a7":              "",
	}
	for in, want := range cases {
		if got := world.StripFormattingForTest(in); got != want {
			t.Errorf("stripFormatting(%q) = %q, want %q", in, got, want)
		}
	}
}
