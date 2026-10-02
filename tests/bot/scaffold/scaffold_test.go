package scaffold_test

import (
	"context"
	"testing"
	"time"

	"bedrock-ai/internal/bot/scaffold"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Two failures sent the bot up a hill and stopped it dead, and both were a
// missing question rather than a wrong answer.
//
// The cell a block goes into was never checked, so a tuft of grass refused every
// placement on any grassy slope. And the "jump" was the jump emote, so the body
// never left the floor and a placement aimed at the cell the player was standing
// in came back refused as intersecting an entity — the block that "sometimes did
// not appear".

// fakeBot is a world the package can be pointed at. cells is the world's block
// map; jumpSucceeds decides whether a jump request actually gets the body off
// the floor.
type fakeBot struct {
	cells        map[[3]int32]string
	pos          mgl32.Vec3
	grounded     bool
	jumpSucceeds bool
	jumpAsked    int
	jumpPending  bool
	placed       []protocol.BlockPos
	broken       []protocol.BlockPos
	// held is the scaffold stack the fake is carrying, with its server-side
	// stack network ID; refWireIDs wires support blocks to fake network IDs
	// the way a hashed-ID connection would.
	held        protocol.ItemStack
	heldStackID int32
	refWireIDs  map[[3]int32]uint32
	// useOnSequence records the vanilla placement packets the fake sees, in
	// order: a start, the single place swing, the transaction, then the stop.
	useOnSequence []string
}

func newFakeBot(cells map[[3]int32]string) *fakeBot {
	if cells == nil {
		// Always a real map: the placement path writes into it, and a nil map
		// would panic on the write rather than fail the assertion the test is
		// actually about.
		cells = make(map[[3]int32]string)
	}
	return &fakeBot{
		cells:        cells,
		grounded:     true,
		jumpSucceeds: true,
		held:         protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 3, MetadataValue: 0}, Count: 16},
		heldStackID:  7,
		refWireIDs:   make(map[[3]int32]uint32),
	}
}

func (b *fakeBot) GetBlockNetworkID(x, y, z int32) (uint32, bool) {
	if id, ok := b.refWireIDs[[3]int32{x, y, z}]; ok {
		return id, true
	}
	// A support block the world knows about always has a wire ID; an unknown
	// cell is an unloaded one.
	if _, loaded := b.GetBlockName(x, y, z); loaded {
		return 1000 + uint32(x+y+z), true
	}
	return 0, false
}

func (b *fakeBot) HeldItemInstance() (uint32, protocol.ItemInstance, bool) {
	if b.held.Count == 0 {
		return 0, protocol.ItemInstance{}, false
	}
	return 0, protocol.ItemInstance{StackNetworkID: b.heldStackID, Stack: b.held}, true
}

func (b *fakeBot) GetCoords() mgl32.Vec3      { return b.pos }
func (b *fakeBot) GetEntityRuntimeID() uint64 { return 1 }
func (b *fakeBot) GetHeldItemSlot() uint32    { return 0 }
func (b *fakeBot) LookAt(mgl32.Vec3)          {}

func (b *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	name, ok := b.cells[[3]int32{x, y, z}]
	return name, ok
}

func (b *fakeBot) WritePacket(pk packet.Packet) error {
	switch p := pk.(type) {
	case *packet.Animate:
		if p.ActionType == packet.AnimateActionSwingArm {
			b.useOnSequence = append(b.useOnSequence, "swing")
		}
	case *packet.PlayerAction:
		switch p.ActionType {
		case protocol.PlayerActionStartItemUseOn:
			b.useOnSequence = append(b.useOnSequence, "start")
		case protocol.PlayerActionStopItemUseOn:
			b.useOnSequence = append(b.useOnSequence, "stop")
		case protocol.PlayerActionPredictDestroyBlock:
			// A break prediction the server honours: the block is gone.
			b.broken = append(b.broken, p.BlockPosition)
			delete(b.cells, [3]int32{p.BlockPosition.X(), p.BlockPosition.Y(), p.BlockPosition.Z()})
		}
	case *packet.InventoryTransaction:
		if d, ok := p.TransactionData.(*protocol.UseItemTransactionData); ok {
			// The server honours a placement into an empty cell and refuses one
			// into an occupied cell. Modelling that is what lets the confirmation
			// path be tested at all.
			b.useOnSequence = append(b.useOnSequence, "transaction")
			// The fake server honours a vanilla placement and silently refuses
			// anything else — which is the live bug under test.
			if d.TriggerType != protocol.TriggerTypePlayerInput {
				return nil
			}
			if d.ClientPrediction != protocol.ClientPredictionSuccess {
				return nil
			}
			if d.HeldItem.StackNetworkID == 0 {
				return nil
			}
			if rid, ok := b.GetBlockNetworkID(d.BlockPosition.X(), d.BlockPosition.Y(), d.BlockPosition.Z()); !ok || rid != d.BlockRuntimeID {
				return nil
			}
			cell := protocol.BlockPos{d.BlockPosition.X(), d.BlockPosition.Y() + 1, d.BlockPosition.Z()}
			b.placed = append(b.placed, cell)
			if _, occupied := b.GetBlockName(cell.X(), cell.Y(), cell.Z()); !occupied {
				b.cells[[3]int32{cell.X(), cell.Y(), cell.Z()}] = "minecraft:dirt"
			}
		}
	}
	return nil
}

func (b *fakeBot) RequestJump() {
	b.jumpAsked++
	// The movement loop consumes the request; whether the body actually leaves
	// the floor is the loop's answer, and the fake stands in for it — including
	// the rise, because a placement needs a body past the top of the cell, not
	// merely "not grounded".
	b.jumpPending = false
	if b.jumpSucceeds {
		b.grounded = false
		b.pos = b.pos.Add(mgl32.Vec3{0, 1.1, 0})
	}
}

func (b *fakeBot) JumpRequested() bool { return b.jumpPending }
func (b *fakeBot) Grounded() bool      { return b.grounded }

// TestGrassIsClearedBeforeAPlacement is the reported bug. A cell holding grass
// refuses a placement, so it has to be emptied first — and grass costs nothing
// to remove.
func TestGrassIsClearedBeforeAPlacement(t *testing.T) {
	t.Parallel()

	cell := protocol.BlockPos{4, 64, 7}
	b := newFakeBot(map[[3]int32]string{{4, 64, 7}: "minecraft:short_grass"})
	bot := scaffold.Bot(b)

	if _, occupied := scaffold.Occupied(bot, cell); !occupied {
		t.Fatal("the grass cell reads as empty; the test is not modelling anything")
	}

	cleared, reason := scaffold.ClearCell(context.Background(), bot, cell, false, scaffold.TierHand)
	if !cleared {
		t.Fatalf("ClearCell refused to clear grass: %s", reason)
	}
	if _, stillThere := scaffold.Occupied(bot, cell); stillThere {
		t.Error("the cell still holds something after ClearCell reported success")
	}
}

// TestARealBlockIsNotBrokenForAPathStep is the other half of the rule. A path
// step may clear a plant; it may not quarry a hole through a wall. Only a tower,
// which has no other way up, asks for breakThrough.
func TestARealBlockIsNotBrokenForAPathStep(t *testing.T) {
	t.Parallel()

	cell := protocol.BlockPos{1, 64, 1}
	b := newFakeBot(map[[3]int32]string{{1, 64, 1}: "minecraft:stone"})
	bot := scaffold.Bot(b)

	cleared, reason := scaffold.ClearCell(context.Background(), bot, cell, false, scaffold.TierHand)
	if cleared {
		t.Error("ClearCell broke a stone block for a path step that did not ask for it")
	}
	if reason == "" {
		t.Error("ClearCell refused without saying why; the caller has nothing to report")
	}
	if len(b.broken) != 0 {
		t.Error("a stone block was broken for a path step")
	}

	// With breakThrough a tower may take it, and it is genuinely gone afterwards.
	cleared, _ = scaffold.ClearCell(context.Background(), bot, cell, true, scaffold.TierHand)
	if !cleared {
		t.Error("a tower was not allowed to break the stone in its way")
	}
	if len(b.broken) == 0 {
		t.Error("breakThrough cleared the cell without sending a destroy")
	}
}

// TestATowerPlacesOnlyAfterLeavingTheGround is the other reported bug. A body
// still on the floor has its own cell occupied, and the server refuses a
// placement into it — so the jump has to be real and has to be waited for.
func TestATowerPlacesOnlyAfterLeavingTheGround(t *testing.T) {
	t.Parallel()

	ref := protocol.BlockPos{2, 63, 2}
	cell := scaffold.PlaceCell(ref)
	// The tower stands on something: a support with no wire ID is an honest
	// refusal, not what this test is about.
	b := newFakeBot(map[[3]int32]string{{2, 63, 2}: "minecraft:dirt"})
	b.pos = mgl32.Vec3{2.2, 64.0, 2.1}
	bot := scaffold.Bot(b)

	ok, reason := scaffold.JumpAndWait(context.Background(), bot, cell, 600*time.Millisecond)
	if !ok {
		t.Fatalf("the jump did not happen: %s", reason)
	}
	if b.jumpAsked == 0 {
		t.Error("JumpAndWait reported the body clear of the cell without ever asking for a jump")
	}

	placed, reason := scaffold.PlaceVerified(context.Background(), bot, ref, b.held)
	if !placed {
		t.Fatalf("the placement was not confirmed: %s", reason)
	}
	if _, occupied := scaffold.Occupied(bot, cell); !occupied {
		t.Error("PlaceVerified reported success but the cell is still empty")
	}
}

// TestAJumpThatNeverLeavesTheFloorIsAFailure is the honest default. A body that
// asks to jump and stays down must not be reported as airborne, because the
// placement that follows is a placement into the player's own feet.
func TestAJumpThatNeverLeavesTheFloorIsAFailure(t *testing.T) {
	t.Parallel()

	b := newFakeBot(nil)
	b.pos = mgl32.Vec3{2.2, 64.0, 2.1}
	b.jumpSucceeds = false

	ok, reason := scaffold.JumpAndWait(context.Background(), scaffold.Bot(b), protocol.BlockPos{2, 64, 2}, 200*time.Millisecond)
	if ok {
		t.Error("JumpAndWait reported success for a body that never cleared the cell")
	}
	if reason == "" {
		t.Error("the refusal did not say why")
	}
	if b.jumpAsked == 0 {
		t.Error("no jump was even requested")
	}
}

// TestABodyAlreadyClearOfTheCellNeedsNoJump. A body beside or risen past the
// cell is in the one state a placement needs, and asking for a jump here is a
// request that should never have been made.
func TestABodyAlreadyClearOfTheCellNeedsNoJump(t *testing.T) {
	t.Parallel()

	b := newFakeBot(nil)
	b.grounded = false
	b.pos = mgl32.Vec3{2.2, 65.5, 2.1}

	ok, _ := scaffold.JumpAndWait(context.Background(), scaffold.Bot(b), protocol.BlockPos{2, 64, 2}, 200*time.Millisecond)
	if !ok {
		t.Error("a body already clear of the cell was told it could not jump")
	}
	if b.jumpAsked != 0 {
		t.Error("a jump was requested for a body that was already clear of the cell")
	}
}

// TestARefusedPlacementIsNotReportedAsSuccess. The old code wrote the
// transaction and then told the world model the block was there, so a refusal —
// the common case — was recorded as a success.
func TestARefusedPlacementIsNotReportedAsSuccess(t *testing.T) {
	t.Parallel()

	ref := protocol.BlockPos{3, 63, 3}
	cell := scaffold.PlaceCell(ref)
	// The cell is occupied by something the caller did not clear, and the server
	// will refuse the placement.
	b := newFakeBot(map[[3]int32]string{{3, 64, 3}: "minecraft:chest"})
	b.grounded = false
	bot := scaffold.Bot(b)

	placed, reason := scaffold.PlaceVerified(context.Background(), bot, ref, b.held)
	if placed {
		t.Error("a placement into an occupied cell was reported as a success")
	}
	if reason == "" {
		t.Error("the refusal did not say why")
	}
	if _, nowOccupied := scaffold.Occupied(bot, cell); !nowOccupied {
		t.Error("the chest vanished; the fake is not refusing as a server would")
	}
}

// TestAnUnloadedCellIsFreeNotOccupied. "I cannot see it" is not "something is in
// the way", and treating it as occupied would send the bot breaking blocks it
// never saw.
func TestAnUnloadedCellIsFreeNotOccupied(t *testing.T) {
	t.Parallel()

	b := newFakeBot(nil)
	if _, occupied := scaffold.Occupied(scaffold.Bot(b), protocol.BlockPos{9, 9, 9}); occupied {
		t.Error("an unloaded cell was reported as occupied")
	}
	cleared, _ := scaffold.ClearCell(context.Background(), scaffold.Bot(b), protocol.BlockPos{9, 9, 9}, true, scaffold.TierHand)
	if !cleared {
		t.Error("an unloaded cell was not reported as free")
	}
	if len(b.broken) != 0 {
		t.Error("the bot tried to break a block in a cell it could not see")
	}
}

// TestTheReplaceableTableIsTheOneAPlayerWouldUse pins the distinction the whole
// feature rests on: you can place into grass, you cannot place into rock.
func TestTheReplaceableTableIsTheOneAPlayerWouldUse(t *testing.T) {
	t.Parallel()

	plants := []string{
		"short_grass", "tall_grass", "fern", "dead_bush", "poppy", "dandelion",
		"red_tulip", "brown_mushroom", "crimson_fungus", "oak_sapling",
		"seagrass", "kelp", "snow_layer", "carpet", "vine", "torch",
		"minecraft:short_grass", "SHORT_GRASS",
	}
	for _, name := range plants {
		if !scaffold.IsReplaceable(name) {
			t.Errorf("IsReplaceable(%q) = false; a player places straight through that", name)
		}
		if d := scaffold.BreakDuration(name); d != 0 {
			t.Errorf("BreakDuration(%q) = %v, want 0: a plant is not something to wait for", name, d)
		}
	}

	structures := []string{
		"stone", "oak_log", "chest", "furnace", "obsidian", "bedrock",
		"dirt", "cobblestone", "minecraft:stone", "glass",
	}
	for _, name := range structures {
		if scaffold.IsReplaceable(name) {
			t.Errorf("IsReplaceable(%q) = true; the bot would quarry a wall to lay one block of path", name)
		}
		if scaffold.BreakDuration(name) <= 0 {
			t.Errorf("BreakDuration(%q) = 0; that block does not vanish when touched", name)
		}
	}

	// Air is free, and not because it is replaceable — it needs no breaking.
	if !scaffold.IsReplaceable("air") || !scaffold.IsReplaceable("cave_air") {
		t.Error("air was not reported as free")
	}
}

// TestTheAimSitsAboveTheTopFace is why placements used to be refused. The old
// code aimed half a block below the feet, which is inside the block being stood
// on, so the crosshair was never on the face the server checks.
func TestTheAimSitsAboveTheTopFace(t *testing.T) {
	t.Parallel()

	ref := protocol.BlockPos{6, 63, 6}
	aim := scaffold.AimPoint(ref)

	if aim.X() != 6.5 || aim.Z() != 6.5 {
		t.Errorf("aim x/z = %v/%v, want 6.5/6.5: the crosshair must be over the block being placed on", aim.X(), aim.Z())
	}
	// The top face of the block at y=63 is the plane at y=64.
	if aim.Y() <= float32(ref.Y())+1 {
		t.Errorf("aim y = %v, want above the top face at %v; aiming into the block is what got placements refused", aim.Y(), ref.Y()+1)
	}
}

// TestAPlacementSpeaksTheVanillaSequence is the live bug pinned down. The log
// filled with "the server never placed the block" because PlaceVerified sent a
// bare transaction: no use-on start/stop around it, no trigger type, no client
// prediction, no stack network ID on the held item, and no wire ID for the
// support block. The fake server above refuses exactly those, so a regression
// here fails the same way the live server did — quietly.
func TestAPlacementSpeaksTheVanillaSequence(t *testing.T) {
	t.Parallel()

	ref := protocol.BlockPos{5, 63, 5}
	cell := scaffold.PlaceCell(ref)
	b := newFakeBot(map[[3]int32]string{{5, 63, 5}: "minecraft:dirt"})
	b.grounded = false

	placed, reason := scaffold.PlaceVerified(context.Background(), scaffold.Bot(b), ref, b.held)
	if !placed {
		t.Fatalf("the placement was not confirmed: %s", reason)
	}
	if _, occupied := scaffold.Occupied(scaffold.Bot(b), cell); !occupied {
		t.Error("PlaceVerified reported success but the cell is still empty")
	}

	want := []string{"start", "swing", "transaction", "stop"}
	if len(b.useOnSequence) != len(want) {
		t.Fatalf("placement packets = %v, want %v", b.useOnSequence, want)
	}
	for i := range want {
		if b.useOnSequence[i] != want[i] {
			t.Fatalf("placement packets = %v, want %v: the order is the vanilla sequence", b.useOnSequence, want)
		}
	}
}

// TestAnEmptySlotIsNotPlaced. With nothing held there is no stack network ID
// to send, so the call must say so instead of sending a transaction that reads
// as a nameless click.
func TestAnEmptySlotIsNotPlaced(t *testing.T) {
	t.Parallel()

	ref := protocol.BlockPos{7, 63, 7}
	b := newFakeBot(map[[3]int32]string{{7, 63, 7}: "minecraft:dirt"})
	b.grounded = false
	b.held = protocol.ItemStack{}
	want := protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 3}, Count: 1}

	placed, reason := scaffold.PlaceVerified(context.Background(), scaffold.Bot(b), ref, want)
	if placed {
		t.Error("a placement with an empty held slot was reported as a success")
	}
	if reason == "" {
		t.Error("the refusal did not say why")
	}
	if len(b.placed) != 0 {
		t.Error("a transaction went out with nothing held")
	}
}

// TestAnUnloadedSupportIsNotPlaced. Without a wire ID for the support block
// there is nothing truthful to put in the transaction, and sending a zero in
// its place is a silent refusal on a hashed-ID connection.
func TestAnUnloadedSupportIsNotPlaced(t *testing.T) {
	t.Parallel()

	ref := protocol.BlockPos{11, 63, 11}
	b := newFakeBot(nil)
	b.grounded = false
	// The support cell holds nothing the world knows, so it has no wire ID —
	// but the destination must still be free or the call refuses for the wrong
	// reason and the test proves nothing.
	delete(b.cells, [3]int32{ref.X(), ref.Y(), ref.Z()})

	placed, reason := scaffold.PlaceVerified(context.Background(), scaffold.Bot(b), ref, b.held)
	if placed {
		t.Error("a placement with no support wire ID was reported as a success")
	}
	if reason == "" {
		t.Error("the refusal did not say why")
	}
	if len(b.useOnSequence) != 0 {
		t.Errorf("packets went out with no support wire ID: %v", b.useOnSequence)
	}
}

// TestTowerColumnIsTheBlockUnderTheFeet ties the tower geometry together: the
// reference is one below the body and the cell that ends up filled is the one the
// body is standing in — which is exactly why it must leave the floor first.
func TestTowerColumnIsTheBlockUnderTheFeet(t *testing.T) {
	t.Parallel()

	ref, cell := scaffold.TowerColumn(mgl32.Vec3{10.9, 64.3, -3.2})
	if ref.X() != 10 || ref.Y() != 63 || ref.Z() != -4 {
		t.Errorf("ref = %v, want (10,63,-4): the block directly under the feet, floored", ref)
	}
	if cell.Y() != 64 {
		t.Errorf("cell y = %d, want 64: the cell the body is standing in", cell.Y())
	}
	if cell.X() != ref.X() || cell.Z() != ref.Z() {
		t.Errorf("cell %v is not above ref %v", cell, ref)
	}
}

// TestBodyBlocksCellUsesTheRealBodyWidth is the swing-without-a-block bug in one
// predicate. A body 0.7 blocks off the column centre still clips the cell (the
// body is 0.6 wide), so a tolerance that called that body clear let it skip its
// jump and swing at a placement the server would never honour.
func TestBodyBlocksCellUsesTheRealBodyWidth(t *testing.T) {
	t.Parallel()

	cell := protocol.BlockPos{0, 64, 0}

	// Standing in the cell: the tower case, needs a jump.
	if !scaffold.BodyBlocksCell(mgl32.Vec3{0.5, 64.0, 0.5}, cell) {
		t.Error("a body standing in the cell was called clear")
	}
	// Clipping the cell from 0.7 off-centre: the body spans [0.9, 1.5] over the
	// cell [0, 1]. Still blocking.
	if !scaffold.BodyBlocksCell(mgl32.Vec3{1.2, 64.0, 0.5}, cell) {
		t.Error("a body clipping the cell from 0.7 off-centre was called clear")
	}
	// Fully beside the cell: the body spans [1.5, 2.1], clear of [0, 1].
	if scaffold.BodyBlocksCell(mgl32.Vec3{1.8, 64.0, 0.5}, cell) {
		t.Error("a body fully beside the cell was called blocking")
	}
	// The jump apex: risen past the top of the cell, clear even in-column.
	if scaffold.BodyBlocksCell(mgl32.Vec3{0.5, 65.0, 0.5}, cell) {
		t.Error("a body risen past the top of the cell was called blocking")
	}
	// Below the cell: a body standing on the floor under it.
	if scaffold.BodyBlocksCell(mgl32.Vec3{0.5, 61.5, 0.5}, cell) {
		t.Error("a body below the cell was called blocking")
	}
}
