package survival

import (
	"testing"
	"time"
)

// The armor container occupies global slots 36-39 in the bot's inventory map
// (see network/player containerSlotOffset: ContainerArmor -> 36). These tests
// use the same numbering so the fixtures describe a real worn loadout.
const (
	wornHelmetSlot   uint32 = 36
	wornChestSlot    uint32 = 37
	wornLegsSlot     uint32 = 38
	wornBootsSlot    uint32 = 39
)

// wear places an item into one of the worn armor slots.
func wear(b *fakeBot, slot uint32, networkID int32, name string) {
	stock(b, slot, networkID, name)
}

// TestAutoArmorWearsBetterHelmet is the behaviour itself: the bot picks up a
// diamond helmet while wearing leather, and the next tick puts it on.
func TestAutoArmorWearsBetterHelmet(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	wear(b, wornHelmetSlot, netIDLeatherHelmet, "minecraft:leather_helmet")
	stock(b, 3, netIDDiamondHelmet, "minecraft:diamond_helmet")

	m := newTestManager(b)
	m.tickAutoArmor()

	if len(b.equipped) == 0 {
		t.Fatal("tickAutoArmor equipped nothing despite a strictly better helmet in the inventory")
	}
	if b.equipped[0] != 3 {
		t.Errorf("equipped slot %d, want 3 (the diamond helmet)", b.equipped[0])
	}
}

// TestAutoArmorFillsEmptySlot is the post-loot case: nothing worn, something
// good carried.
func TestAutoArmorFillsEmptySlot(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	stock(b, 12, netIDIronChestplate, "minecraft:iron_chestplate")

	m := newTestManager(b)
	m.tickAutoArmor()

	if len(b.equipped) == 0 {
		t.Fatal("tickAutoArmor equipped nothing while carrying a chestplate and wearing none")
	}
	if b.equipped[0] != 12 {
		t.Errorf("equipped slot %d, want 12", b.equipped[0])
	}
}

// TestAutoArmorLeavesWornPiecesAlone is the anti-churn guard. Every tick the
// manager would otherwise re-equip the same piece it is already wearing, which
// on a live server is a stream of no-op equip packets.
func TestAutoArmorLeavesWornPiecesAlone(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	wear(b, wornChestSlot, netIDDiamondHelmet, "minecraft:diamond_helmet")

	m := newTestManager(b)
	m.tickAutoArmor()

	if len(b.equipped) != 0 {
		t.Errorf("tickAutoArmor equipped %v with nothing better carried, want no equips", b.equipped)
	}
}

// TestAutoArmorDoesNotDowngrade is the ordering rule: worse armor in the bag
// is not a reason to take off what is already worn.
func TestAutoArmorDoesNotDowngrade(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	wear(b, wornHelmetSlot, netIDDiamondHelmet, "minecraft:diamond_helmet")
	stock(b, 4, netIDLeatherHelmet, "minecraft:leather_helmet")

	m := newTestManager(b)
	m.tickAutoArmor()

	if len(b.equipped) != 0 {
		t.Errorf("tickAutoArmor equipped %v, downgrading a worn diamond helmet to leather", b.equipped)
	}
}

// TestAutoArmorRespectsDisabledFlag keeps the toggle honest.
func TestAutoArmorRespectsDisabledFlag(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	wear(b, wornHelmetSlot, netIDLeatherHelmet, "minecraft:leather_helmet")
	stock(b, 3, netIDDiamondHelmet, "minecraft:diamond_helmet")

	m := newTestManager(b)
	m.EnableAutoArmor(false)

	m.tickAutoArmor()

	if len(b.equipped) != 0 {
		t.Errorf("tickAutoArmor equipped %v while auto-armor was disabled", b.equipped)
	}
}

// TestAutoArmorIsCooldowned keeps the 500ms tick from spamming the server. A
// worn loadout only changes when loot changes it, so a short window is enough.
func TestAutoArmorIsCooldowned(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	wear(b, wornHelmetSlot, netIDLeatherHelmet, "minecraft:leather_helmet")
	stock(b, 3, netIDDiamondHelmet, "minecraft:diamond_helmet")

	m := newTestManager(b)

	if !m.shouldUpgradeArmor() {
		t.Fatal("shouldUpgradeArmor = false with a strictly better helmet carried")
	}
	m.mu.Lock()
	m.reactive.lastArmorAttempt = time.Now()
	m.mu.Unlock()

	if m.shouldUpgradeArmor() {
		t.Error("shouldUpgradeArmor fired again inside the armor cooldown")
	}
}

// TestAutoArmorDoesNotInterruptWork is the same rule the night routine follows:
// a self-care action that swaps the held item should wait for the brain.
func TestAutoArmorDoesNotInterruptWork(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	b.busy = true
	wear(b, wornHelmetSlot, netIDLeatherHelmet, "minecraft:leather_helmet")
	stock(b, 3, netIDDiamondHelmet, "minecraft:diamond_helmet")

	m := newTestManager(b)
	m.tickAutoArmor()

	if len(b.equipped) != 0 {
		t.Errorf("tickAutoArmor equipped %v while the bot was busy", b.equipped)
	}
}

// TestAutoArmorIsNotConfusedByArmorShapedNames is a guard against the keyword
// matcher: "iron_chestplate" must not be read as a helmet upgrade when the
// helmet slot is the one being judged.
func TestAutoArmorIsNotConfusedByArmorShapedNames(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	wear(b, wornHelmetSlot, netIDDiamondHelmet, "minecraft:diamond_helmet")
	stock(b, 6, netIDWoodenPickaxe, "minecraft:wooden_pickaxe")

	m := newTestManager(b)
	m.tickAutoArmor()

	for _, slot := range b.equipped {
		item, ok := b.inventory[slot]
		if !ok {
			continue
		}
		if name := b.names[item.NetworkID]; name == "minecraft:wooden_pickaxe" {
			t.Errorf("tickAutoArmor equipped a wooden pickaxe as armor (slot %d)", slot)
		}
	}
}

// TestArmorTierRanking pins the ordering the comparisons rely on.
func TestArmorTierRanking(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{"netherite beats diamond", "minecraft:netherite_helmet", "minecraft:diamond_helmet", 1},
		{"diamond beats iron", "minecraft:diamond_chestplate", "minecraft:iron_chestplate", 1},
		{"iron beats chainmail", "minecraft:iron_leggings", "minecraft:chainmail_leggings", 1},
		{"chainmail beats golden", "minecraft:chainmail_boots", "minecraft:golden_boots", 1},
		{"golden beats leather", "minecraft:golden_helmet", "minecraft:leather_helmet", 1},
		{"equal tiers are not an upgrade", "minecraft:iron_helmet", "minecraft:iron_helmet", 0},
		{"unknown is never an upgrade", "minecraft:iron_helmet", "minecraft:turtle_helmet", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := compareArmorTier(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("compareArmorTier(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestWornArmorTierReadsTheWornSlot checks the worn side of the comparison is
// read from the armor container slots rather than the carried inventory.
func TestWornArmorTierReadsTheWornSlot(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	wear(b, wornHelmetSlot, netIDLeatherHelmet, "minecraft:leather_helmet")
	// The same helmet type is also carried; the worn tier must be the worn one.
	stock(b, 2, netIDGoldenBoots, "minecraft:golden_boots")

	m := newTestManager(b)
	if got := m.wornTier(0); got != armorTierScore("minecraft:leather_helmet") {
		t.Errorf("wornTier(helmet) = %d, want the worn leather score %d", got, armorTierScore("minecraft:leather_helmet"))
	}
}
