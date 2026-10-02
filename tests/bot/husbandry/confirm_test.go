package husbandry_test

import (
	"testing"

	"bedrock-ai/internal/bot/husbandry"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func stack(networkID int32, count uint16) protocol.ItemStack {
	return protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: networkID}, Count: count}
}

// --- Taming -----------------------------------------------------------

// The acceptance criterion for taming is a collared animal, so a transition from
// uncollared to collared is the confirmation and nothing else is.
func TestACollaredWolfIsTameConfirmed(t *testing.T) {
	t.Parallel()

	before := husbandry.EntityMeta{EntityType: "minecraft:wolf"}
	after := husbandry.EntityMeta{EntityType: "minecraft:wolf", Collared: true, Tamed: true}

	if !husbandry.TameConfirmed(before, after) {
		t.Error("a wolf that went from wild to collared was not confirmed tame")
	}
}

func TestAnUncollaredWolfIsNeverTameConfirmed(t *testing.T) {
	t.Parallel()

	// This is the whole defect: the old routine returned true after five
	// attempts whether or not anything happened to the animal.
	before := husbandry.EntityMeta{EntityType: "minecraft:wolf"}
	after := husbandry.EntityMeta{EntityType: "minecraft:wolf"}

	if husbandry.TameConfirmed(before, after) {
		t.Error("a wolf that never got a collar was confirmed tame")
	}
}

func TestAnAlreadyTamedWolfIsNotANewTame(t *testing.T) {
	t.Parallel()

	// Nothing changed. Reporting a tame here means the bot claims credit for
	// work it did not do.
	before := husbandry.EntityMeta{EntityType: "minecraft:wolf", Collared: true}
	after := husbandry.EntityMeta{EntityType: "minecraft:wolf", Collared: true}

	if husbandry.TameConfirmed(before, after) {
		t.Error("an already collared wolf was reported as newly tamed")
	}
}

func TestAnOwnedCatIsTameConfirmed(t *testing.T) {
	t.Parallel()

	before := husbandry.EntityMeta{EntityType: "minecraft:cat"}
	after := husbandry.EntityMeta{EntityType: "minecraft:cat", OwnerKnown: true, Tamed: true}

	if !husbandry.TameConfirmed(before, after) {
		t.Error("a cat with an owner was not confirmed tame; not every tameable mob wears a collar")
	}
}

func TestAnUnrelatedMobIsNeverTameConfirmed(t *testing.T) {
	t.Parallel()

	before := husbandry.EntityMeta{EntityType: "minecraft:cow"}
	after := husbandry.EntityMeta{EntityType: "minecraft:cow", Tamed: true, Collared: true}

	if husbandry.TameConfirmed(before, after) {
		t.Error("a cow was reported as tamed; cows are not tameable")
	}
}

func TestAnUnknownEntityIsNeverTameConfirmed(t *testing.T) {
	t.Parallel()

	// The entity type never arrived. A blank type must not be treated as a
	// wildcard that everything passes.
	if husbandry.TameConfirmed(husbandry.EntityMeta{}, husbandry.EntityMeta{Collared: true}) {
		t.Error("an unidentified entity with a collar was reported as tamed")
	}
}

func TestIsTameableKnowsTheMobList(t *testing.T) {
	t.Parallel()

	for _, n := range []string{"wolf", "minecraft:cat", "horse", "minecraft:llama", "parrot", "fox"} {
		if !husbandry.IsTameable(n) {
			t.Errorf("%q is tameable but IsTameable says no", n)
		}
	}
	for _, n := range []string{"", "cow", "minecraft:pig", "sheep", "zombie", "chicken"} {
		if husbandry.IsTameable(n) {
			t.Errorf("%q was treated as tameable", n)
		}
	}
}

// --- The server's own taming verdict -----------------------------------

func TestATamingSucceededEventIsAnObservation(t *testing.T) {
	t.Parallel()

	events := []husbandry.ActorEvent{{Type: husbandry.ActorEventTamingSucceeded}}
	succeeded, failed := husbandry.TameObserved(events)

	if !succeeded || failed {
		t.Errorf("TameObserved = (%v, %v), want (true, false)", succeeded, failed)
	}
}

func TestAFailedTamingAttemptIsNotSuccess(t *testing.T) {
	t.Parallel()

	events := []husbandry.ActorEvent{{Type: husbandry.ActorEventTamingFailed}}
	succeeded, failed := husbandry.TameObserved(events)

	if succeeded {
		t.Error("a rejected tame attempt was read as success")
	}
	if !failed {
		t.Error("a rejected tame attempt was not recorded as a failure")
	}
}

func TestNoEventsIsNoObservation(t *testing.T) {
	t.Parallel()

	succeeded, failed := husbandry.TameObserved(nil)
	if succeeded || failed {
		t.Errorf("TameObserved(nil) = (%v, %v), want (false, false)", succeeded, failed)
	}
}

// --- Breeding ---------------------------------------------------------

func TestBreedingNeedsAnObservedSignal(t *testing.T) {
	t.Parallel()

	// Feeding two cows is not breeding. The old code reported success the
	// moment it had clicked twice.
	if husbandry.BreedingConfirmed(husbandry.BreedSignal{}) {
		t.Error("breeding was confirmed with nothing observed at all")
	}
}

func TestBreedingIsConfirmedByHearts(t *testing.T) {
	t.Parallel()

	if !husbandry.BreedingConfirmed(husbandry.BreedSignal{HeartsObserved: true}) {
		t.Error("love-mode hearts were not accepted as a breeding signal")
	}
}

func TestBreedingIsConfirmedByABaby(t *testing.T) {
	t.Parallel()

	if !husbandry.BreedingConfirmed(husbandry.BreedSignal{BabyAppeared: true}) {
		t.Error("a baby appearing was not accepted as a breeding signal")
	}
}

func TestHeartsAreReadFromActorEvents(t *testing.T) {
	t.Parallel()

	events := []husbandry.ActorEvent{
		{Type: husbandry.ActorEventEatGrass},
		{Type: husbandry.ActorEventLoveHearts},
	}
	if !husbandry.HeartsObserved(events) {
		t.Error("a love-hearts actor event was not read as a breeding signal")
	}
	if husbandry.HeartsObserved([]husbandry.ActorEvent{{Type: husbandry.ActorEventEatGrass}}) {
		t.Error("grazing was read as love hearts")
	}
}

// --- Milking and shearing ---------------------------------------------

func TestMilkNeedsAMilkBucketInTheInventory(t *testing.T) {
	t.Parallel()

	before := husbandry.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(1, 1)},
		map[int32]string{1: "minecraft:bucket"},
	)
	after := husbandry.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(1, 1)},
		map[int32]string{1: "minecraft:bucket"},
	)

	if husbandry.MilkConfirmed(before, after) {
		t.Error("milking was confirmed with the bucket still empty")
	}
}

func TestMilkIsConfirmedByTheBucketChanging(t *testing.T) {
	t.Parallel()

	before := husbandry.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(1, 1)},
		map[int32]string{1: "minecraft:bucket"},
	)
	after := husbandry.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(2, 1)},
		map[int32]string{2: "minecraft:milk_bucket"},
	)

	if !husbandry.MilkConfirmed(before, after) {
		t.Error("a milk bucket appeared and milking was not confirmed")
	}
}

func TestShearingNeedsWool(t *testing.T) {
	t.Parallel()

	before := husbandry.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(1, 1)},
		map[int32]string{1: "minecraft:shears"},
	)
	after := husbandry.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(1, 1)},
		map[int32]string{1: "minecraft:shears"},
	)

	if _, ok := husbandry.ShearConfirmed(before, after); ok {
		t.Error("shearing was confirmed with no wool in the inventory")
	}
}

func TestShearingIsConfirmedByWool(t *testing.T) {
	t.Parallel()

	before := husbandry.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(1, 1)},
		map[int32]string{1: "minecraft:shears"},
	)
	after := husbandry.NewInventory(
		map[uint32]protocol.ItemStack{0: stack(1, 1), 5: stack(2, 1)},
		map[int32]string{1: "minecraft:shears", 2: "minecraft:white_wool"},
	)

	got, ok := husbandry.ShearConfirmed(before, after)
	if !ok {
		t.Fatal("a ball of wool appeared and shearing was not confirmed")
	}
	if got != 1 {
		t.Errorf("ShearConfirmed gained %d wool, want 1", got)
	}
}
