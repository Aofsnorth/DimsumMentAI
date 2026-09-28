package combat

import "testing"

// Choosing what to hold was previously a static tier list with no situational
// input, so a bot holding a bow and a sword picked the sword against a creeper
// forty blocks away. These tests pin the decisions that fix that, over plain
// data — a weapon choice that can only be tested by starting a fight is a
// weapon choice that never gets tested.

func inv(pairs map[uint32]string) map[uint32]string { return pairs }

var basicLoadout = map[uint32]string{
	0: "minecraft:diamond_sword",
	1: "minecraft:iron_sword",
	2: "minecraft:bow",
	3: "minecraft:arrow",
	4: "minecraft:shield",
}

// TestSwordIsTheDefault is the baseline. One target, close, no water: the sword
// is the right answer and everything else is a special case layered on top.
func TestSwordIsTheDefault(t *testing.T) {
	t.Parallel()

	choice := ChooseWeapon(inv(basicLoadout), Situation{
		TargetDistance: 2.5, MeleeHostiles: 1, Health: 20, MaxHealth: 20,
	})
	if choice.Kind != WeaponSword {
		t.Errorf("kind = %v, want WeaponSword", choice.Kind)
	}
	if choice.Name != "minecraft:diamond_sword" {
		t.Errorf("picked %q, want the best sword carried rather than the first", choice.Name)
	}
	if choice.Reason == "" {
		t.Error("a choice with no reason recorded cannot be explained in a log")
	}
}

// TestRangePullsOutABow is the case the old tier list could not express.
func TestRangePullsOutABow(t *testing.T) {
	t.Parallel()

	choice := ChooseWeapon(inv(basicLoadout), Situation{
		TargetDistance: 20, MeleeHostiles: 0, NearbyHostiles: 1,
		HasArrows: true, Health: 20, MaxHealth: 20,
	})
	if choice.Kind != WeaponBow {
		t.Errorf("kind = %v at 20 blocks, want WeaponBow", choice.Kind)
	}
}

// TestNoArrowsMeansNoBow is the companion. A bow with nothing to shoot is a
// stick, and switching to it leaves the bot holding a stick twenty blocks from a
// creeper.
func TestNoArrowsMeansNoBow(t *testing.T) {
	t.Parallel()

	choice := ChooseWeapon(inv(basicLoadout), Situation{
		TargetDistance: 20, MeleeHostiles: 0, NearbyHostiles: 1,
		HasArrows: false, Health: 20, MaxHealth: 20,
	})
	if choice.Kind != WeaponSword {
		t.Errorf("kind = %v with an empty quiver, want WeaponSword", choice.Kind)
	}
}

// TestCrowdsCallForAnAxe is the other situational case. An axe swing sweeps a
// wide arc, so it lands on all of them; a sword takes them one at a time, and
// the first one killed is usually the one that was hitting the bot.
func TestCrowdsCallForAnAxe(t *testing.T) {
	t.Parallel()

	loadout := map[uint32]string{
		0: "minecraft:diamond_sword",
		1: "minecraft:iron_axe",
		2: "minecraft:shield",
	}

	choice := ChooseWeapon(inv(loadout), Situation{
		TargetDistance: 2, MeleeHostiles: 3, NearbyHostiles: 3, Health: 20, MaxHealth: 20,
	})
	if choice.Kind != WeaponAxe {
		t.Errorf("kind = %v against three hostiles, want WeaponAxe", choice.Kind)
	}

	// One hostile is still a sword fight, even with the axe in the bag.
	choice = ChooseWeapon(inv(loadout), Situation{
		TargetDistance: 2, MeleeHostiles: 1, NearbyHostiles: 1, Health: 20, MaxHealth: 20,
	})
	if choice.Kind != WeaponSword {
		t.Errorf("kind = %v against one hostile, want WeaponSword", choice.Kind)
	}
}

// TestWaterCallsForATrident pins the override that outranks distance. Underwater
// a bow cannot be drawn and a melee swing may not connect, so holding anything
// else is holding the wrong tool.
func TestWaterCallsForATrident(t *testing.T) {
	t.Parallel()

	loadout := map[uint32]string{
		0: "minecraft:diamond_sword",
		1: "minecraft:bow",
		2: "minecraft:trident",
		3: "minecraft:arrow",
	}

	choice := ChooseWeapon(inv(loadout), Situation{
		TargetDistance: 12, MeleeHostiles: 0, HasArrows: true,
		Underwater: true, Health: 20, MaxHealth: 20,
	})
	if choice.Kind != WeaponTrident {
		t.Errorf("kind = %v underwater, want WeaponTrident", choice.Kind)
	}
}

// TestAMissingChoiceFallsBackRatherThanGivingUp. A bot that asked for a bow
// and had none should use the sword it was ignoring, not stand there.
func TestAMissingChoiceFallsBackRatherThanGivingUp(t *testing.T) {
	t.Parallel()

	loadout := map[uint32]string{0: "minecraft:iron_sword", 1: "minecraft:shield"}
	choice := ChooseWeapon(inv(loadout), Situation{
		TargetDistance: 25, MeleeHostiles: 0, HasArrows: true, Health: 20, MaxHealth: 20,
	})
	if choice.Kind != WeaponSword {
		t.Errorf("kind = %v with no bow carried, want the sword it was ignoring", choice.Kind)
	}
	if choice.Reason == "" {
		t.Error("the fallback did not record why it fell back")
	}
}

// TestFightingWithNothingIsARealAnswer. Fists are a legitimate last resort, and
// reporting it honestly is what lets the caller run instead of standing still.
func TestFightingWithNothingIsARealAnswer(t *testing.T) {
	t.Parallel()

	choice := ChooseWeapon(inv(map[uint32]string{0: "minecraft:dirt", 1: "minecraft:bread"}), Situation{
		TargetDistance: 2, MeleeHostiles: 1, Health: 20, MaxHealth: 20,
	})
	if choice.Kind != WeaponNone {
		t.Errorf("kind = %v with nothing to fight with, want WeaponNone", choice.Kind)
	}
}

// TestNamespacesAndSuffixesMatch keeps the lookups working on any server. A bot
// that only recognises "minecraft:sword" is a bot that recognises nothing on most
// of the servers it will actually be pointed at.
func TestNamespacesAndSuffixesMatch(t *testing.T) {
	t.Parallel()

	loadout := map[uint32]string{
		0: "custom:netherite_sword",
		1: "minecraft:bow",
		2: "minecraft:arrow",
	}
	choice := ChooseWeapon(inv(loadout), Situation{
		TargetDistance: 2, MeleeHostiles: 1, Health: 20, MaxHealth: 20,
	})
	if choice.Kind != WeaponSword || choice.Name != "custom:netherite_sword" {
		t.Errorf("got %v %q, want the namespaced netherite sword", choice.Kind, choice.Name)
	}
}

// TestChoiceIsDeterministic pins the tie-break. Two identical swords must
// resolve to the same slot every time, or the bot swaps between them forever and
// the log becomes unreadable.
func TestChoiceIsDeterministic(t *testing.T) {
	t.Parallel()

	loadout := map[uint32]string{
		3: "minecraft:iron_sword",
		1: "minecraft:iron_sword",
		2: "minecraft:iron_sword",
	}
	s := Situation{TargetDistance: 2, MeleeHostiles: 1, Health: 20, MaxHealth: 20}

	first := ChooseWeapon(inv(loadout), s)
	for i := 0; i < 20; i++ {
		again := ChooseWeapon(inv(loadout), s)
		if again.Slot != first.Slot {
			t.Fatalf("slot changed between calls: %d then %d", first.Slot, again.Slot)
		}
	}
	if first.Slot != 1 {
		t.Errorf("slot = %d, want the lowest matching slot (1)", first.Slot)
	}
}

// --- Shield ---

// TestShieldGoesUpOnlyWhenSomethingIsHitting is the defensive half. The shield
// exists in the codebase and was never raised by anything; these pin the three
// states it has to distinguish.
func TestShieldGoesUpOnlyWhenSomethingIsHitting(t *testing.T) {
	t.Parallel()

	loadout := inv(basicLoadout)

	raised := PlanShield(loadout, Situation{
		TargetDistance: 2, MeleeHostiles: 2, Health: 20, MaxHealth: 20,
	}, false)
	if raised != ShieldRaise {
		t.Errorf("plan = %v with two hostiles in melee, want ShieldRaise", raised)
	}

	// Nothing in reach: no reason to be blocking.
	quiet := PlanShield(loadout, Situation{
		TargetDistance: 20, MeleeHostiles: 0, NearbyHostiles: 1, Health: 20, MaxHealth: 20,
	}, false)
	if quiet != ShieldHold {
		t.Errorf("plan = %v with nothing in reach, want ShieldHold", quiet)
	}

	// Currently up and the threat is gone: put it away to free the hand.
	lowered := PlanShield(loadout, Situation{
		TargetDistance: 20, MeleeHostiles: 0, NearbyHostiles: 0, Health: 20, MaxHealth: 20,
	}, true)
	if lowered != ShieldLower {
		t.Errorf("plan = %v with the threat gone and the shield up, want ShieldLower", lowered)
	}
}

// TestDyingBotDropsTheShield is the judgement call. At a fifth of its health,
// blocking trades a certain hit for a certain death, so the shield comes down
// and the bot commits to the swing.
func TestDyingBotDropsTheShield(t *testing.T) {
	t.Parallel()

	plan := PlanShield(inv(basicLoadout), Situation{
		TargetDistance: 2, MeleeHostiles: 2, Health: 4, MaxHealth: 20,
	}, true)
	if plan != ShieldLower {
		t.Errorf("plan = %v at 4/20 health, want ShieldLower; blocking there is a slower death", plan)
	}

	// And it does not immediately go back up on the next tick.
	plan = PlanShield(inv(basicLoadout), Situation{
		TargetDistance: 2, MeleeHostiles: 2, Health: 4, MaxHealth: 20,
	}, false)
	if plan != ShieldHold {
		t.Errorf("plan = %v with the shield already down, want ShieldHold", plan)
	}
}

// TestNoShieldNoPlan keeps a bot without one from logging a decision it cannot
// make.
func TestNoShieldNoPlan(t *testing.T) {
	t.Parallel()

	loadout := inv(map[uint32]string{0: "minecraft:iron_sword"})

	if plan := PlanShield(loadout, Situation{MeleeHostiles: 3, Health: 20, MaxHealth: 20}, false); plan != ShieldHold {
		t.Errorf("plan = %v with no shield carried, want ShieldHold", plan)
	}
	// Holding a shield that is already up and has just left the bag: lower it,
	// or the bot blocks with a ghost.
	if plan := PlanShield(loadout, Situation{MeleeHostiles: 0, Health: 20, MaxHealth: 20}, true); plan != ShieldLower {
		t.Errorf("plan = %v holding a shield it no longer has, want ShieldLower", plan)
	}
}
