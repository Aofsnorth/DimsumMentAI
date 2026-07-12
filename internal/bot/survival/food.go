// Package survival provides automation for managing the bot's survival needs,
// including auto-eat, auto-armor, auto-tool, time tracking, bed sleeping, torch
// placement, death recovery, shelter, and potions.
// This file implements automatic food consumption and related helpers.
package survival

import (
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"bedrock-ai/internal/safecast"
)

// Food items ranked by hunger restoration
var foodPriority = []string{
	"golden_apple", "enchanted_golden_apple",
	"cooked_beef", "cooked_porkchop", "cooked_mutton", "cooked_chicken", "cooked_rabbit",
	"cooked_salmon", "cooked_cod",
	"bread", "baked_potato", "pumpkin_pie",
	"apple", "carrot", "sweet_berries", "glow_berries",
	"melon_slice", "beetroot", "dried_kelp",
	"cookie",
}

func (m *Manager) tickAutoEat() {
	if !m.autoEatOn {
		return
	}

	m.mu.Lock()
	hunger := m.hungerLevel
	lastEat := m.lastEatTime
	m.mu.Unlock()

	// Only eat when hunger is below threshold and cooldown passed
	if hunger > m.EatThreshold {
		return
	}
	if time.Since(lastEat) < 3*time.Second {
		return
	}

	m.logger.Info("Auto-eat triggered", "hunger", hunger, "threshold", m.EatThreshold)

	if m.EatBestFood() {
		m.mu.Lock()
		m.lastEatTime = time.Now()
		m.mu.Unlock()
	}
}

// EatBestFood finds and eats the best available food item
func (m *Manager) EatBestFood() bool {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	// Try food items in priority order
	for _, foodName := range foodPriority {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			name = strings.TrimPrefix(name, "minecraft:")
			if strings.Contains(name, foodName) {
				if m.eatFoodItem(slot, item) {
					m.logger.Info("Auto-eat: consumed food", "name", name, "slot", slot)
					return true
				}
			}
		}
	}

	m.logger.Debug("Auto-eat: no food found in inventory")
	return false
}

func (m *Manager) eatFoodItem(slot uint32, item protocol.ItemStack) bool {
	// Equip the food item
	if err := m.bot.EquipItem(slot); err != nil {
		return false
	}
	time.Sleep(150 * time.Millisecond)

	// Send use item packet (eating)
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{0, -1, 0},
			BlockFace:       255, // self-use
			HotBarSlot:      safecast.To[int32](slot),
			HeldItem:        protocol.ItemInstance{Stack: item},
			Position:        m.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0, 0, 0},
		},
	}
	if err := m.bot.WritePacket(tx); err != nil {
		return false
	}
	time.Sleep(1600 * time.Millisecond) // eating takes ~1.6 seconds
	return true
}

// EnableAutoEat enables/disables auto-eat
func (m *Manager) EnableAutoEat(enabled bool) {
	m.mu.Lock()
	m.autoEatOn = enabled
	m.mu.Unlock()
}
