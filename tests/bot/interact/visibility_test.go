package interact_test

import (
	"testing"

	"bedrock-ai/internal/bot/interact"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Block clicking used to be decided by distance alone, which made a lever on
// the far side of a wall exactly as reachable as the one in front of the bot.
// Servers validate interaction distance and not occlusion, so the click landed:
// a bot pressing doors through walls is the clearest tell that it is not
// playing like a player.

// wallAcrossEyes is one stone block sitting between the bot and anything beyond
// it, at the height the bot's eyes travel through.
func wallAcrossEyes(b *fakeBot, z int32) {
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 65, z})] = "minecraft:stone"
}

// TestNamedBlockBehindAWallIsNotReachable is the regression. The named path is
// the one commands arrive on, so it is the one that has to refuse.
func TestNamedBlockBehindAWallIsNotReachable(t *testing.T) {
	t.Parallel()

	// Precondition: with nothing in the way, the button is found. Without this
	// the test would pass for the wrong reason — a bot that cannot find buttons
	// at all also cannot find them through walls.
	clear := newFakeBot()
	clear.blocks[interact.BlockKey(protocol.BlockPos{0, 65, 3})] = "minecraft:stone_button"
	if _, err := newTestInteractor(clear).Resolve(interact.ParseRequest("button")); err != nil {
		t.Fatalf("a button in clear view was not resolved: %v", err)
	}

	blocked := newFakeBot()
	blocked.blocks[interact.BlockKey(protocol.BlockPos{0, 65, 3})] = "minecraft:stone_button"
	wallAcrossEyes(blocked, 1)

	if target, err := newTestInteractor(blocked).Resolve(interact.ParseRequest("button")); err == nil {
		t.Fatalf("resolved %v through a wall, want no target", target.Block)
	}
}

// TestNamedBlockFallsBackToOneItCanSee checks the refusal is not the whole story:
// when the far one is walled off and a near one is not, the bot clicks the near
// one. A bot that gave up entirely would be a worse liar than one that reached
// through a wall.
func TestNamedBlockFallsBackToOneItCanSee(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	wallAcrossEyes(b, 2)
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 65, 1})] = "minecraft:stone_button" // in the open
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 65, 3})] = "minecraft:stone_button" // behind the wall

	target, err := newTestInteractor(b).Resolve(interact.ParseRequest("button"))
	if err != nil {
		t.Fatalf("no button resolved at all: %v", err)
	}
	if target.Block != (protocol.BlockPos{0, 65, 1}) {
		t.Errorf("resolved %v, want the visible button at 0,65,1", target.Block)
	}
}

// TestSeeThroughBlocksDoNotOcclude keeps the filter usable. A bot that refuses
// to click because a torch or a row of crops is in the way is trading one tell
// for a worse one.
func TestSeeThroughBlocksDoNotOcclude(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		block string
		z     int32
	}{
		{"torch", "minecraft:torch", 1},
		{"rail", "minecraft:rail", 1},
		{"water", "minecraft:water", 1},
		{"tall grass", "minecraft:tall_grass", 1},
		{"lever on the wall", "minecraft:lever", 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newFakeBot()
			b.blocks[interact.BlockKey(protocol.BlockPos{0, 65, tc.z})] = tc.block
			b.blocks[interact.BlockKey(protocol.BlockPos{0, 65, 3})] = "minecraft:oak_door"

			if _, err := newTestInteractor(b).Resolve(interact.ParseRequest("door")); err != nil {
				t.Errorf("%s blocked a click the bot could plainly make: %v", tc.block, err)
			}
		})
	}
}

// TestTheTargetIsNeverItsOwnOccluder guards the off-by-one that would make every
// interaction fail: the block at the end of the ray is solid by definition.
func TestTheTargetIsNeverItsOwnOccluder(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 65, 3})] = "minecraft:chest"

	if _, err := newTestInteractor(b).Resolve(interact.ParseRequest("chest")); err != nil {
		t.Errorf("the target block occluded itself: %v", err)
	}
}

// TestUnknownCellsDoNotBlock is the other half of that guard. A cell the world
// model has no data for is a gap in the bot's knowledge, not a wall, and
// treating it as solid would leave the bot unable to click anything near a
// chunk it has not fully received.
func TestUnknownCellsDoNotBlock(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	// Only the door exists. Everything the ray crosses is unknown.
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 65, 3})] = "minecraft:stone_button"

	if _, err := newTestInteractor(b).Resolve(interact.ParseRequest("button")); err != nil {
		t.Errorf("unloaded cells blocked a legitimate click: %v", err)
	}
}

// TestOnlyVisibleKeepsClearSightAndDropsWalledOff pins the filter itself, so the
// behaviour is pinned where it is decided rather than only through Resolve.
func TestOnlyVisibleKeepsClearSightAndDropsWalledOff(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 65, 1})] = "minecraft:stone_button"
	b.blocks[interact.BlockKey(protocol.BlockPos{0, 65, 3})] = "minecraft:stone_button"
	behind := interact.Target{Kind: interact.KindBlock, Name: "minecraft:stone_button", Block: protocol.BlockPos{0, 65, 3}}
	visible := interact.Target{Kind: interact.KindBlock, Name: "minecraft:stone_button", Block: protocol.BlockPos{0, 65, 1}}

	wallAcrossEyes(b, 2)
	got := interact.OnlyVisible(b, b.pos, []interact.Target{behind, visible})

	if len(got) != 1 || got[0].Block != visible.Block {
		t.Errorf("onlyVisible kept %v, want just the button in the open", got)
	}
}
