package blockentity_test

import (
	"bytes"
	"context"
	"testing"

	"bedrock-ai/internal/bot/blockentity"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// TestSignNBTSurvivesAWireRoundTrip is the test that says the payload is real.
//
// The write path is only trustworthy if the NBT the writer builds is the same
// NBT the reader would get back off the wire. This marshals a BlockActorData
// through protocol.NewWriter exactly as the connection does, reads it back
// through protocol.NewReader, and checks the sign's own reader — the same NBT
// shape internal/bot/world parses out of chunk data — still finds the text.
//
// It is here because the alternative is taking the library's word for it, and
// the library is not the thing being trusted: the server is.
func TestSignNBTSurvivesAWireRoundTrip(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	pos := protocol.BlockPos{12, 70, -9}
	if _, err := w.WriteSign(context.Background(), pos, []string{"ALAT", "BESI"}); err != nil {
		t.Fatalf("WriteSign: %v", err)
	}

	original := bot.blockActorWrites()[0]

	// Out onto the wire.
	var buf bytes.Buffer
	original.Marshal(protocol.NewWriter(&buf, 0))
	if buf.Len() == 0 {
		t.Fatal("marshalling the sign write produced no bytes")
	}

	// And back off it.
	var got packet.BlockActorData
	got.Marshal(protocol.NewReader(bytes.NewReader(buf.Bytes()), 0, false))

	if got.Position != pos {
		t.Errorf("position survived as %v, want %v", got.Position, pos)
	}
	if id, _ := got.NBTData["id"].(string); id != blockentity.SignEntityID {
		t.Errorf("id survived as %q, want %q", id, blockentity.SignEntityID)
	}
	front, ok := got.NBTData["FrontText"].(map[string]any)
	if !ok {
		t.Fatalf("FrontText did not survive the wire as a compound: %#v", got.NBTData)
	}
	text, _ := front["Text"].(string)
	if text == "" {
		t.Fatalf("FrontText.Text did not survive the wire: %#v", front)
	}
}

// TestSignWriteNBTIsIndependentlyDecodable checks the payload with the NBT
// decoder rather than the packet reader, so the test does not depend on the
// packet round trip to prove the map is well-formed NBT.
func TestSignWriteNBTIsIndependentlyDecodable(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	pos := protocol.BlockPos{3, 4, 5}
	if _, err := w.WriteSign(context.Background(), pos, []string{"GUDANG"}); err != nil {
		t.Fatalf("WriteSign: %v", err)
	}
	data := bot.blockActorWrites()[0].NBTData

	encoded, err := nbt.MarshalEncoding(data, nbt.NetworkLittleEndian)
	if err != nil {
		t.Fatalf("the sign NBT could not be encoded: %v", err)
	}
	var out map[string]any
	dec := nbt.NewDecoderWithEncoding(bytes.NewReader(encoded), nbt.NetworkLittleEndian)
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("the sign NBT could not be decoded: %v", err)
	}
	if out["id"] != blockentity.SignEntityID {
		t.Errorf("decoded id = %v, want %q", out["id"], blockentity.SignEntityID)
	}
	// The coordinates must be int32, not int: the encoder maps Go int to
	// nothing representable, and a sign whose NBT carries a coordinate the
	// server cannot read is an update it attributes to no block at all.
	for field, want := range map[string]int32{"x": 3, "y": 4, "z": 5} {
		if v, ok := out[field].(int32); !ok || v != want {
			t.Errorf("decoded %s = %v (%T), want int32 %d", field, out[field], out[field], want)
		}
	}
}

// TestSignWriteCarriesNoPaddingNewlines is the regression for a bug this package
// shipped and fixed: a one-word label was padded to four lines before being
// written, so the sign received "BAHAN\n\n\n" and echoed it back with three
// empty lines under the words.
//
// The padding is this package's bookkeeping. It belongs in the validated slice
// and not on the wall.
func TestSignWriteCarriesNoPaddingNewlines(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	if _, err := w.WriteSign(context.Background(), protocol.BlockPos{1, 1, 1}, []string{"BAHAN"}); err != nil {
		t.Fatalf("WriteSign: %v", err)
	}
	front, _ := bot.blockActorWrites()[0].NBTData["FrontText"].(map[string]any)
	text, _ := front["Text"].(string)
	if text != "BAHAN" {
		t.Errorf("FrontText.Text = %q, want %q; padding reached the sign", text, "BAHAN")
	}
}

// TestSignWriteJoinsRealLinesWithNewlines is the other half: lines that do carry
// text must still be separated, or a two-line sign renders as one run-on line.
func TestSignWriteJoinsRealLinesWithNewlines(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	if _, err := w.WriteSign(context.Background(), protocol.BlockPos{1, 1, 1}, []string{"ALAT", "BESI"}); err != nil {
		t.Fatalf("WriteSign: %v", err)
	}
	front, _ := bot.blockActorWrites()[0].NBTData["FrontText"].(map[string]any)
	text, _ := front["Text"].(string)
	if text != "ALAT\nBESI" {
		t.Errorf("FrontText.Text = %q, want %q", text, "ALAT\nBESI")
	}
}

// TestSignWriteCarriesBothModernAndFlatForms pins the dual key layout. The
// bot's own sign reader (internal/bot/world) accepts FrontText and a flat Text,
// and which one a host honours depends on its version, so a payload carrying only
// one of them renders blank on the other.
func TestSignWriteCarriesBothModernAndFlatForms(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	if _, err := w.WriteSign(context.Background(), protocol.BlockPos{1, 1, 1}, []string{"X"}); err != nil {
		t.Fatalf("WriteSign: %v", err)
	}
	data := bot.blockActorWrites()[0].NBTData

	if _, ok := data["FrontText"].(map[string]any); !ok {
		t.Error("no FrontText compound for a modern host")
	}
	if _, ok := data["Text"].(string); !ok {
		t.Error("no flat Text field for an older host")
	}
	colours, ok := data["TextColor"].([]int32)
	if !ok {
		t.Fatalf("flat TextColor is %T, want []int32", data["TextColor"])
	}
	if len(colours) != blockentity.MaxSignLines {
		t.Errorf("flat TextColor has %d entries, want %d (one per sign line)",
			len(colours), blockentity.MaxSignLines)
	}
}

// TestSignWriteIsRejectedBeforeTheWireForBadText walks the whole rejected path,
// which is the one a caller has to be able to trust: an invalid sign is refused
// and the caller gets the reason.
func TestSignWriteIsRejectedBeforeTheWireForBadText(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	res, err := w.WriteSign(context.Background(), protocol.BlockPos{1, 1, 1},
		[]string{"a", "b", "c", "d", "e"})
	if err == nil {
		t.Fatal("a five-line sign was accepted")
	}
	if res.Status != blockentity.StatusRejected {
		t.Errorf("Status = %q, want %q", res.Status, blockentity.StatusRejected)
	}
	if len(bot.blockActorWrites()) != 0 {
		t.Error("a rejected sign still reached the wire")
	}
}

// TestSignEntityIDsAreTheOnesTheReaderUses pins the block-entity ids against the
// strings internal/bot/world matches on when it parses sign text out of a
// chunk. A write that carries an id the reader does not know is invisible to the
// bot's own read path as well as to the server.
func TestSignEntityIDsAreTheOnesTheReaderUses(t *testing.T) {
	t.Parallel()

	if blockentity.SignEntityID != "Sign" {
		t.Errorf("SignEntityID = %q, want %q", blockentity.SignEntityID, "Sign")
	}
	if blockentity.HangingSignEntityID != "HangingSign" {
		t.Errorf("HangingSignEntityID = %q, want %q", blockentity.HangingSignEntityID, "HangingSign")
	}
}
