package world

import (
	"bytes"
	"io"
	"log/slog"
	"testing"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

// encodeNBT serialises one compound the way a chunk payload carries block
// entities, so the parse path is exercised on real wire bytes rather than on a
// hand-built map.
func encodeNBT(t *testing.T, values ...map[string]any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := nbt.NewEncoderWithEncoding(&buf, nbt.LittleEndian)
	for _, v := range values {
		if err := enc.Encode(v); err != nil {
			t.Fatalf("encode block entity: %v", err)
		}
	}
	return buf.Bytes()
}

func newSignTestCache() *WorldCache {
	return NewWorldCache(0, cube.Range{-64, 319}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestScanSignReadsModernFrontText covers the 1.20+ shape, where the text lives
// in a FrontText compound. This is the shape almost every current server sends.
func TestScanSignReadsModernFrontText(t *testing.T) {
	t.Parallel()

	wc := newSignTestCache()
	wc.scanSignBlockEntities(encodeNBT(t, map[string]any{
		"id": "Sign",
		"x":  int32(10),
		"y":  int32(64),
		"z":  int32(-3),
		"FrontText": map[string]any{
			"Text":   "BAHAN",
			"RawTxt": "BAHAN",
		},
	}))

	text, ok := wc.SignText(10, 64, -3)
	if !ok {
		t.Fatal("SignText: no text stored for a sign that was in the payload")
	}
	if text != "BAHAN" {
		t.Errorf("SignText = %q, want %q", text, "BAHAN")
	}
}

// TestScanSignIgnoresNonSigns is the guard against the parser claiming every
// block entity in the chunk is a sign: chests and hoppers share the payload.
func TestScanSignIgnoresNonSigns(t *testing.T) {
	t.Parallel()

	wc := newSignTestCache()
	wc.scanSignBlockEntities(encodeNBT(t, map[string]any{
		"id": "Chest",
		"x":  int32(1),
		"y":  int32(2),
		"z":  int32(3),
		"Items": []any{
			map[string]any{"Count": int32(1)},
		},
	}))

	if wc.SignCount() != 0 {
		t.Errorf("SignCount = %d, want 0 for a chest block entity", wc.SignCount())
	}
}

// TestScanSignStopsAtGarbageTail matters because the payload after the block
// entities is not guaranteed to decode cleanly. A parse failure must stop the
// scan, not drop the signs that were already read.
func TestScanSignStopsAtGarbageTail(t *testing.T) {
	t.Parallel()

	wc := newSignTestCache()
	payload := encodeNBT(t, map[string]any{
		"id":        "Sign",
		"x":         int32(5),
		"y":         int32(70),
		"z":         int32(5),
		"FrontText": map[string]any{"Text": "ALAT"},
	})
	// Trailing bytes that cannot be a valid compound.
	payload = append(payload, 0xFF, 0xFE, 0xFD)

	wc.scanSignBlockEntities(payload)

	if _, ok := wc.SignText(5, 70, 5); !ok {
		t.Error("a sign read before the undecodable tail was lost")
	}
}

// TestScanSignSkipsEmptyText keeps blank signs out of the label set: an empty
// sign would otherwise attach an empty label and read as "no label here".
func TestScanSignSkipsEmptyText(t *testing.T) {
	t.Parallel()

	wc := newSignTestCache()
	wc.scanSignBlockEntities(encodeNBT(t, map[string]any{
		"id":        "Sign",
		"x":         int32(0),
		"y":         int32(0),
		"z":         int32(0),
		"FrontText": map[string]any{"Text": "   "},
	}))

	if wc.SignCount() != 0 {
		t.Errorf("SignCount = %d, want 0 for a whitespace-only sign", wc.SignCount())
	}
}

// TestScanSignReadsLegacyText covers the pre-1.20 flat Text field, which older
// servers and many proxies still send.
func TestScanSignReadsLegacyText(t *testing.T) {
	t.Parallel()

	wc := newSignTestCache()
	wc.scanSignBlockEntities(encodeNBT(t, map[string]any{
		"id":   "Sign",
		"x":    int32(-1),
		"y":    int32(64),
		"z":    int32(9),
		"Text": "GUDANG",
	}))

	if text, ok := wc.SignText(-1, 64, 9); !ok || text != "GUDANG" {
		t.Errorf("SignText = (%q, %v), want (\"GUDANG\", true)", text, ok)
	}
}
