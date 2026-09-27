package storage

import (
	"context"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type fakeBot struct {
	pos    mgl32.Vec3
	blocks map[protocol.BlockPos]blockInfo
	signs  map[protocol.BlockPos]string
	// navigated records the last NavigateToBlock target.
	navigated [3]int32
	stopped   bool
	looked    mgl32.Vec3
}

type blockInfo struct {
	name  string
	solid bool
}

func newFakeBot(x, y, z float32) *fakeBot {
	return &fakeBot{
		pos:    mgl32.Vec3{x, y, z},
		blocks: make(map[protocol.BlockPos]blockInfo),
		signs:  make(map[protocol.BlockPos]string),
	}
}

func (f *fakeBot) GetCoords() mgl32.Vec3 { return f.pos }
func (f *fakeBot) StopMovement()         { f.stopped = true }
func (f *fakeBot) LookAt(p mgl32.Vec3)   { f.looked = p }
func (f *fakeBot) NavigateToBlock(x, y, z int32, _ float32) bool {
	f.navigated = [3]int32{x, y, z}
	return true
}

func (f *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	info, ok := f.blocks[protocol.BlockPos{x, y, z}]
	if !ok {
		return "", false
	}
	return info.name, true
}

func (f *fakeBot) BlockLoaded(x, y, z int32) (bool, bool) {
	info, ok := f.blocks[protocol.BlockPos{x, y, z}]
	if !ok {
		return false, false
	}
	return info.solid, true
}

func (f *fakeBot) SignText(x, y, z int32) (string, bool) {
	text, ok := f.signs[protocol.BlockPos{x, y, z}]
	return text, ok
}

func (f *fakeBot) set(x, y, z int32, name string, solid bool) {
	f.blocks[protocol.BlockPos{x, y, z}] = blockInfo{name: name, solid: solid}
}

// TestChestBehindWallIsNotACandidate is the core "a player would not open
// that" rule: a chest one wall away is invisible and must not be discovered.
func TestChestBehindWallIsNotACandidate(t *testing.T) {
	t.Parallel()

	b := newFakeBot(0.5, 64, 0.5)
	b.set(0, 63, 0, "minecraft:stone", true) // floor
	// A wall at z=2, chest behind it at z=3.
	b.set(0, 64, 2, "minecraft:stone", true)
	b.set(0, 65, 2, "minecraft:stone", true)
	b.set(0, 64, 3, "minecraft:chest", true)

	svc := New(b)
	chests := svc.FindContainers()
	for _, chest := range chests {
		if chest.Pos.Z() == 3 {
			t.Fatalf("FindContainers returned a chest behind a wall at %v", chest.Pos)
		}
	}
}

// TestVisibleChestIsFoundAndSortedNearestFirst covers discovery and the
// deterministic ordering the search relies on.
func TestVisibleChestIsFoundAndSortedNearestFirst(t *testing.T) {
	t.Parallel()

	b := newFakeBot(0.5, 64, 0.5)
	b.set(0, 63, 0, "minecraft:stone", true)
	// Side by side, not in a line: a chest in front of another one is a real
	// occluder, and the search must not see through it.
	b.set(6, 64, 3, "minecraft:chest", true) // far
	b.set(3, 64, 3, "minecraft:chest", true) // near

	svc := New(b)
	chests := svc.FindContainers()
	if len(chests) != 2 {
		t.Fatalf("FindContainers found %d chests, want 2", len(chests))
	}
	if chests[0].Pos.X() != 3 {
		t.Errorf("nearest chest is x=%d, want x=3 first", chests[0].Pos.X())
	}
	for _, chest := range chests {
		if !chest.HasLineOf {
			t.Errorf("chest at %v is visible but not marked HasLineOf", chest.Pos)
		}
	}
}

// TestApproachStaysBesideTheChestNotInsideIt checks the stand-off logic picks a
// neighbouring open cell, which is what a player actually stands on.
func TestApproachStaysBesideTheChestNotInsideIt(t *testing.T) {
	t.Parallel()

	b := newFakeBot(0.5, 64, 0.5)
	chestPos := protocol.BlockPos{0, 64, 4}
	b.set(0, 63, 0, "minecraft:stone", true)
	b.set(0, 63, 4, "minecraft:stone", true) // floor under the chest
	b.set(0, 64, 4, "minecraft:chest", true)
	// A block one ABOVE the obvious stand cell. The sight line to the chest
	// passes at y=64 there, so this does not block the view, but it does make
	// the stand cell unusable — which is what forces the neighbour search to
	// step sideways instead of walking into the chest.
	b.set(0, 65, 3, "minecraft:stone", true)

	svc := New(b)
	chest, ok := svc.ChestAt(chestPos, "minecraft:chest")
	if !ok {
		t.Fatal("ChestAt reports a visible chest as not visible")
	}
	// The bot is out of reach, so Approach must have navigated. A short context
	// keeps the test fast; the point is which cell it chose, not the walk.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_ = svc.Approach(ctx, chest)

	if b.navigated == [3]int32{0, 64, 4} || b.navigated == [3]int32{0, 64, 3} {
		t.Errorf("Approach navigated into %v, which is a chest cell", b.navigated)
	}
}

// TestFindContainersSkipsUnloadedCells is important because the world cache is
// sparse in request mode: unknown cells must not be guessed as chests.
func TestFindContainersSkipsUnloadedCells(t *testing.T) {
	t.Parallel()

	b := newFakeBot(0.5, 64, 0.5)
	svc := New(b)
	if got := svc.FindContainers(); len(got) != 0 {
		t.Fatalf("FindContainers with no loaded blocks returned %d chests", len(got))
	}
}

// TestSignTextNeedsLoadedText guards the rule that a sign whose block-entity
// text never arrived is not offered as readable.
func TestSignTextNeedsLoadedText(t *testing.T) {
	t.Parallel()

	b := newFakeBot(0.5, 64, 0.5)
	b.set(0, 63, 0, "minecraft:stone", true)
	b.set(0, 64, 2, "minecraft:oak_wall_sign", true) // sign block, no text yet
	b.set(0, 65, 2, "minecraft:air", false)

	svc := New(b)
	if signs := svc.FindSigns(); len(signs) != 0 {
		t.Fatalf("FindSigns returned %d signs with no text loaded", len(signs))
	}

	b.signs[protocol.BlockPos{0, 64, 2}] = "BAHAN"
	signs := svc.FindSigns()
	if len(signs) != 1 {
		t.Fatalf("FindSigns found %d signs after text loaded, want 1", len(signs))
	}
	if signs[0].Text != "BAHAN" {
		t.Errorf("sign text = %q, want %q", signs[0].Text, "BAHAN")
	}
}

// TestLabelChestsAttachesNearbySign is the labelled-storage case: a sign near
// a chest becomes that chest's label, which is what lets the search open one
// chest instead of all of them.
func TestLabelChestsAttachesNearbySign(t *testing.T) {
	t.Parallel()

	b := newFakeBot(0.5, 64, 0.5)
	b.set(0, 63, 0, "minecraft:stone", true)
	b.set(0, 64, 3, "minecraft:chest", true)
	b.set(0, 65, 3, "minecraft:oak_wall_sign", true) // sign above the chest
	b.signs[protocol.BlockPos{0, 65, 3}] = "BAHAN"

	svc := New(b)
	chests := svc.FindContainers()
	svc.LabelChests(chests, svc.FindSigns())
	if len(chests) != 1 {
		t.Fatalf("expected 1 chest, got %d", len(chests))
	}
	if chests[0].Label != "BAHAN" {
		t.Errorf("chest label = %q, want BAHAN", chests[0].Label)
	}
}

// TestOrderByLabelHintNeverDropsChests is the safety rule: a label reorders
// the search but must never filter a chest away.
func TestOrderByLabelHintNeverDropsChests(t *testing.T) {
	t.Parallel()

	chests := []Chest{
		{Pos: protocol.BlockPos{0, 64, 1}, Label: "ALAT"},
		{Pos: protocol.BlockPos{0, 64, 2}, Label: "BAHAN"},
		{Pos: protocol.BlockPos{0, 64, 3}},
	}
	ordered := OrderByLabelHint(chests, "bahan")
	if len(ordered) != 3 {
		t.Fatalf("OrderByLabelHint dropped chests: got %d, want 3", len(ordered))
	}
	if ordered[0].Label != "BAHAN" {
		t.Errorf("first chest after hint = %q, want the BAHAN-labelled one", ordered[0].Label)
	}
}

// TestCleanSignTextStripsFormatting keeps colour codes out of label matching.
func TestCleanSignTextStripsFormatting(t *testing.T) {
	t.Parallel()

	raw := "§lBAHAN §r§7v.2"
	if got := CleanSignText(raw); got != "BAHAN v.2" {
		t.Errorf("CleanSignText = %q, want %q", got, "BAHAN v.2")
	}
}

// TestApproachFailsHonestlyWhenUnreachable makes sure a chest it cannot reach
// produces an error rather than a silent "opened".
func TestApproachFailsHonestlyWhenUnreachable(t *testing.T) {
	t.Parallel()

	b := newFakeBot(0.5, 64, 0.5)
	// A chest 40 blocks away, still "visible" because we only blocked a wall.
	svc := New(b)
	chest := Chest{Pos: protocol.BlockPos{40, 64, 0}, HasLineOf: true}
	// The navigate call won't move the bot in the fake, so Approach times out.
	// Shorten the wait by trusting the error path quickly: just assert error.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := svc.Approach(ctx, chest); err == nil {
		t.Error("Approach on a far chest returned nil error in a test where the bot cannot move")
	}
}
