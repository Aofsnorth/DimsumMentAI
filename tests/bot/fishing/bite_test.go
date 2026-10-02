package fishing_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/fishing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

var origin = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func at(sec float64) time.Time { return origin.Add(time.Duration(sec * float64(time.Second))) }

// stack builds an inventory stack the way the bot sees it: a network ID and a
// count, with the name living in a separate lookup table.
func stack(networkID int32, count uint16) protocol.ItemStack {
	return protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: networkID}, Count: count}
}

func names(table map[int32]string) map[int32]string { return table }

// --- The bite predicate -------------------------------------------------
//
// This is the whole point of the package. The version that shipped before reeled
// the rod on a timer and then incremented a counter, so it reported fish that
// never existed. The rule it needs is simple: a bite is something the bobber DID.

func TestASinkingBiteIsABite(t *testing.T) {
	t.Parallel()

	// The bobber is floating four blocks out. The next sample is it jerking
	// down and a little closer to the player.
	before := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{10, 62.0, 10}, At: at(0)}
	after := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{9.6, 61.6, 9.6}, At: at(0.5)}

	if !fishing.ShouldReel(before, after, mgl32.Vec3{0, 62, 0}) {
		t.Error("a bobber that sank and closed distance was not treated as a bite")
	}
}

func TestAStationaryBobberIsNeverABite(t *testing.T) {
	t.Parallel()

	// A float sitting on still water. Nothing moves, so there is nothing to
	// reel on, and the old timer would have "caught" a fish right here.
	before := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{10, 62.0, 10}, At: at(0)}
	after := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{10, 62.0, 10}, At: at(3)}

	if fishing.ShouldReel(before, after, mgl32.Vec3{0, 62, 0}) {
		t.Error("a bobber that did not move was treated as a bite")
	}
}

func TestSwellIsNotABite(t *testing.T) {
	t.Parallel()

	// Riding the surface: it rises and falls by a centimetre or two and drifts
	// slightly, exactly like a float on moving water. Reeling on this is how a
	// bot yanks its line in before any fish is even interested.
	before := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{10, 62.00, 10}, At: at(0)}
	after := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{10.01, 62.02, 10}, At: at(1)}

	if fishing.SankFast(before, after) {
		t.Error("a bobber riding the swell was read as sinking fast")
	}
}

func TestRisingIsNotABite(t *testing.T) {
	t.Parallel()

	// A fish that swallows the hook and is yanked upward by the server's own
	// "fish is attached" motion is not the bite signal; the tease event is.
	before := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{10, 62.0, 10}, At: at(0)}
	after := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{9.4, 63.0, 9.4}, At: at(0.2)}

	if fishing.ShouldReel(before, after, mgl32.Vec3{0, 62, 0}) {
		t.Error("a bobber moving upward was treated as a bite")
	}
}

func TestMovingAwayFromThePlayerIsNotABite(t *testing.T) {
	t.Parallel()

	// Reeling because the bobber drifted off down-current is a false positive
	// on every windy lake.
	before := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{10, 62.0, 10}, At: at(0)}
	after := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{14, 61.5, 14}, At: at(0.3)}

	if fishing.Approached(before, after, mgl32.Vec3{0, 62, 0}) {
		t.Error("a bobber travelling away from the player was read as approaching")
	}
	if fishing.ShouldReel(before, after, mgl32.Vec3{0, 62, 0}) {
		t.Error("a bobber travelling away from the player was reeled on")
	}
}

func TestATeleportIsNotABite(t *testing.T) {
	t.Parallel()

	// A long gap between samples is a chunk resync, not a fish. Comparing
	// across it manufactures a huge apparent velocity.
	before := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{10, 62.0, 10}, At: at(0)}
	after := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{9, 61.0, 9}, At: at(30)}

	if fishing.ShouldReel(before, after, mgl32.Vec3{0, 62, 0}) {
		t.Error("a 30 second gap was compared as if it were one sample interval")
	}
}

func TestTwoDifferentEntitiesAreNeverCompared(t *testing.T) {
	t.Parallel()

	// A different actor reusing the same spot — comparing them manufactures a
	// bite out of an unrelated mob.
	before := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{10, 62.0, 10}, At: at(0)}
	after := fishing.BobberSample{ID: 8, Position: mgl32.Vec3{9, 61.0, 9}, At: at(0.2)}

	if fishing.ShouldReel(before, after, mgl32.Vec3{0, 62, 0}) {
		t.Error("samples from two different entities were compared as one bobber")
	}
}

func TestAnUnseenBobberIsNeverABite(t *testing.T) {
	t.Parallel()

	// The zero value means "not observed". Reading it as a sample at the origin
	// would fire a reel on the very first poll of every cast.
	var unseen fishing.BobberSample
	before := fishing.BobberSample{ID: 7, Position: mgl32.Vec3{10, 62.0, 10}, At: at(0)}

	if fishing.ShouldReel(before, unseen, mgl32.Vec3{0, 62, 0}) {
		t.Error("a sample with no entity ID was read as a bite")
	}
}

// --- The tease event ---------------------------------------------------

func TestBiteObservedNeedsTheFishhookTeaseEvent(t *testing.T) {
	t.Parallel()

	events := []fishing.ActorEvent{
		{RuntimeID: 7, Type: fishing.ActorEventFishhookBubble, At: at(1)},
		{RuntimeID: 7, Type: fishing.ActorEventFishhookTease, At: at(4)},
	}

	if !fishing.BiteObserved(events, at(0)) {
		t.Error("a fishhook tease after the cast was not read as a bite")
	}
}

func TestEventsBeforeTheCastDoNotCount(t *testing.T) {
	t.Parallel()

	// The last cast's tease is still sitting in the buffer. Reeling on it again
	// would empty a water bucket for nothing.
	events := []fishing.ActorEvent{
		{RuntimeID: 7, Type: fishing.ActorEventFishhookTease, At: at(1)},
	}

	if fishing.BiteObserved(events, at(5)) {
		t.Error("a tease from before this cast was counted as this cast's bite")
	}
}

func TestIsBobberTypeKnowsTheName(t *testing.T) {
	t.Parallel()

	for _, n := range []string{
		"minecraft:fishing_hook", "fishing_hook",
		"minecraft:fish_hook", "fish_hook",
		"minecraft:fishing_bobber", "fishingbobber",
		"minecraft:bobber", "bobber",
	} {
		if !fishing.IsBobberType(n) {
			t.Errorf("%q was not recognised as the bobber", n)
		}
	}
	for _, n := range []string{"", "minecraft:cow", "minecraft:item", "minecraft:player", "hook"} {
		if fishing.IsBobberType(n) {
			t.Errorf("%q was mistaken for the bobber", n)
		}
	}
}

// --- Catch confirmation -----------------------------------------------

func TestCaughtDeltaCountsRealLoot(t *testing.T) {
	t.Parallel()

	before := fishing.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(1, 1), 1: stack(2, 1)},
		names(map[int32]string{1: "minecraft:fishing_rod", 2: "minecraft:oak_log"}),
	)
	after := fishing.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(1, 1), 1: stack(2, 1), 2: stack(3, 1)},
		names(map[int32]string{1: "minecraft:fishing_rod", 2: "minecraft:oak_log", 3: "minecraft:cod"}),
	)

	if got := fishing.CaughtDelta(before, after); got != 1 {
		t.Errorf("CaughtDelta = %d, want 1: one cod appeared", got)
	}
}

func TestAnUnchangedInventoryIsNotACatch(t *testing.T) {
	t.Parallel()

	slots := map[uint32]protocol.ItemStack{0: stack(1, 1)}
	table := names(map[int32]string{1: "minecraft:fishing_rod"})

	if got := fishing.CaughtDelta(fishing.NewInventory(slots, table), fishing.NewInventory(slots, table)); got != 0 {
		t.Errorf("CaughtDelta = %d on an unchanged inventory, want 0", got)
	}
}

func TestCastingAndReelingAreNotCatches(t *testing.T) {
	t.Parallel()

	// A rod that merely moved slot must not be read as a fish. This is the
	// guard on the exact bug the old implementation had: counting something
	// that merely happened as a catch.
	before := fishing.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(1, 1)},
		names(map[int32]string{1: "minecraft:fishing_rod"}),
	)
	after := fishing.NewInventory(
		map[uint32]protocol.ItemStack{1: stack(1, 1)},
		names(map[int32]string{1: "minecraft:fishing_rod"}),
	)

	if got := fishing.CaughtDelta(before, after); got != 0 {
		t.Errorf("CaughtDelta = %d after the rod merely moved slot, want 0", got)
	}
}

func TestTheVanillaLootTableIsCatchable(t *testing.T) {
	t.Parallel()

	// Everything a fishing rod can actually come back with. A list that misses
	// an entry means real fish get thrown away for being unrecognised.
	loot := []string{
		"minecraft:cod", "minecraft:salmon", "minecraft:pufferfish", "minecraft:tropical_fish",
		"minecraft:cooked_cod", "minecraft:cooked_salmon",
		"minecraft:bowl", "minecraft:leather", "minecraft:leather_horse_armor",
		"minecraft:rotten_flesh", "minecraft:string", "minecraft:bone",
		"minecraft:ink_sac", "minecraft:nautilus_shell", "minecraft:tripwire_hook",
		"minecraft:bamboo", "minecraft:clay_ball", "minecraft:clay",
		"minecraft:seagrass", "minecraft:water_bottle", "minecraft:torchflower_seeds",
		"minecraft:sponge", "minecraft:scute", "minecraft:prismarine_shard",
		"minecraft:potion", "minecraft:wooden_hoe", "minecraft:name_tag",
		"minecraft:raw_fish", "minecraft:cooked_fish",
	}
	for _, name := range loot {
		if !fishing.IsCatchable(name) {
			t.Errorf("%q is on the fishing loot table but IsCatchable says no", name)
		}
	}
}

func TestTheRodItselfIsNotLoot(t *testing.T) {
	t.Parallel()

	// "hook" is a substring of "tripwire_hook" and "fishing_rod" is a substring
	// of nothing here, but a sloppy matcher reads the rod as a catch. That is
	// the fabricated fish.
	notLoot := []string{
		"minecraft:fishing_rod", "minecraft:stick", "minecraft:bucket",
		"minecraft:water_bucket", "minecraft:oak_log", "", "minecraft:hoe",
		"minecraft:apple",
	}
	for _, name := range notLoot {
		if fishing.IsCatchable(name) {
			t.Errorf("%q was classified as fishing loot", name)
		}
	}
}
