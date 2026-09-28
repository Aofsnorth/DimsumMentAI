package agi

import (
	"testing"

	"bedrock-ai/internal/bot/perception"
	"bedrock-ai/internal/jev"
)

func hasActivity(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}

// plainWorld is a snapshot with nothing special in it: daytime, alone, no
// resources, and therefore no business-specific activities on offer.
func plainWorld() Snapshot {
	return Snapshot{
		HP: 20, Hunger: 20, FreeSlots: 36,
		NearBlocks: "none",
	}
}

// TestSubsystemActivitiesNeedTheirPrecondition is the P2 rule: a menu built from
// wishful thinking makes a bot repeatedly pick an action that cannot work, and a
// player can plainly see it cannot work. Each of these is offered only when the
// world actually supports it.
func TestSubsystemActivitiesNeedTheirPrecondition(t *testing.T) {
	t.Parallel()

	// Fishing needs water. Casting at a tree is the canonical bot failure.
	if got := Curriculum(plainWorld()); hasActivity(got, jev.ActivityFish) {
		t.Error("offered fishing with no water in sight")
	}
	withWater := plainWorld()
	withWater.Features = perception.Features{Water: true}
	if got := Curriculum(withWater); !hasActivity(got, jev.ActivityFish) {
		t.Error("water is in sight but fishing was not offered")
	}

	// Harvesting needs ripe crops. Harvesting seedlings destroys the field.
	if got := Curriculum(plainWorld()); hasActivity(got, jev.ActivityHarvest) {
		t.Error("offered harvest with no crops in sight")
	}
	withCrops := plainWorld()
	withCrops.Features = perception.Features{RipeCrops: 3}
	if got := Curriculum(withCrops); !hasActivity(got, jev.ActivityHarvest) {
		t.Error("ripe crops are in sight but harvest was not offered")
	}

	// Tending needs animals actually present.
	if got := Curriculum(plainWorld()); hasActivity(got, jev.ActivityTendAnimals) {
		t.Error("offered tend_animals with no animals nearby")
	}
	withAnimals := plainWorld()
	withAnimals.Features = perception.Features{Animals: 2}
	if got := Curriculum(withAnimals); !hasActivity(got, jev.ActivityTendAnimals) {
		t.Error("animals are nearby but tend_animals was not offered")
	}

	// Crafting needs a recipe the bot can actually complete.
	if got := Curriculum(plainWorld()); hasActivity(got, jev.ActivityCraft) {
		t.Error("offered craft with no ingredients")
	}
	withRecipe := plainWorld()
	withRecipe.Craftable = 2
	if got := Curriculum(withRecipe); !hasActivity(got, jev.ActivityCraft) {
		t.Error("craftable recipes exist but craft was not offered")
	}
}

// TestFishingIsWithheldAtNightEvenWithWater is a small but real judgement: a
// player who spots water after dark is not going to go fishing in the dark
// immediately, and offering it makes the bot seem to ignore its own sense of
// danger. The night branch of the curriculum replaces the day menu entirely.
func TestFishingIsWithheldAtNightEvenWithWater(t *testing.T) {
	t.Parallel()

	night := plainWorld()
	night.IsNight = true
	night.HasBed = true
	night.Features = perception.Features{Water: true, RipeCrops: 5, Animals: 3}

	got := Curriculum(night)
	if hasActivity(got, jev.ActivityFish) {
		t.Error("offered fishing at midnight")
	}
	if hasActivity(got, jev.ActivityHarvest) {
		t.Error("offered harvesting at midnight")
	}
	if !hasActivity(got, jev.ActivitySleep) {
		t.Errorf("night with a bed did not offer sleep: %v", got)
	}
}

// TestNightWithoutABedOffersShelter keeps the night branch honest in the other
// direction.
func TestNightWithoutABedOffersShelter(t *testing.T) {
	t.Parallel()

	night := plainWorld()
	night.IsNight = true
	night.HasBed = false

	if got := Curriculum(night); !hasActivity(got, jev.ActivityShelter) {
		t.Errorf("night with no bed did not offer shelter: %v", got)
	}
	if got := Curriculum(night); hasActivity(got, jev.ActivitySleep) {
		t.Error("offered sleep with no bed to sleep in")
	}
}

// TestEveryOfferedActivityIsExecutable guards the mapping from menu to action.
// An activity Jev can pick that maps to nothing is worse than not offering it:
// the bot announces it is going to do something and then visibly does not, which
// is the most damaging kind of broken.
func TestEveryOfferedActivityIsExecutable(t *testing.T) {
	t.Parallel()

	world := plainWorld()
	world.NearBlocks = "oak_log, dirt, chest"
	world.Craftable = 3
	world.Features = perception.Features{Water: true, RipeCrops: 2, Animals: 1, Logs: 4}
	world.Nearby = []Person{{Name: "Artheny", HasLineOf: true, Distance: 3}}

	for _, activity := range Curriculum(world) {
		if !actionFor(activity) {
			t.Errorf("activity %q is offered but has no action behind it", activity)
		}
	}
}

// actionFor mirrors the switch in doActivity. It is duplicated deliberately: a
// table that shared the implementation could not catch a case added to one and
// not the other, which is exactly the bug this test is looking for.
func actionFor(activity string) bool {
	switch activity {
	case jev.ActivityRest, jev.ActivityWander, jev.ActivityExplore,
		jev.ActivityGather, jev.ActivityMine, jev.ActivityApproach,
		jev.ActivityChat, jev.ActivityShelter, jev.ActivitySleep,
		jev.ActivityGesture, jev.ActivityLook, jev.ActivityFish,
		jev.ActivityHarvest, jev.ActivityTendAnimals, jev.ActivityCraft:
		return true
	}
	return false
}
