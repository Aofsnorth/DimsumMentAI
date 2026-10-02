package chest_test

import (
	"testing"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/inventory/chest"

	"github.com/go-gl/mathgl/mgl32"
)

// Giving an item reported success the moment the drop was sent.
//
// Everything before the return was a plan: the bot walked to a standoff, aimed at
// the recipient's head, computed a throw, and asked to drop. A Bedrock drop is a
// thrown object with an initial velocity — it clips walls, lands short, and is
// refused outright often enough that a player learns to stop checking. The old
// code returned true anyway and logged "Gave item successfully" while doing it.
//
// So delivery is observed rather than assumed. The server is the witness: a landed
// drop arrives as AddItemActor, which the bot already tracks. The rule that
// decides this is a pure function over two snapshots, so it is testable here
// without a connection.

// dropped is a snapshot entry as the network handler records it.
func dropped(id uint64, name string, pos mgl32.Vec3) *entity.Info {
	return &entity.Info{ID: id, Type: "minecraft:item", Name: name, Position: pos, Health: 1}
}

var recipient = mgl32.Vec3{10, 64, 10}

// TestALandedDropIsConfirmed is the positive case: something new, the right name,
// near the player.
func TestALandedDropIsConfirmed(t *testing.T) {
	t.Parallel()

	before := map[uint64]chest.ItemActor{}
	now := map[uint64]*entity.Info{7: dropped(7, "minecraft:diamond", mgl32.Vec3{11, 64, 10})}

	if !chest.DroppedItemAppeared(before, now, "minecraft:diamond", recipient) {
		t.Error("a diamond that landed beside the player was not confirmed as delivered")
	}
}

// TestNothingLandedIsNotConfirmed is the bug itself. The drop was sent; the
// server produced nothing. An item that never existed cannot be evidence that an
// item was given.
func TestNothingLandedIsNotConfirmed(t *testing.T) {
	t.Parallel()

	before := map[uint64]chest.ItemActor{}
	now := map[uint64]*entity.Info{}

	if chest.DroppedItemAppeared(before, now, "minecraft:diamond", recipient) {
		t.Error("a drop the server never produced was reported as delivered")
	}
}

// TestAnItemThatWasAlreadyThereIsNotAConfirmation. A previous drop of the same
// item is still lying on the ground; counting it would mean the bot is confident
// it delivered something on the strength of an earlier, unrelated toss.
func TestAnItemThatWasAlreadyThereIsNotAConfirmation(t *testing.T) {
	t.Parallel()

	// A drop of the same item is still lying on the ground from earlier. Only the
	// key matters to the rule — that is the whole point of the snapshot — so the
	// entry carries nothing.
	before := map[uint64]chest.ItemActor{3: {}}
	// Nothing new arrived.
	now := map[uint64]*entity.Info{3: dropped(3, "minecraft:diamond", mgl32.Vec3{11, 64, 10})}

	if chest.DroppedItemAppeared(before, now, "minecraft:diamond", recipient) {
		t.Error("a pre-existing item was counted as the delivery of this drop")
	}
}

// TestTheRightItemMatters. "Some item appeared" is not evidence of anything; a
// torch is not a diamond.
func TestTheRightItemMatters(t *testing.T) {
	t.Parallel()

	before := map[uint64]chest.ItemActor{}
	now := map[uint64]*entity.Info{7: dropped(7, "minecraft:torch", mgl32.Vec3{11, 64, 10})}

	if chest.DroppedItemAppeared(before, now, "minecraft:diamond", recipient) {
		t.Error("a torch confirmed a diamond delivery")
	}
}

// TestTooFarAwayIsNotDelivered. A throw carries velocity, so the item does not
// land on the player's feet — but past pickup range the player never gets it,
// and claiming otherwise is the same lie as a drop that clipped a wall.
func TestTooFarAwayIsNotDelivered(t *testing.T) {
	t.Parallel()

	before := map[uint64]chest.ItemActor{}
	// Nine blocks away: a clean miss.
	now := map[uint64]*entity.Info{7: dropped(7, "minecraft:diamond", mgl32.Vec3{19, 64, 10})}

	if chest.DroppedItemAppeared(before, now, "minecraft:diamond", recipient) {
		t.Error("a diamond nine blocks away confirmed a delivery; the player cannot pick that up")
	}
}

// TestANonItemEntityIsNotAConfirmation. A mob that happens to be nearby is not a
// delivery.
func TestANonItemEntityIsNotAConfirmation(t *testing.T) {
	t.Parallel()

	before := map[uint64]chest.ItemActor{}
	now := map[uint64]*entity.Info{
		7: {ID: 7, Type: "minecraft:cow", Name: "minecraft:cow", Position: mgl32.Vec3{11, 64, 10}, Health: 10},
	}

	if chest.DroppedItemAppeared(before, now, "minecraft:diamond", recipient) {
		t.Error("a cow confirmed a diamond delivery")
	}
}

// TestANilEntryDoesNotPanic. The actor map is written by a network goroutine and
// read here, so a nil in it is a real possibility and a panic in the middle of
// a drop would be a poor way to learn about it.
func TestANilEntryDoesNotPanic(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("a nil actor entry panicked: %v", r)
		}
	}()

	before := map[uint64]chest.ItemActor{}
	now := map[uint64]*entity.Info{7: nil}

	chest.DroppedItemAppeared(before, now, "minecraft:diamond", recipient)
}
