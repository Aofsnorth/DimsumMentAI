package world

import (
	"bytes"

	"github.com/df-mc/dragonfly/server/block/cube"
)

// Test-support accessors for the world package.

// DecodePalettedStorageForTest decodes one network paletted storage exactly as
// the chunk decoder does, so a black-box test can feed it wire bytes without a
// live connection.
func DecodePalettedStorageForTest(buf *bytes.Buffer) error {
	wc := NewWorldCache(0, cube.Range{0, 255}, nil)
	_, err := wc.decodeNetworkPalettedStorage(buf)
	return err
}

// StripFormattingForTest exposes the sign colour-code stripper.
func StripFormattingForTest(text string) string {
	return stripFormatting(text)
}
