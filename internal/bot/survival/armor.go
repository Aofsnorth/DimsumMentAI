// Package survival provides automation for managing the bot's survival needs,
// including auto-eat, auto-armor, auto-tool, time tracking, bed sleeping, torch
// placement, death recovery, shelter, and potions.
// This file implements automatic armor equipping and related helpers.
package survival

import (
	"strings"
	"time"
)

// Armor slots: 0=helmet, 1=chestplate, 2=leggings, 3=boots
var armorSlots = []struct {
	name     string
	slotID   uint32
	keywords []string
}{
	{"helmet", 0, []string{"helmet", "cap", "crown"}},
	{"chestplate", 1, []string{"chestplate", "tunic"}},
	{"leggings", 2, []string{"leggings", "pants"}},
	{"boots", 3, []string{"boots", "shoes"}},
}

// Armor tier priority (best first)
var armorTierPriority = []string{
	"netherite", "diamond", "iron", "chainmail", "golden", "leather",
}

// wornArmorSlotBase is the global inventory slot the bot's worn helmet occupies.
// The armor container is mapped to slots 36-39 by the inventory handlers
// (network/player: ContainerArmor -> 36), so a worn piece is readable from the
// same snapshot the carried inventory comes from.
const wornArmorSlotBase uint32 = 36

// armorCooldown is the minimum gap between automatic armor swaps.
//
// A worn loadout only changes when loot or the server changes it, so checking
// far more often than this finds the same answer every time while emitting a
// steady trickle of equip packets. The tick runs twice a second.
const armorCooldown = 10 * time.Second

// ArmorTierScore ranks an item name by armor tier: higher is better, and an
// item that is not armor (or is armor of a tier the bot does not know, such as
// a turtle helmet) scores 0 so it is never treated as an upgrade.
func ArmorTierScore(name string) int {
	lower := strings.ToLower(strings.TrimPrefix(name, "minecraft:"))
	for i, tier := range armorTierPriority {
		if strings.Contains(lower, tier) {
			return len(armorTierPriority) - i
		}
	}
	return 0
}

// CompareArmorTier reports 1 when a is strictly better armor than b, -1 when b
// is strictly better, and 0 when they are equal or either is not scored armor.
// Equality is deliberately not an upgrade: swapping one iron helmet for another
// is a wasted packet and a visible twitch.
func CompareArmorTier(a, b string) int {
	sa, sb := ArmorTierScore(a), ArmorTierScore(b)
	switch {
	case sa == 0 || sb == 0 || sa == sb:
		return 0
	case sa > sb:
		return 1
	default:
		return -1
	}
}

// isArmorType reports whether an item name is a piece of the given armor slot.
func isArmorType(name string, keywords []string) bool {
	lower := strings.ToLower(strings.TrimPrefix(name, "minecraft:"))
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// WornTier reports the armor score of the piece currently worn in the given
// armor slot index (0-3, as listed in armorSlots). An empty slot scores 0.
func (m *Manager) WornTier(armorIndex int) int {
	item, ok := m.bot.GetInventorySlots()[wornArmorSlotBase+armorSlots[armorIndex].slotID]
	if !ok || item.Count <= 0 {
		return 0
	}
	name := m.bot.GetItemNames()[item.NetworkID]
	if !isArmorType(name, armorSlots[armorIndex].keywords) {
		return 0
	}
	return ArmorTierScore(name)
}

// bestArmorCandidate returns the inventory slot holding the best piece of the
// given armor type, along with its score. It only considers the carried
// inventory (slots 0-35), never the worn armor slots, so the piece already on
// the bot's head is not offered back to it.
func (m *Manager) bestArmorCandidate(armorIndex int) (uint32, int) {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()
	keywords := armorSlots[armorIndex].keywords

	// Slots are walked in index order rather than by ranging the map, for the
	// same reason the rest of the bot does: Go randomises map iteration, so two
	// identical helmets in two slots would otherwise swap the held one on
	// alternate ticks.
	bestSlot := uint32(0)
	bestScore := 0
	for slot := uint32(0); slot < wornArmorSlotBase; slot++ {
		item, ok := inv[slot]
		if !ok || item.Count <= 0 {
			continue
		}
		name := names[item.NetworkID]
		if !isArmorType(name, keywords) {
			continue
		}
		if score := ArmorTierScore(name); score > bestScore {
			bestScore, bestSlot = score, slot
		}
	}
	return bestSlot, bestScore
}

// ShouldUpgradeArmor is the decision half of auto-armor: is there a piece in
// the bag that beats what is worn, and is the bot free to swap it?
//
// It is separate from the action because the action sends packets and sleeps,
// while the judgement is the part worth testing.
func (m *Manager) ShouldUpgradeArmor() bool {
	m.mu.Lock()
	autoArmor := m.autoArmorOn && m.ArmorEnabled
	m.mu.Unlock()
	if !autoArmor {
		return false
	}
	// A swap changes what is in the bot's hand, so it waits for the brain to
	// finish what it is doing, the same way the night routine does.
	if m.bot.IsBusy() {
		return false
	}
	if !m.armorUpgradeAvailable() {
		return false
	}
	return m.takeCooldown(&m.reactive.lastArmorAttempt, armorCooldown)
}

// armorUpgradeAvailable reports whether any armor slot has a strictly better
// piece in the bag. Read without consuming the cooldown, so a bot that is
// holding its best armor does not burn the window.
func (m *Manager) armorUpgradeAvailable() bool {
	for i := range armorSlots {
		worn := m.WornTier(i)
		_, best := m.bestArmorCandidate(i)
		if best > worn {
			return true
		}
	}
	return false
}

// armorEquipper is the optional view of the bot that can wear a piece onto an
// armor slot through the server-confirmed path. It is an assertion rather
// than a method on the survival Bot interface for the same reason the
// durability tracker is: the test doubles implement that interface, and
// pushing an armor method through all of them buys nothing.
type armorEquipper interface {
	EquipArmorToSlot(slot uint32, armorSlot byte) error
}

// equipArmorPiece wears the piece in slot onto the given armor slot index
// (0-3, as listed in armorSlots) through the best available path.
//
// The server-confirmed armor swap is the only one that works: Bedrock
// resolves worn armor through the armor container, and the hotbar-select
// packet EquipItem sends only ever changes what the hand holds — which is
// why "pasang armor" showed nothing on the body. A bot without the armor
// path falls back to the hotbar select, which at least moves the piece into
// reach of the armor UI the way the old behaviour did.
func (m *Manager) equipArmorPiece(armorIndex int, slot uint32) error {
	if eq, ok := m.bot.(armorEquipper); ok {
		return eq.EquipArmorToSlot(slot, byte(armorSlots[armorIndex].slotID))
	}
	return m.bot.EquipItem(slot)
}

// TickAutoArmor is the acting half of auto-armor: it equips every piece that
// beats what is worn. Exported so the decision and the action can be driven
// independently from an external test.
func (m *Manager) TickAutoArmor() {
	if !m.ShouldUpgradeArmor() {
		return
	}

	for i := range armorSlots {
		slot, best := m.bestArmorCandidate(i)
		if best <= m.WornTier(i) {
			continue
		}
		m.logger.Info("Auto-armor: upgrading", "type", armorSlots[i].name, "slot", slot)
		if err := m.equipArmorPiece(i, slot); err != nil {
			m.logger.Warn("Auto-armor: failed to equip", "type", armorSlots[i].name, "slot", slot, "err", err)
			continue
		}
		// Server-confirmed swaps are not instant, and two equips in the same
		// tick race on the same inventory state.
		time.Sleep(200 * time.Millisecond)
	}
}

// EquipBestArmor wears the best carried piece onto every armor slot that an
// upgrade improves, and reports how many pieces it put on. "Pakai armor"
// showed nothing in game because this walked its own duplicate scan and then
// sent hotbar-select packets, which cannot wear armor at all; the decision
// now runs through the same worn-tier comparison the auto path uses, so the
// action and the automation can never disagree about what "best" means.
func (m *Manager) EquipBestArmor() int {
	equipped := 0
	for i := range armorSlots {
		worn := m.WornTier(i)
		slot, best := m.bestArmorCandidate(i)
		if best <= worn {
			continue
		}
		m.logger.Info("Armor: equipping", "type", armorSlots[i].name, "slot", slot)
		if err := m.equipArmorPiece(i, slot); err != nil {
			m.logger.Warn("Armor: failed to equip", "type", armorSlots[i].name, "slot", slot, "err", err)
			continue
		}
		equipped++
		// Server-confirmed swaps are not instant, and two equips in the same
		// tick race on the same inventory state.
		time.Sleep(200 * time.Millisecond)
	}

	if equipped > 0 {
		m.logger.Info("Armor complete", "pieces_equipped", equipped)
	}
	return equipped
}

// EnableAutoArmor enables/disables auto-armor
func (m *Manager) EnableAutoArmor(enabled bool) {
	m.mu.Lock()
	m.autoArmorOn = enabled
	m.ArmorEnabled = enabled
	m.mu.Unlock()
}
