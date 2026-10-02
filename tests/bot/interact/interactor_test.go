package interact_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/interact"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// fakeBot is a Bot with just enough state to exercise targeting, and which
// records the packets an interaction produced.
type fakeBot struct {
	pos      mgl32.Vec3
	yaw      float32
	entities map[uint64]*entity.Info
	blocks   map[string]string // "x,y,z" -> block name
	blockIDs map[string]uint32 // "x,y,z" -> wire network ID
	players  map[string]uint64 // username -> entity id
	packets  []packet.Packet
	statuses []event.ActionStatus
	// queued collects ItemInteractionData handed to QueueItemInteractionData,
	// i.e. clicks scheduled to ride the next PlayerAuthInput.
	queued []protocol.UseItemTransactionData
	// clickActivates flips the clicked block's network ID when a standalone
	// InventoryTransaction arrives, simulating a host that processed the click.
	clickActivates bool
	lookCalls      int
	lookAt         mgl32.Vec3
	// aim simulates what the last PlayerAuthInput carried; LookAt snaps it,
	// the way movement.LookAt does on the real bot.
	aimYaw   float32
	aimPitch float32
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		pos:      mgl32.Vec3{0.5, 64, 0.5},
		yaw:      0, // facing +Z
		entities: map[uint64]*entity.Info{},
		blocks:   map[string]string{},
		blockIDs: map[string]uint32{},
		players:  map[string]uint64{},
	}
}

func (f *fakeBot) GetCoords() mgl32.Vec3 { return f.pos }
func (f *fakeBot) GetYaw() float32       { return f.yaw }
func (f *fakeBot) GetEntities() map[uint64]*entity.Info {
	return f.entities
}

func (f *fakeBot) FindPlayer(username string) (uint64, mgl32.Vec3, bool) {
	for name, id := range f.players {
		if interact.Normalise(name) == interact.Normalise(username) {
			return id, f.entities[id].Position, true
		}
	}
	return 0, mgl32.Vec3{}, false
}

func (f *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	name, ok := f.blocks[interact.BlockKey(protocol.BlockPos{x, y, z})]
	return name, ok
}

func (f *fakeBot) GetBlockNetworkID(x, y, z int32) (uint32, bool) {
	rid, ok := f.blockIDs[interact.BlockKey(protocol.BlockPos{x, y, z})]
	return rid, ok
}

func (f *fakeBot) GetLastSentAim() (float32, float32) {
	return f.aimYaw, f.aimPitch
}

func (f *fakeBot) QueueItemInteractionData(data protocol.UseItemTransactionData) error {
	f.queued = append(f.queued, data)
	return nil
}

func (f *fakeBot) LookAt(pos mgl32.Vec3) {
	f.lookCalls++
	f.lookAt = pos
	f.aimYaw, f.aimPitch = interact.AimAngles(f.pos.Add(mgl32.Vec3{0, interact.EyeHeight, 0}), pos)
}

func (f *fakeBot) WritePacket(pk packet.Packet) error {
	f.packets = append(f.packets, pk)
	if f.clickActivates {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if data, ok := tx.TransactionData.(*protocol.UseItemTransactionData); ok && data.ActionType == protocol.UseItemActionClickBlock {
				key := interact.BlockKey(data.BlockPosition)
				if _, exists := f.blockIDs[key]; exists {
					f.blockIDs[key]++ // a new block state arrives on the wire
				}
			}
		}
	}
	return nil
}

func (f *fakeBot) GetEntityRuntimeID() uint64 { return 7 }
func (f *fakeBot) GetHeldItemSlot() uint32    { return 0 }
func (f *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack {
	return map[uint32]protocol.ItemStack{}
}
func (f *fakeBot) GetItemNames() map[int32]string { return map[int32]string{} }
func (f *fakeBot) ReportActionStatus(_ string, status event.ActionStatus) {
	f.statuses = append(f.statuses, status)
}
func (f *fakeBot) ResetLook() {}

func newTestInteractor(b *fakeBot) *interact.Interactor {
	return interact.New(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func addEntity(b *fakeBot, id uint64, entType, name string, x, y, z float32) {
	b.entities[id] = &entity.Info{
		ID:       id,
		Type:     entType,
		Name:     name,
		Position: mgl32.Vec3{x, y, z},
		Health:   20,
	}
}

// --- Request parsing ---

// TestParseRequestCoversRealWorldPhrasings is the regression guard for the
// original gap: a player saying "klik sign di depanku" has to resolve to a
// sign, not to "whatever happens to be nearest".
func TestParseRequestCoversRealWorldPhrasings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		param     string
		wantBlock string
		wantEnt   string
		wantFront bool
		// alsoEntity marks a word that legitimately has a second reading (see
		// TestParseRequestKeepsBothReadingsForAmbiguousWords).
		alsoEntity string
	}{
		{param: "sign", wantBlock: "sign"},
		{param: "papan di depan", wantBlock: "sign"},
		{param: "klik pintu", wantBlock: "door"},
		{param: "tombol", wantBlock: "button", alsoEntity: "join"},
		{param: "lever", wantBlock: "lever"},
		{param: "chest", wantBlock: "chest"},
		{param: "podium", wantBlock: "lectern"},
		{param: "klik podium", wantBlock: "lectern"},
		{param: "npc", wantEnt: "npc"},
		{param: "klik orang", wantEnt: "person"},
		{param: "villager", wantEnt: "villager"},
		{param: "join server", wantEnt: "join"},
		{param: "", wantFront: true},
		{param: "yang di depanku", wantFront: true},
		{param: "in front of you", wantFront: true},
		{param: "terdekat", wantFront: true},
	}

	for _, tc := range tests {
		t.Run(tc.param, func(t *testing.T) {
			t.Parallel()

			got := interact.ParseRequest(tc.param)
			if got.Block != tc.wantBlock {
				t.Errorf("Block = %q, want %q", got.Block, tc.wantBlock)
			}
			if tc.alsoEntity != "" {
				if got.Entity != tc.alsoEntity {
					t.Errorf("Entity = %q, want the second reading %q", got.Entity, tc.alsoEntity)
				}
			} else if got.Entity != tc.wantEnt {
				t.Errorf("Entity = %q, want %q", got.Entity, tc.wantEnt)
			}
			if got.InFront != tc.wantFront {
				t.Errorf("InFront = %v, want %v", got.InFront, tc.wantFront)
			}
			if tc.wantBlock == "" && tc.wantEnt == "" && !tc.wantFront {
				// Anything else must be treated as a player name, never silently
				// dropped.
				if got.Entity == "" && got.Block == "" {
					t.Error("request fell through to nothing, want a player-name target")
				}
			}
		})
	}
}

// TestParseRequestKeepsBothReadingsForAmbiguousWords covers the case that made
// "klik tombol" a coin flip: the word means both a button block and a join
// button. Parsing records both and leaves the tie-break to what is actually
// standing there.
func TestParseRequestKeepsBothReadingsForAmbiguousWords(t *testing.T) {
	t.Parallel()

	got := interact.ParseRequest("tombol")
	if got.Block != "button" {
		t.Errorf("Block = %q, want %q", got.Block, "button")
	}
	if got.Entity != "join" {
		t.Errorf("Entity = %q, want %q — the join-button reading must survive parsing", got.Entity, "join")
	}
	if got.InFront {
		t.Error("InFront set even though the word named something")
	}
}

// TestResolvePrefersRealBlockOverEntityForAmbiguousWord is the tie-break: with a
// stone button actually in front, "klik tombol" must hit the block.
func TestResolvePrefersRealBlockOverEntityForAmbiguousWord(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 64, 2})] = "minecraft:stone_button"
	// A join stand further away and off to the side.
	addEntity(b, 22, "minecraft:armor_stand", "Join Server", -2.5, 64, -1.5)

	target, err := newTestInteractor(b).Resolve(interact.ParseRequest("tombol"))
	if err != nil {
		t.Fatalf("Resolve(tombol) error = %v", err)
	}
	if target.Kind != interact.KindBlock {
		t.Fatalf("resolved kind = %v (%s), want the real button block", target.Kind, target.String())
	}
}

// TestResolveFallsBackToEntityWhenNoBlockExists is the other side of the same
// tie-break: with no button block nearby, the join stand is what gets clicked.
func TestResolveFallsBackToEntityWhenNoBlockExists(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	addEntity(b, 22, "minecraft:armor_stand", "Join Server", 0.5, 64, 2.5)

	target, err := newTestInteractor(b).Resolve(interact.ParseRequest("tombol"))
	if err != nil {
		t.Fatalf("Resolve(tombol) error = %v", err)
	}
	if target.Kind != interact.KindEntity || target.ID != 22 {
		t.Fatalf("resolved %+v, want the join stand entity 22", target)
	}
}

// TestEntityNameMatchesUsesDisplayName covers how hub buttons actually appear: an
// armour stand with a display name, not something the entity type would reveal.
func TestEntityNameMatchesUsesDisplayName(t *testing.T) {
	t.Parallel()

	stand := &entity.Info{Type: "minecraft:armor_stand", Name: "Join Server"}
	if !interact.EntityNameMatches(stand, "join") {
		t.Error("armour stand named 'Join Server' did not match the join concept")
	}
	if !interact.EntityNameMatches(&entity.Info{Type: "minecraft:npc"}, "npc") {
		t.Error("npc entity type did not match the npc concept")
	}
	if interact.EntityNameMatches(stand, "zombie") {
		t.Error("join stand matched the zombie concept")
	}
}

// TestBlockNameMatchesIgnoresNamespaceAndCase keeps matching forgiving enough
// for both the English block name and an Indonesian request.
func TestBlockNameMatchesIgnoresNamespaceAndCase(t *testing.T) {
	t.Parallel()

	if !interact.BlockNameMatches("minecraft:oak_door", "door") {
		t.Error("oak_door did not match the door concept")
	}
	if !interact.BlockNameMatches("minecraft:oak_sign", "sign") {
		t.Error("oak_sign did not match the sign concept")
	}
	if interact.BlockNameMatches("minecraft:stone", "door") {
		t.Error("plain stone matched the door concept")
	}
	if !interact.IsInteractiveBlockName("minecraft:stone_pressure_plate") {
		t.Error("pressure plate is not treated as interactive")
	}
	if interact.IsInteractiveBlockName("minecraft:stone") {
		t.Error("plain stone treated as interactive")
	}
}

// --- Geometry ---

// TestForwardVectorMatchesYawConvention pins the yaw conversion to the project
// convention (yaw = atan2(dz, dx) * 180/pi − 90). Getting the sign wrong makes
// "in front of you" point behind the bot.
func TestForwardVectorMatchesYawConvention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		yaw     float32
		wantX   float32
		wantZ   float32
		comment string
	}{
		{yaw: 0, wantX: 0, wantZ: 1, comment: "yaw 0 looks toward +Z"},
		{yaw: -90, wantX: 1, wantZ: 0, comment: "yaw -90 looks toward +X"},
		{yaw: 90, wantX: -1, wantZ: 0, comment: "yaw 90 looks toward -X"},
		{yaw: 180, wantX: 0, wantZ: -1, comment: "yaw 180 looks toward -Z"},
	}

	for _, tc := range tests {
		t.Run(tc.comment, func(t *testing.T) {
			t.Parallel()

			got := interact.ForwardVector(tc.yaw)
			if interact.Abs32(got.X()-tc.wantX) > 0.001 || interact.Abs32(got.Z()-tc.wantZ) > 0.001 {
				t.Fatalf("ForwardVector(%v) = %v, want x=%v z=%v", tc.yaw, got, tc.wantX, tc.wantZ)
			}
		})
	}
}

// TestWithinConeSeparatesFrontFromBack is what makes "in front of you" mean
// something: a target behind the bot must be rejected even when it is closer.
func TestWithinConeSeparatesFrontFromBack(t *testing.T) {
	t.Parallel()

	from := mgl32.Vec3{0, 64, 0}
	forward := interact.ForwardVector(0) // +Z

	if !interact.WithinCone(from, mgl32.Vec3{0, 64, 3}, forward, interact.FrontConeDegrees) {
		t.Error("target straight ahead was rejected")
	}
	if interact.WithinCone(from, mgl32.Vec3{0, 64, -3}, forward, interact.FrontConeDegrees) {
		t.Error("target straight behind was accepted")
	}
	if !interact.WithinCone(from, mgl32.Vec3{0, 64, 0.01}, forward, interact.FrontConeDegrees) {
		t.Error("target at the bot's feet was rejected, want it treated as in front")
	}
}

// TestBlockFaceTowardPicksDominantAxis covers the face a client reports clicking.
// A wrong face is silently ignored by the server, so the dominant axis has to be
// chosen from the bot's side of the block.
func TestBlockFaceTowardPicksDominantAxis(t *testing.T) {
	t.Parallel()

	pos := protocol.BlockPos{0, 64, 0}
	tests := []struct {
		name string
		from mgl32.Vec3
		want int32
	}{
		{name: "bot to the east", from: mgl32.Vec3{2.5, 64.5, 0.5}, want: 5},
		{name: "bot to the west", from: mgl32.Vec3{-1.5, 64.5, 0.5}, want: 4},
		{name: "bot to the south", from: mgl32.Vec3{0.5, 64.5, 2.5}, want: 3},
		{name: "bot to the north", from: mgl32.Vec3{0.5, 64.5, -1.5}, want: 2},
		{name: "bot above", from: mgl32.Vec3{0.5, 66.5, 0.5}, want: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := interact.BlockFaceToward(pos, tc.from); got != tc.want {
				t.Fatalf("BlockFaceToward from %v = %d, want %d", tc.from, got, tc.want)
			}
		})
	}
}

// --- Resolution ---

// TestResolvePrefersExplicitEntityOverLooseFrontWords covers "klik join server"
// while a player is standing right in front: the named concept has to win.
func TestResolvePrefersExplicitEntityOverLooseFrontWords(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	// A player 1 block in front, and a join button 3 blocks away.
	b.players["Arthenyxx"] = 11
	addEntity(b, 11, "minecraft:player", "Arthenyxx", 0.5, 64, 1.5)
	addEntity(b, 22, "minecraft:armor_stand", "Join Server", 0.5, 64, 3.5)

	target, err := newTestInteractor(b).Resolve(interact.ParseRequest("join server"))
	if err != nil {
		t.Fatalf("Resolve(join server) error = %v", err)
	}
	if target.ID != 22 {
		t.Fatalf("resolved entity %d, want the join stand 22", target.ID)
	}
}

// TestResolveInFrontIgnoresDroppedItems keeps "click the thing in front" aimed
// at an interactable, not at a loot drop it is standing on.
func TestResolveInFrontIgnoresDroppedItems(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	addEntity(b, 5, "minecraft:item", "minecraft:stick", 0.5, 64, 1.5)

	target, err := newTestInteractor(b).Resolve(interact.ParseRequest(""))
	if err == nil {
		t.Fatalf("Resolve(in front) = %+v, want no target when only a drop is there", target)
	}
}

// TestResolveReportsNamedTargetOutOfReach checks a named target that is too far
// away fails loudly instead of clicking something else nearby.
func TestResolveReportsNamedTargetOutOfReach(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	addEntity(b, 9, "minecraft:villager", "minecraft:villager", 0.5, 64, 20.5)

	if _, err := newTestInteractor(b).Resolve(interact.ParseRequest("villager")); err == nil {
		t.Fatal("Resolve(villager 20 blocks away) succeeded, want a not-found error")
	}
}

// TestResolveInFrontFindsBlockEntity covers the sign/button case with no entity
// around, which is the block half of the feature.
func TestResolveInFrontFindsBlockEntity(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	// A door two blocks north (+Z is forward at yaw 0), outside the bot's own cell.
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 64, 2})] = "minecraft:oak_door"

	target, err := newTestInteractor(b).Resolve(interact.ParseRequest(""))
	if err != nil {
		t.Fatalf("Resolve(in front) error = %v", err)
	}
	if target.Kind != interact.KindBlock {
		t.Fatalf("resolved kind = %v, want a block", target.Kind)
	}
	if target.Block != (protocol.BlockPos{0, 64, 2}) {
		t.Fatalf("resolved block = %v, want the door at 0,64,2", target.Block)
	}
	if target.Face != 2 { // bot is at -Z relative to the door: north face
		t.Fatalf("resolved face = %d, want 2 (north)", target.Face)
	}
}

// TestResolveNamedBlockSkipsPlainBlocks makes sure a named "chest" does not
// resolve to the wall behind it.
func TestResolveNamedBlockSkipsPlainBlocks(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 64, 1})] = "minecraft:stone"
	b.blocks[interact.BlockKey(protocol.BlockPos{2, 64, 1})] = "minecraft:chest"

	target, err := newTestInteractor(b).Resolve(interact.ParseRequest("chest"))
	if err != nil {
		t.Fatalf("Resolve(chest) error = %v", err)
	}
	if target.Block != (protocol.BlockPos{2, 64, 1}) {
		t.Fatalf("resolved block = %v, want the chest at 2,64,1", target.Block)
	}
}

// --- Wire format ---

// TestInteractEntitySendsHoverThenOpen pins the entity wire sequence. Sending
// only one of the two is the usual reason a server button "does nothing".
func TestInteractEntitySendsHoverThenOpen(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	addEntity(b, 22, "minecraft:armor_stand", "Join Server", 0.5, 64, 2.5)

	newTestInteractor(b).Interact(context.Background(), "tester", "join server")

	// Sequence: hover, swing, open. Sending only one of the two Interact
	// packets is the usual reason a server button "does nothing"; the swing in
	// between is the arm movement a real player makes while tapping.
	if len(b.packets) < 3 {
		t.Fatalf("wrote %d packets, want hover, swing and open", len(b.packets))
	}
	hover, ok := b.packets[0].(*packet.Interact)
	if !ok {
		t.Fatalf("first packet = %T, want *packet.Interact", b.packets[0])
	}
	if hover.ActionType != packet.InteractActionMouseOverEntity {
		t.Errorf("first action = %d, want MouseOverEntity (%d)", hover.ActionType, packet.InteractActionMouseOverEntity)
	}
	if hover.TargetEntityRuntimeID != 22 {
		t.Errorf("hover target = %d, want 22", hover.TargetEntityRuntimeID)
	}

	if _, isSwing := b.packets[1].(*packet.Animate); !isSwing {
		t.Errorf("second packet = %T, want *packet.Animate (the arm swing)", b.packets[1])
	}

	open, ok := b.packets[2].(*packet.Interact)
	if !ok {
		t.Fatalf("third packet = %T, want *packet.Interact", b.packets[2])
	}
	if open.ActionType != packet.InteractActionNPCOpen {
		t.Errorf("third action = %d, want NPCOpen (%d)", open.ActionType, packet.InteractActionNPCOpen)
	}

	if b.lookCalls == 0 {
		t.Error("never looked at the target before clicking; the server drops clicks that arrive before the look settles")
	}
	if len(b.statuses) != 1 || !b.statuses[0].Success {
		t.Fatalf("statuses = %+v, want one success report", b.statuses)
	}
}

// TestInteractBlockSendsUseItemOnSequence pins the block-entity click as a
// vanilla client sends it: one arm swing, one UseItem ClickBlock transaction,
// and NOTHING else. The earlier version wrapped the transaction in
// PlayerActionStartItemUseOn/StopItemUseOn with ClientPrediction=Success — the
// hold-to-use prediction shape — and on real servers the button was never
// actually pressed. Any reintroduction of those packets should fail this test.
func TestInteractBlockSendsUseItemOnSequence(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 64, 2})] = "minecraft:oak_door"
	b.blockIDs[interact.BlockKey(protocol.BlockPos{0, 64, 2})] = 500
	b.clickActivates = true

	newTestInteractor(b).Interact(context.Background(), "tester", "door")

	if len(b.packets) != 2 {
		t.Fatalf("wrote %d packets, want exactly swing/transaction", len(b.packets))
	}

	swing, ok := b.packets[0].(*packet.Animate)
	if !ok {
		t.Fatalf("first packet = %T, want *packet.Animate", b.packets[0])
	}
	if swing.ActionType != packet.AnimateActionSwingArm {
		t.Errorf("swing action = %d, want SwingArm (%d)", swing.ActionType, packet.AnimateActionSwingArm)
	}
	if swing.SwingSource != packet.AnimateSwingSourceInteract {
		t.Errorf("swing source = %d, want Interact (%d); a mine-swing reads as digging, not clicking", swing.SwingSource, packet.AnimateSwingSourceInteract)
	}

	tx, ok := b.packets[1].(*packet.InventoryTransaction)
	if !ok {
		t.Fatalf("second packet = %T, want *packet.InventoryTransaction", b.packets[1])
	}
	data, ok := tx.TransactionData.(*protocol.UseItemTransactionData)
	if !ok {
		t.Fatalf("transaction data = %T, want *protocol.UseItemTransactionData", tx.TransactionData)
	}
	if data.ActionType != protocol.UseItemActionClickBlock {
		t.Errorf("action = %d, want ClickBlock (%d)", data.ActionType, protocol.UseItemActionClickBlock)
	}
	if data.TriggerType != protocol.TriggerTypePlayerInput {
		t.Errorf("trigger = %d, want PlayerInput (%d)", data.TriggerType, protocol.TriggerTypePlayerInput)
	}
	if data.ClientPrediction != 0 {
		t.Errorf("client prediction = %d, want 0; the working placement path leaves it unset", data.ClientPrediction)
	}
	if data.BlockRuntimeID != 500 {
		t.Errorf("block runtime ID = %d, want 500; vanilla echoes the clicked block's network ID and hosts drop transactions that do not", data.BlockRuntimeID)
	}
	if want := b.pos.Add(mgl32.Vec3{0, 1.62, 0}); data.Position != want {
		t.Errorf("position = %v, want eye position %v matching PlayerAuthInput", data.Position, want)
	}
	if data.ClickedPosition == (mgl32.Vec3{}) {
		t.Errorf("clicked position = zero vector, want a point on the clicked face")
	}
	for _, pk := range b.packets {
		if _, isAction := pk.(*packet.PlayerAction); isAction {
			t.Errorf("unexpected PlayerAction packet %v in a plain click", pk)
		}
	}
	if len(b.queued) != 0 {
		t.Errorf("inline fallback queued %d interactions after a click that landed, want none", len(b.queued))
	}
	if len(b.statuses) != 1 || !b.statuses[0].Success {
		t.Fatalf("statuses = %+v, want one success report", b.statuses)
	}
}

// TestInteractBlockFallsBackInlineWhenIgnored covers a host that ignores the
// standalone InventoryTransaction: the same click must be retried through the
// inline PlayerAuthInput path, and the report must stay honest that the block
// never reacted instead of claiming success.
func TestInteractBlockFallsBackInlineWhenIgnored(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 64, 2})] = "minecraft:stone_button"
	b.blockIDs[interact.BlockKey(protocol.BlockPos{0, 64, 2})] = 900

	newTestInteractor(b).Interact(context.Background(), "tester", "button")

	swings := 0
	for _, pk := range b.packets {
		if _, ok := pk.(*packet.Animate); ok {
			swings++
		}
	}
	if swings != 2 {
		t.Errorf("sent %d swings, want 2 (one per transport attempt)", swings)
	}
	if len(b.queued) != 1 {
		t.Fatalf("queued %d inline interactions, want exactly one fallback", len(b.queued))
	}
	if b.queued[0].ActionType != protocol.UseItemActionClickBlock {
		t.Errorf("queued action = %d, want ClickBlock", b.queued[0].ActionType)
	}
	if b.queued[0].BlockRuntimeID != 900 {
		t.Errorf("queued block runtime ID = %d, want 900 echoed from the cache", b.queued[0].BlockRuntimeID)
	}
	if len(b.statuses) != 1 || b.statuses[0].Success {
		t.Fatalf("statuses = %+v, want one honest failure report", b.statuses)
	}
	if b.statuses[0].Error == "" {
		t.Error("failure reported without a reason")
	}
}

// TestInteractBlockUIBlockQueuesInlineFallback covers blocks whose activation
// opens a screen instead of changing the block: there is nothing to verify, so
// both transports are used unconditionally and the click still reports
// success, because an unobservable open is not a failure.
func TestInteractBlockUIBlockQueuesInlineFallback(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 64, 2})] = "minecraft:chest"

	newTestInteractor(b).Interact(context.Background(), "tester", "chest")

	if len(b.queued) != 1 {
		t.Fatalf("queued %d inline interactions, want one for the UI fallback", len(b.queued))
	}
	if len(b.statuses) != 1 || !b.statuses[0].Success {
		t.Fatalf("statuses = %+v, want one success report", b.statuses)
	}
}

// TestAimAngles pins the yaw/pitch convention against the movement loop's: 0
// faces +Z, 90 faces −X, negative pitch looks up. If these drift from
// movement.LookAt, waitForAim would wait out its timeout on every click.
func TestAimAngles(t *testing.T) {
	t.Parallel()

	from := mgl32.Vec3{0, 65.62, 0}
	tests := []struct {
		to        mgl32.Vec3
		wantYaw   float32
		wantPitch float32
	}{
		{to: mgl32.Vec3{0, 65.62, 5}, wantYaw: 0, wantPitch: 0},   // straight +Z
		{to: mgl32.Vec3{-5, 65.62, 0}, wantYaw: 90, wantPitch: 0}, // −X (west)
		{to: mgl32.Vec3{0, 70.62, 5}, wantYaw: 0, wantPitch: -45}, // up 45°
		{to: mgl32.Vec3{0, 60.62, 5}, wantYaw: 0, wantPitch: 45},  // down 45°
	}
	for _, tc := range tests {
		yaw, pitch := interact.AimAngles(from, tc.to)
		if interact.AngleDelta(yaw, tc.wantYaw) > 0.01 || interact.AngleDelta(pitch, tc.wantPitch) > 0.01 {
			t.Errorf("aimAngles(%v) = (%v, %v), want (%v, %v)", tc.to, yaw, pitch, tc.wantYaw, tc.wantPitch)
		}
	}
}

// TestInteractNPCAlsoSendsDialogueRequest checks NPC figures get the dialogue
// request too, since Interact alone leaves an Education NPC inert.
func TestInteractNPCAlsoSendsDialogueRequest(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	addEntity(b, 33, "minecraft:npc", "minecraft:npc", 0.5, 64, 2.5)

	newTestInteractor(b).Interact(context.Background(), "tester", "npc")

	found := false
	for _, pk := range b.packets {
		if req, ok := pk.(*packet.NPCRequest); ok {
			found = true
			if req.EntityRuntimeID != 33 {
				t.Errorf("NPCRequest target = %d, want 33", req.EntityRuntimeID)
			}
		}
	}
	if !found {
		t.Fatal("no NPCRequest sent for an NPC target")
	}
}

// TestInteractReportsMissingTarget makes a failed click honest: the model has to
// learn nothing was there rather than believe it worked.
func TestInteractReportsMissingTarget(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	newTestInteractor(b).Interact(context.Background(), "tester", "npc")

	if len(b.packets) != 0 {
		t.Fatalf("wrote %d packets with no target in range, want none", len(b.packets))
	}
	if len(b.statuses) != 1 {
		t.Fatalf("statuses = %+v, want exactly one report", b.statuses)
	}
	if b.statuses[0].Success {
		t.Fatal("reported success with no target in range")
	}
	if b.statuses[0].Error == "" {
		t.Error("failure reported without a reason")
	}
}

// TestInteractCancelsWithContext keeps a cancelled request from continuing to
// click after the caller gave up, and makes the report honest about it.
func TestInteractCancelsWithContext(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 64, 2})] = "minecraft:oak_door"

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	newTestInteractor(b).Interact(ctx, "tester", "door")

	if len(b.packets) != 0 {
		t.Fatalf("wrote %d packets after cancellation, want none", len(b.packets))
	}
	if len(b.statuses) != 1 {
		t.Fatalf("statuses = %+v, want exactly one report", b.statuses)
	}
	if b.statuses[0].Success {
		t.Fatal("interact reported success on a cancelled click, want a failure report")
	}
	if b.statuses[0].Error == "" {
		t.Fatal("failure report carries no reason")
	}
}

// TestInteractEntitySwingsArm pins the arm movement on entity clicks: tapping
// an NPC or server figure with a frozen arm is the exact bot tell the swing
// source field exists for.
func TestInteractEntitySwingsArm(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	addEntity(b, 44, "minecraft:armor_stand", "Join Server", 0.5, 64, 2.5)

	newTestInteractor(b).Interact(context.Background(), "tester", "join server")

	found := false
	for _, pk := range b.packets {
		if swing, ok := pk.(*packet.Animate); ok {
			found = true
			if swing.ActionType != packet.AnimateActionSwingArm {
				t.Errorf("swing action = %d, want SwingArm (%d)", swing.ActionType, packet.AnimateActionSwingArm)
			}
			if swing.SwingSource != packet.AnimateSwingSourceInteract {
				t.Errorf("swing source = %d, want Interact (%d)", swing.SwingSource, packet.AnimateSwingSourceInteract)
			}
		}
	}
	if !found {
		t.Fatal("entity click sent no arm swing")
	}
}

// TestFaceClickedPosition pins the click point per face — the protocol expects
// it relative to the block corner, on the face being pressed.
func TestFaceClickedPosition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		face int32
		want mgl32.Vec3
	}{
		{face: 0, want: mgl32.Vec3{0.5, 0.0, 0.5}}, // down
		{face: 1, want: mgl32.Vec3{0.5, 1.0, 0.5}}, // up
		{face: 2, want: mgl32.Vec3{0.5, 0.5, 0.1}}, // north
		{face: 3, want: mgl32.Vec3{0.5, 0.5, 0.9}}, // south
		{face: 4, want: mgl32.Vec3{0.1, 0.5, 0.5}}, // west
		{face: 5, want: mgl32.Vec3{0.9, 0.5, 0.5}}, // east
	}
	for _, tc := range tests {
		if got := interact.FaceClickedPosition(tc.face); got != tc.want {
			t.Errorf("faceClickedPosition(%d) = %v, want %v", tc.face, got, tc.want)
		}
	}
}

// TestResolveCustomBlockByName covers behaviour-pack blocks: no alias list will
// ever contain them, so the words the player said have to be matched against
// the block names actually standing nearby.
func TestResolveCustomBlockByName(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 64, 3})] = "custom:elevator_block"

	target, err := newTestInteractor(b).Resolve(interact.ParseRequest("elevator_block"))
	if err != nil {
		t.Fatalf("custom block not resolved: %v", err)
	}
	if target.Kind != interact.KindBlock {
		t.Fatalf("kind = %v, want KindBlock", target.Kind)
	}
	if target.Block != (protocol.BlockPos{0, 64, 3}) {
		t.Errorf("block = %v, want the custom block's position", target.Block)
	}
}
