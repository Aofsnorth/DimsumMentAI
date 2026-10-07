// "Pakai armor" showed nothing on the body for the whole life of the feature:
// the wear path sent hotbar-select packets, which change what the hand holds
// and can never put a piece on the body. These tests pin the corrected wear
// path — pieces go onto the armor container through the server-confirmed
// swap, the worn-side comparison decides upgrades, and the count the action
// reports is the number of pieces that actually went on.
package survival_test

import (
	"testing"
)

func wornSlots(b *fakeBot) map[uint32]armorWear {
	out := make(map[uint32]armorWear, len(b.armorWorn))
	for _, w := range b.armorWorn {
		out[w.armorSlot] = w
	}
	return out
}

func TestEquipBestArmorWearsTheBestPieces(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	stock(b, 3, netIDLeatherHelmet, "minecraft:leather_helmet")
	stock(b, 5, netIDDiamondHelmet, "minecraft:diamond_helmet")
	stock(b, 8, netIDIronChestplate, "minecraft:iron_chestplate")

	m := newTestManager(b)
	count := m.EquipBestArmor()

	if count != 2 {
		t.Fatalf("EquipBestArmor = %d pieces, want 2 (helmet + chestplate)", count)
	}
	worn := wornSlots(b)
	helmet, ok := worn[0]
	if !ok {
		t.Fatal("no piece worn on the helmet slot")
	}
	if helmet.carried != 5 {
		t.Fatalf("helmet taken from slot %d, want 5 (the diamond one, not the leather)", helmet.carried)
	}
	if chest, ok := worn[1]; !ok || chest.carried != 8 {
		t.Fatalf("chestplate = %+v, want slot 8 on armor slot 1", chest)
	}
	// The fake mirrors the server swap, so the world afterwards reads right:
	// the diamond helmet is worn, the leather one is back in the bag.
	if name := b.names[b.inventory[36].ItemType.NetworkID]; name != "minecraft:diamond_helmet" {
		t.Fatalf("worn helmet = %q, want the diamond one", name)
	}
}

// TestEquipBestArmorSkipsEqualWorn is the no-churn rule the action shares
// with the automation: swapping an iron helmet for the same iron helmet is a
// wasted packet and a visible twitch.
func TestEquipBestArmorSkipsEqualWorn(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	wear(b, wornHelmetSlot, netIDIronHelmet, "minecraft:iron_helmet")
	stock(b, 4, netIDIronHelmet, "minecraft:iron_helmet")

	m := newTestManager(b)
	count := m.EquipBestArmor()

	if count != 0 {
		t.Fatalf("EquipBestArmor = %d, want 0 — the same tier is not an upgrade", count)
	}
	if len(b.armorWorn) != 0 {
		t.Fatalf("wear path called %v, want nothing", b.armorWorn)
	}
}

// TestEquipBestArmorUpgradesWornOnlyWhereBetter: mixed loadout — the helmet
// is upgraded, the already-better chestplate is left alone.
func TestEquipBestArmorUpgradesWornOnlyWhereBetter(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	wear(b, wornHelmetSlot, netIDLeatherHelmet, "minecraft:leather_helmet")
	wear(b, wornChestSlot, netIDDiamondChestplate, "minecraft:diamond_chestplate")
	stock(b, 3, netIDDiamondHelmet, "minecraft:diamond_helmet")
	stock(b, 6, netIDLeatherChestplate, "minecraft:leather_chestplate")

	m := newTestManager(b)
	count := m.EquipBestArmor()

	if count != 1 {
		t.Fatalf("EquipBestArmor = %d, want 1 (only the helmet upgrade)", count)
	}
	if w := wornSlots(b); len(w) != 1 || w[0].carried != 3 {
		t.Fatalf("worn pieces = %v, want exactly the diamond helmet from slot 3", b.armorWorn)
	}
}
