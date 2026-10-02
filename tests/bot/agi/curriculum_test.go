package agi_test

import (
	"testing"

	"bedrock-ai/internal/bot/agi"
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
func plainWorld() agi.Snapshot {
	return agi.Snapshot{
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
	if got := agi.Curriculum(plainWorld()); hasActivity(got, jev.ActivityFish) {
		t.Error("offered fishing with no water in sight")
	}
	withWater := plainWorld()
	withWater.Features = perception.Features{Water: true}
	if got := agi.Curriculum(withWater); !hasActivity(got, jev.ActivityFish) {
		t.Error("water is in sight but fishing was not offered")
	}

	// Harvesting needs ripe crops. Harvesting seedlings destroys the field.
	if got := agi.Curriculum(plainWorld()); hasActivity(got, jev.ActivityHarvest) {
		t.Error("offered harvest with no crops in sight")
	}
	withCrops := plainWorld()
	withCrops.Features = perception.Features{RipeCrops: 3}
	if got := agi.Curriculum(withCrops); !hasActivity(got, jev.ActivityHarvest) {
		t.Error("ripe crops are in sight but harvest was not offered")
	}

	// Tending needs animals actually present.
	if got := agi.Curriculum(plainWorld()); hasActivity(got, jev.ActivityTendAnimals) {
		t.Error("offered tend_animals with no animals nearby")
	}
	withAnimals := plainWorld()
	withAnimals.Features = perception.Features{Animals: 2}
	if got := agi.Curriculum(withAnimals); !hasActivity(got, jev.ActivityTendAnimals) {
		t.Error("animals are nearby but tend_animals was not offered")
	}

	// Crafting needs a recipe the bot can actually complete.
	if got := agi.Curriculum(plainWorld()); hasActivity(got, jev.ActivityCraft) {
		t.Error("offered craft with no ingredients")
	}
	withRecipe := plainWorld()
	withRecipe.Craftable = 2
	if got := agi.Curriculum(withRecipe); !hasActivity(got, jev.ActivityCraft) {
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

	got := agi.Curriculum(night)
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

	if got := agi.Curriculum(night); !hasActivity(got, jev.ActivityShelter) {
		t.Errorf("night with no bed did not offer shelter: %v", got)
	}
	if got := agi.Curriculum(night); hasActivity(got, jev.ActivitySleep) {
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
	world.Nearby = []agi.Person{{Name: "Artheny", HasLineOf: true, Distance: 3}}

	for _, activity := range agi.Curriculum(world) {
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

// --- Breath reflex ---

// TestDrowningOutranksEverythingElse is the claim that makes the reflex worth
// having. Health only starts falling once the air is nearly gone, so a reflex
// layer that ranks by health bars always finds out too late — the bot surfaces
// with a health bar already draining, or does not surface at all.
//
// Hunger and an approaching player are both real and both survivable, which is
// exactly why they must lose.
func TestDrowningOutranksEverythingElse(t *testing.T) {
	t.Parallel()

	thresholds := agi.Thresholds{LowHP: 8, LowHunger: 6, LowAirSeconds: 10}

	// Everything else is also true at once: starving, someone walking up, and
	// about to run out of air.
	snap := agi.Snapshot{
		HP:                20,
		Hunger:            1,
		Nearby:            []agi.Person{{Name: "Steve", Distance: 3}},
		Underwater:        true,
		SecondsUnderwater: 30,
	}

	if got := agi.DecideReflex(snap, thresholds); got.Kind != agi.ReflexSurface {
		t.Errorf("reflex = %v, want ReflexSurface; the bot is drowning with 30s under", got.Kind)
	}
}

// TestAirReflexWaitsUntilTheThreshold is the other half. Surfacing the instant
// the bot touches water would be worse than never surfacing: it would break the
// bot out of every shallow crossing, every fishing spot and every underwater
// structure it had a reason to be in.
func TestAirReflexWaitsUntilTheThreshold(t *testing.T) {
	t.Parallel()

	thresholds := agi.Thresholds{LowHP: 8, LowHunger: 6, LowAirSeconds: 10}

	fresh := agi.Snapshot{HP: 20, Hunger: 20, Underwater: true, SecondsUnderwater: 3}
	if got := agi.DecideReflex(fresh, thresholds); got.Kind == agi.ReflexSurface {
		t.Error("surfaced after 3 seconds; the bot would break out of every puddle it wades through")
	}

	spent := agi.Snapshot{HP: 20, Hunger: 20, Underwater: true, SecondsUnderwater: 10}
	if got := agi.DecideReflex(spent, thresholds); got.Kind != agi.ReflexSurface {
		t.Errorf("reflex = %v at exactly the threshold, want ReflexSurface", got.Kind)
	}
}

// TestDryLandNeverTriggersTheBreathReflex is the false-positive guard. The
// counter is a count of seconds, and a stale one would drown a bot standing in
// a field.
func TestDryLandNeverTriggersTheBreathReflex(t *testing.T) {
	t.Parallel()

	thresholds := agi.Thresholds{LowHP: 8, LowHunger: 6, LowAirSeconds: 10}

	dry := agi.Snapshot{HP: 20, Hunger: 20, Underwater: false, SecondsUnderwater: 0}
	if got := agi.DecideReflex(dry, thresholds); got.Kind == agi.ReflexSurface {
		t.Error("surfaced on dry land")
	}

	// A stale counter with Underwater false is the specific bug this guards: the
	// bot has to clear the clock when it breaks the surface, not just stop
	// reading it.
	stale := agi.Snapshot{HP: 20, Hunger: 20, Underwater: false, SecondsUnderwater: 999}
	if got := agi.DecideReflex(stale, thresholds); got.Kind == agi.ReflexSurface {
		t.Error("a stale submersion count sent a bot on dry land to the surface")
	}
}

// TestEveryReflexRemainsReachable is a regression guard on the new branch. The
// breath check runs first and returns early, so the reflexes after it are only
// ever reached when the bot is breathing. This walks each of them to prove the
// early return did not swallow one.
func TestEveryReflexRemainsReachable(t *testing.T) {
	t.Parallel()

	thresholds := agi.Thresholds{LowHP: 8, LowHunger: 6, LowAirSeconds: 10}

	cases := []struct {
		name string
		snap agi.Snapshot
		want agi.ReflexKind
	}{
		{"critical health", agi.Snapshot{HP: 2, Hunger: 20}, agi.ReflexFlee},
		{"hunger", agi.Snapshot{HP: 20, Hunger: 1}, agi.ReflexEat},
		{"a player appears", agi.Snapshot{HP: 20, Hunger: 20, Nearby: []agi.Person{{Name: "Steve", Distance: 3, HasLineOf: true}}}, agi.ReflexLook},
		{"nothing at all", agi.Snapshot{HP: 20, Hunger: 20}, agi.ReflexNone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := agi.DecideReflex(tc.snap, thresholds); got.Kind != tc.want {
				t.Errorf("reflex = %v, want %v", got.Kind, tc.want)
			}
		})
	}
}
