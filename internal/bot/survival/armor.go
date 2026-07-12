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

func (m *Manager) tickAutoArmor() {
	if !m.autoArmorOn || !m.ArmorEnabled {
		return
	}
	// Auto-armor is event-driven (called explicitly), not ticked every frame
	// to avoid constant re-equipping. We just check periodically.
}

// EquipBestArmor equips the best available armor pieces
func (m *Manager) EquipBestArmor() int {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()
	equipped := 0

	for _, armorSlot := range armorSlots {
		bestSlot := uint32(0)
		bestTier := -1
		found := false

		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			name = strings.TrimPrefix(name, "minecraft:")

			// Check if this item matches the armor type
			isArmorType := false
			for _, kw := range armorSlot.keywords {
				if strings.Contains(name, kw) {
					isArmorType = true
					break
				}
			}
			if !isArmorType {
				continue
			}

			// Determine tier
			for tierIdx, tierName := range armorTierPriority {
				if strings.Contains(name, tierName) {
					tierScore := len(armorTierPriority) - tierIdx
					if tierScore > bestTier {
						bestTier = tierScore
						bestSlot = slot
						found = true
					}
					break
				}
			}
		}

		if found {
			// Equip via armor swap packet (slot 36-39 are armor slots in Bedrock)
			m.logger.Info("Auto-armor: equipping", "type", armorSlot.name, "slot", bestSlot)
			_ = m.bot.EquipItem(bestSlot)
			equipped++
			time.Sleep(200 * time.Millisecond)
		}
	}

	if equipped > 0 {
		m.logger.Info("Auto-armor complete", "pieces_equipped", equipped)
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
