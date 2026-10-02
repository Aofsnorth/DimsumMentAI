package blockentity_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"bedrock-ai/internal/bot/blockentity"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// --- fake bot ---

// fakeBot is a server that can be told to echo a sign write back or to stay
// silent, which is the whole difference the confirmation logic has to survive.
//
// echoSigns models the host that re-broadcasts the block entity (so the write
// can be confirmed). A silent server models the host that accepts the packet and
// never says anything, and a bot that claims success there is lying.
type fakeBot struct {
	mu sync.Mutex

	pos      mgl32.Vec3
	cells    map[[3]int32]string
	signText map[[3]int32]string
	held     protocol.ItemStack

	echoSigns   bool
	writeErr    error
	blockedCell [3]int32
	blockedSet  bool

	writes  []packet.Packet
	lookAts []mgl32.Vec3
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		pos:       mgl32.Vec3{0, 64, 0},
		cells:     map[[3]int32]string{},
		signText:  map[[3]int32]string{},
		echoSigns: true,
		held:      protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 1}, Count: 1},
	}
}

func (b *fakeBot) GetCoords() mgl32.Vec3 { return b.pos }

func (b *fakeBot) GetEntityRuntimeID() uint64 { return 1 }

func (b *fakeBot) GetHeldItemSlot() uint32 { return 0 }

func (b *fakeBot) LookAt(p mgl32.Vec3) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lookAts = append(b.lookAts, p)
}

func (b *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	name, ok := b.cells[[3]int32{x, y, z}]
	return name, ok
}

func (b *fakeBot) SignText(x, y, z int32) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.signText[[3]int32{x, y, z}]
	return t, ok
}

func (b *fakeBot) HeldItem() protocol.ItemStack { return b.held }

func (b *fakeBot) WritePacket(pk packet.Packet) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.writeErr != nil {
		return b.writeErr
	}
	b.writes = append(b.writes, pk)

	// A sign write is echoed back the way a real host re-broadcasts it, by
	// storing the text the bot claims to have written.
	if b.echoSigns {
		if ba, ok := pk.(*packet.BlockActorData); ok {
			if id, _ := ba.NBTData["id"].(string); id == "Sign" {
				key := [3]int32{ba.Position.X(), ba.Position.Y(), ba.Position.Z()}
				if front, ok := ba.NBTData["FrontText"].(map[string]any); ok {
					if text, ok := front["Text"].(string); ok {
						b.signText[key] = text
					}
				}
			}
		}
	}
	return nil
}

func (b *fakeBot) blockActorWrites() []*packet.BlockActorData {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*packet.BlockActorData
	for _, w := range b.writes {
		if ba, ok := w.(*packet.BlockActorData); ok {
			out = append(out, ba)
		}
	}
	return out
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testItem builds a stack for the interaction payloads.
//
// ItemStack embeds ItemType, so a promoted-field literal (protocol.ItemStack{NetworkID: n})
// needs the go1.27 language level and this module is on 1.26. Naming the
// embedded field keeps this compiling either way.
func testItem(networkID int32) protocol.ItemStack {
	return protocol.ItemStack{
		ItemType: protocol.ItemType{NetworkID: networkID},
		Count:    1,
	}
}

// --- sign writing ---

// TestWriteSignSendsBlockActorData is the packet-level fact the whole feature
// rests on: Bedrock sign text is block-entity NBT, and the packet that carries
// it is BlockActorData.
func TestWriteSignSendsBlockActorData(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	pos := protocol.BlockPos{10, 64, -3}
	if _, err := w.WriteSign(context.Background(), pos, []string{"BAHAN", "KAYU"}); err != nil {
		t.Fatalf("WriteSign: %v", err)
	}

	writes := bot.blockActorWrites()
	if len(writes) != 1 {
		t.Fatalf("got %d BlockActorData writes, want 1", len(writes))
	}
	got := writes[0]
	if got.Position != pos {
		t.Errorf("wrote at %v, want %v", got.Position, pos)
	}
	if id, _ := got.NBTData["id"].(string); id != blockentity.SignEntityID {
		t.Errorf("NBT id = %q, want %q", id, blockentity.SignEntityID)
	}
}

// TestWriteSignNBTCarriesTheFourLines checks the payload the server actually
// reads. A sign written with the wrong key renders blank for every other client,
// which is the acceptance criterion for 6.4.
func TestWriteSignNBTCarriesTheFourLines(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	pos := protocol.BlockPos{1, 2, 3}
	if _, err := w.WriteSign(context.Background(), pos, []string{"SATU", "DUA"}); err != nil {
		t.Fatalf("WriteSign: %v", err)
	}

	nbtData := bot.blockActorWrites()[0].NBTData
	front, ok := nbtData["FrontText"].(map[string]any)
	if !ok {
		t.Fatalf("NBT has no FrontText compound: %#v", nbtData)
	}
	text, _ := front["Text"].(string)
	if text == "" {
		t.Fatalf("FrontText.Text is empty: %#v", front)
	}
	// The position fields have to travel with the text or the server drops the
	// update as belonging to no block at all.
	for field, want := range map[string]int32{"x": 1, "y": 2, "z": 3} {
		if v, _ := nbtData[field].(int32); v != want {
			t.Errorf("NBT %s = %v, want %d", field, v, want)
		}
	}
}

// TestWriteSignValidatesBeforeWriting is the ordering that matters: a rejected
// sign must never reach the wire, because a bot that writes a bad sign and then
// complains about it has already left broken signage on the wall.
func TestWriteSignValidatesBeforeWriting(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	_, err := w.WriteSign(context.Background(), protocol.BlockPos{1, 2, 3},
		[]string{string(make([]byte, 40))})
	if err == nil {
		t.Fatal("an overlong sign was accepted")
	}
	if len(bot.blockActorWrites()) != 0 {
		t.Error("an invalid sign was written to the wire before being rejected")
	}
}

// TestWriteSignReportsTheWriteItSaw is the honesty rule. When the host echoes
// the text back, the result must say so.
func TestWriteSignReportsTheWriteItSaw(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	res, err := w.WriteSign(context.Background(), protocol.BlockPos{5, 6, 7}, []string{"TERLIHAT"})
	if err != nil {
		t.Fatalf("WriteSign: %v", err)
	}
	if !res.Confirmed() {
		t.Fatal("Confirmed() = false on a host that echoed the text back")
	}
	if res.ConfirmedText != "TERLIHAT" {
		t.Errorf("ConfirmedText = %q, want %q", res.ConfirmedText, "TERLIHAT")
	}
}

// TestWriteSignDoesNotClaimASilentServer is the failure this exists to prevent:
// the server accepts the packet and never echoes it, and the bot reports a
// written sign that nobody has ever seen.
func TestWriteSignDoesNotClaimASilentServer(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.echoSigns = false // the host never re-broadcasts
	w := blockentity.NewWriter(bot, discardLogger())

	res, err := w.WriteSign(context.Background(), protocol.BlockPos{5, 6, 7}, []string{"TERLIHAT"})
	if err != nil {
		t.Fatalf("WriteSign returned a hard error for an unconfirmed write: %v", err)
	}
	if res.Confirmed() {
		t.Error("Confirmed() = true although the server never echoed the sign")
	}
	if res.ConfirmedText != "" {
		t.Errorf("ConfirmedText = %q, want empty when nothing was observed", res.ConfirmedText)
	}
	// The packet still went out, and the result has to say that honestly rather
	// than pretending nothing happened.
	if len(bot.blockActorWrites()) != 1 {
		t.Errorf("the write was not sent at all; an unconfirmed write is still sent")
	}
	if res.Status == blockentity.StatusConfirmed {
		t.Error("an unobserved write was reported with the confirmed status")
	}
}

// TestWriteSignReportsAWriteError is the transport failure. A refused write is
// not a written sign.
func TestWriteSignReportsAWriteError(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.writeErr = errors.New("connection closed")
	w := blockentity.NewWriter(bot, discardLogger())

	res, err := w.WriteSign(context.Background(), protocol.BlockPos{5, 6, 7}, []string{"X"})
	if err == nil {
		t.Fatal("a failed packet write was reported as success")
	}
	if res != nil && res.Confirmed() {
		t.Error("Confirmed() = true on a write that never left the bot")
	}
}

// TestWriteSignHonoursContextCancellation keeps a caller from hanging on a sign
// the bot can no longer reach.
func TestWriteSignHonoursContextCancellation(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.echoSigns = false
	w := blockentity.NewWriter(bot, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := w.WriteSign(ctx, protocol.BlockPos{1, 1, 1}, []string{"X"}); err == nil {
		t.Fatal("a cancelled context was ignored")
	}
}

// TestWriteSignLooksAtTheSignFirst is the human tell. A bot that writes signage
// without turning to face it writes from across the room, which looks wrong to
// anyone watching and is refused by hosts that check the interaction.
func TestWriteSignLooksAtTheSignFirst(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	if _, err := w.WriteSign(context.Background(), protocol.BlockPos{4, 5, 6}, []string{"X"}); err != nil {
		t.Fatalf("WriteSign: %v", err)
	}
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.lookAts) == 0 {
		t.Error("the bot wrote a sign without ever looking at it")
	}
}

// TestWriteSignIsIdempotentUnderRepeatedCalls is the retry story: a caller that
// cannot tell whether the first write landed has to be able to ask again, and
// the second attempt must not double-pad the text.
func TestWriteSignIsIdempotentUnderRepeatedCalls(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())
	pos := protocol.BlockPos{9, 9, 9}

	for i := 0; i < 3; i++ {
		res, err := w.WriteSign(context.Background(), pos, []string{"ULANG"})
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if !res.Confirmed() {
			t.Fatalf("attempt %d was not confirmed on an echoing host", i)
		}
		if res.ConfirmedText != "ULANG" {
			t.Errorf("attempt %d confirmed text = %q", i, res.ConfirmedText)
		}
	}
}

// --- item frames and armor stands ---

// TestInsertItemFrameItemSendsUseItemOnEntity is the mechanism. Putting an item
// in a frame is an entity interaction, not a block update, and the two are not
// interchangeable on the wire.
func TestInsertItemFrameItemSendsUseItemOnEntity(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	frame := protocol.BlockPos{2, 3, 4}
	bot.cells[[3]int32{2, 3, 4}] = "minecraft:frame"

	err := w.InsertItemIntoFrame(context.Background(), frame, 77, testItem(5))
	if err != nil {
		t.Fatalf("InsertItemIntoFrame: %v", err)
	}

	bot.mu.Lock()
	defer bot.mu.Unlock()
	var found bool
	for _, pk := range bot.writes {
		tx, ok := pk.(*packet.InventoryTransaction)
		if !ok {
			continue
		}
		if d, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData); ok {
			found = true
			if d.TargetEntityRuntimeID != 77 {
				t.Errorf("TargetEntityRuntimeID = %d, want 77", d.TargetEntityRuntimeID)
			}
			if d.ActionType != protocol.UseItemOnEntityActionInteract {
				t.Errorf("ActionType = %d, want UseItemOnEntityActionInteract (%d)",
					d.ActionType, protocol.UseItemOnEntityActionInteract)
			}
		}
	}
	if !found {
		t.Error("no UseItemOnEntity transaction was sent for the item frame")
	}
}

// TestInsertItemFrameItemNeedsTheFrameToExist stops the bot from clicking a
// block that is not there and reporting a successful insert.
func TestInsertItemFrameItemNeedsTheFrameToExist(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	// No frame placed at this cell.
	if err := w.InsertItemIntoFrame(context.Background(), protocol.BlockPos{2, 3, 4}, 77, protocol.ItemStack{}); err == nil {
		t.Fatal("an insert into an empty cell was accepted")
	}
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.writes) != 0 {
		t.Error("an insert was sent for a cell with no item frame in it")
	}
}

// TestInsertItemFrameItemRejectsAnUnresolvableFrameID is the honest failure: a
// frame the bot has never seen has no entity runtime ID, and there is no way to
// interact with an entity whose ID was never learned.
func TestInsertItemFrameItemRejectsAnUnresolvableFrameID(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.cells[[3]int32{2, 3, 4}] = "minecraft:frame"
	w := blockentity.NewWriter(bot, discardLogger())

	err := w.InsertItemIntoFrame(context.Background(), protocol.BlockPos{2, 3, 4}, 0, protocol.ItemStack{})
	if err == nil {
		t.Fatal("an insert was attempted with no entity runtime ID for the frame")
	}
}

// TestEquipArmorStandSendsUseItemOnEntity is the 6.5 acceptance: armour on a
// stand goes through the same entity interaction as a frame.
func TestEquipArmorStandSendsUseItemOnEntity(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.cells[[3]int32{7, 7, 7}] = "minecraft:armor_stand"
	w := blockentity.NewWriter(bot, discardLogger())

	stand := protocol.BlockPos{7, 7, 7}
	if err := w.EquipArmorStand(context.Background(), stand, 88, testItem(12)); err != nil {
		t.Fatalf("EquipArmorStand: %v", err)
	}

	bot.mu.Lock()
	defer bot.mu.Unlock()
	var found bool
	for _, pk := range bot.writes {
		tx, ok := pk.(*packet.InventoryTransaction)
		if !ok {
			continue
		}
		d, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData)
		if !ok {
			continue
		}
		found = true
		if d.TargetEntityRuntimeID != 88 {
			t.Errorf("TargetEntityRuntimeID = %d, want 88", d.TargetEntityRuntimeID)
		}
	}
	if !found {
		t.Error("no entity interaction was sent for the armor stand")
	}
}

// TestEquipArmorStandNeedsTheStand confirms the same "is it there" discipline
// the sign writer uses.
func TestEquipArmorStandNeedsTheStand(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := blockentity.NewWriter(bot, discardLogger())

	if err := w.EquipArmorStand(context.Background(), protocol.BlockPos{1, 2, 3}, 5, protocol.ItemStack{}); err == nil {
		t.Fatal("armour was equipped onto a cell with no armor stand")
	}
}

// TestWriterTimeoutIsHonoured keeps a confirm loop from polling forever on a
// server that simply never answers.
func TestWriterTimeoutIsHonoured(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.echoSigns = false
	w := blockentity.NewWriter(bot, discardLogger(), blockentity.WithConfirmTimeout(20*time.Millisecond))

	start := time.Now()
	res, err := w.WriteSign(context.Background(), protocol.BlockPos{1, 1, 1}, []string{"X"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("WriteSign: %v", err)
	}
	if res.Confirmed() {
		t.Error("Confirmed() = true without an echo")
	}
	if elapsed > 2*time.Second {
		t.Errorf("the confirm loop ran for %s, ignoring its own timeout", elapsed)
	}
}
