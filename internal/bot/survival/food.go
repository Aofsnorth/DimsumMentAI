// Package survival provides automation for managing the bot's survival needs,
// including auto-eat, auto-armor, auto-tool, time tracking, bed sleeping, torch
// placement, death recovery, shelter, and potions.
// This file implements automatic food consumption and related helpers.
package survival

import (
	"fmt"
	"sort"
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
	"golden_carrot", "mushroom_stew", "beetroot_soup",
}

// eyeHeight is where a use-item transaction is measured from.
const eyeHeight = 1.62

// eatCooldown is the minimum gap between automatic meals, counted from the
// END of the previous one. EatBestFood already blocks for the ~1.6 s eating
// animation, so this only needs to be a short breather: a hungry bot should
// chain meals like a player holding right-click.
const eatCooldown = 250 * time.Millisecond

// eatFailBackoff is how long to wait after an attempt that ate nothing (no
// food, failed equip), so the tick does not retry every 500 ms forever.
const eatFailBackoff = 3 * time.Second

// eatConfirmTimeout is how long to wait for the host to shrink the stack after
// a use-item before calling the meal a failure.
const eatConfirmTimeout = 3 * time.Second

// maxMealsPerTick caps a chain of back-to-back meals.
const maxMealsPerTick = 24

// ShouldAutoEat is the decision half of auto-eat: is the bot hungry enough, and
// has enough time passed since the last meal? Kept separate from the action so
// the judgement can be tested without the 1.6-second eating animation.
func (m *Manager) ShouldAutoEat() bool {
	if !m.autoEatOn {
		return false
	}

	m.mu.Lock()
	hunger := m.hungerLevel
	lastEat := m.lastEatTime
	m.mu.Unlock()

	if hunger > m.EatThreshold {
		return false
	}
	return time.Since(lastEat) >= eatCooldown
}

// tickAutoEat is the live entry point for auto-eat.
func (m *Manager) tickAutoEat() {
	if !m.ShouldAutoEat() {
		return
	}

	m.mu.Lock()
	hunger := m.hungerLevel
	m.mu.Unlock()

	m.logger.Debug("Auto-eat triggered", "hunger", hunger, "threshold", m.EatThreshold)

	// Chain meals back to back while still hungry, like a player holding
	// right-click, instead of waiting for the next 500 ms tick between them.
	// Stamp the attempt, and back off longer on failure.
	for i := 0; i < maxMealsPerTick; i++ {
		sent, consumed := m.eatBest()
		ate := sent && consumed
		stamp := time.Now()
		if !ate {
			stamp = stamp.Add(eatFailBackoff - eatCooldown)
		}
		m.mu.Lock()
		m.lastEatTime = stamp
		m.mu.Unlock()
		if !ate || !m.ShouldAutoEat() {
			return
		}
		time.Sleep(eatCooldown)
	}
}

// EatBestFood finds and eats the best available food item. It reports whether
// a meal was started.
func (m *Manager) EatBestFood() bool {
	sent, _ := m.eatBest()
	return sent
}

// eatBest is EatBestFood that also reports whether the host registered the meal.
func (m *Manager) eatBest() (sent, consumed bool) {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	// Deterministic slot order (Go randomises map iteration).
	slots := make([]uint32, 0, len(inv))
	for slot := range inv {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })

	// Try food items in priority order. Exact names only: a substring match
	// treated carrot_on_a_stick and beetroot_seeds as food.
	for _, foodName := range foodPriority {
		for _, slot := range slots {
			item := inv[slot]
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			name = strings.TrimPrefix(name, "minecraft:")
			if name == foodName {
				if sent, consumed := m.eatFoodItem(slot, item); sent {
					m.logger.Info("Auto-eat: consumed food", "name", name, "slot", slot, "registered", consumed)
					return true, consumed
				}
			}
		}
	}

	m.logger.Debug("Auto-eat: no food found in inventory")
	return false, false
}

// EatNamed eats the carried item the player asked for ("cookie", "cookies",
// "minecraft:bread"). An exact name wins over a substring match, so asking for
// "carrot" never picks carrot_on_a_stick while a carrot exists. It reports an
// error when nothing matches or the host did not register the meal, so the
// caller never claims a meal that did not happen.
func (m *Manager) EatNamed(want string) error {
	want = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(want)), "minecraft:")
	want = strings.ReplaceAll(want, " ", "_")
	singular := strings.TrimSuffix(want, "s")
	if want == "" {
		return fmt.Errorf("no food name given")
	}

	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()
	slots := make([]uint32, 0, len(inv))
	for slot := range inv {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })

	pick := func(match func(name string) bool) (uint32, bool) {
		for _, slot := range slots {
			if inv[slot].Count <= 0 {
				continue
			}
			name := strings.TrimPrefix(strings.ToLower(names[inv[slot].NetworkID]), "minecraft:")
			if match(name) {
				return slot, true
			}
		}
		return 0, false
	}
	slot, ok := pick(func(n string) bool { return n == want || n == singular })
	if !ok {
		slot, ok = pick(func(n string) bool { return n != "" && strings.Contains(n, singular) })
	}
	if !ok {
		return fmt.Errorf("no %s in inventory", want)
	}

	// Keep eating the same food until the hunger bar is full, the stack is
	// gone, or the host stops registering meals.
	ate := 0
	for i := 0; i < maxMealsPerTick; i++ {
		sent, consumed := m.eatFoodItem(slot, m.bot.GetInventorySlots()[slot])
		if !sent {
			if ate > 0 {
				return nil
			}
			return fmt.Errorf("could not use %s", want)
		}
		if !consumed {
			if ate > 0 {
				return nil // full, most likely
			}
			return fmt.Errorf("server did not register eating %s (not edible, or already full)", want)
		}
		ate++
		m.mu.Lock()
		full := m.hungerLevel >= 20
		m.mu.Unlock()
		if full || m.bot.GetInventorySlots()[slot].Count <= 0 {
			return nil
		}
	}
	return nil
}

// eatFoodItem reports whether the use-item went out and whether the stack
// shrank afterwards.
func (m *Manager) eatFoodItem(slot uint32, item protocol.ItemStack) (sent, consumed bool) {
	m.eatMu.Lock()
	defer m.eatMu.Unlock()
	// Skip the equip when the food is already in the selected hotbar slot.
	//
	// A meal out of the selected slot needs no MobEquipment, and the use-item
	// transaction below then names a slot the bot really is holding.
	if held := m.bot.GetHeldItemSlot(); held != slot {
		if err := m.bot.EquipItem(slot); err != nil {
			return false, false
		}
		time.Sleep(150 * time.Millisecond)
	}

	// Baseline AFTER the equip, from the live inventory: the caller's stack can
	// be stale, which made a failed meal look consumed.
	before := item
	if cur, ok := m.bot.GetInventorySlots()[m.bot.GetHeldItemSlot()]; ok {
		before = cur
	}

	if !m.useHeldItem() {
		return false, false
	}

	// The host confirms a meal by shrinking the stack, about 1.7 s after the
	// use (1.6 s of eating plus latency). A fixed 1.6 s sleep read just before
	// that update and reported a good meal as failed. Poll instead, and return
	// the moment the stack shrinks so the next meal starts immediately.
	deadline := time.Now().Add(eatConfirmTimeout)
	for {
		held := m.bot.GetHeldItemSlot()
		after, ok := m.bot.GetInventorySlots()[held]
		consumed = !ok || after.Count < before.Count || after.NetworkID != before.NetworkID
		if consumed || time.Now().After(deadline) {
			m.logger.Info("Auto-eat: meal result", "slot", slot, "held", held, "consumed", consumed)
			return true, consumed
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// useHeldItem right-clicks the air with whatever is in the selected hotbar
// slot (eating, drinking). The transaction must name the HOTBAR slot the item
// really sits in and carry that slot's current stack: EquipItem may have swapped
// the food up from the main inventory, so the slot and stack the caller found
// are stale.
//
// It goes out as a standalone InventoryTransaction. Geyser only reads a
// PlayerAuthInput item interaction when it is a block break (action 2) and
// logs "Unhandled item use transaction type" for anything else, so an inline
// ClickAir is silently ignored (meal result consumed=false). Its standalone
// ITEM_USE handler is where ClickAir (action 1) turns into a Java use-item.
func (m *Manager) useHeldItem() bool {
	held := m.bot.GetHeldItemSlot()
	if held > 8 {
		return false
	}
	stack, ok := m.bot.GetInventorySlots()[held]
	if !ok || stack.Count <= 0 {
		return false
	}
	return m.sendUse(held, protocol.ItemInstance{Stack: WireItemStack(stack)})
}

// WireItemStack reduces a stack to the fields the Geyser host tolerates in a
// client-sent item packet: type and count. Full server-side stacks carry NBT
// (display names, GeyserHash...) and a block runtime ID; echoing them back made
// the host disconnect with "Invalid packet received!" (live log, 2026-10-07),
// while the bare stack was accepted and acted on.
func WireItemStack(s protocol.ItemStack) protocol.ItemStack {
	return protocol.ItemStack{ItemType: s.ItemType, Count: s.Count}
}

// sendUse writes the standalone ClickAir transaction naming the given hotbar
// slot and held item.
func (m *Manager) sendUse(held uint32, item protocol.ItemInstance) bool {
	tx := protocol.UseItemTransactionData{
		ActionType:    protocol.UseItemActionClickAir,
		TriggerType:   protocol.TriggerTypePlayerInput,
		BlockPosition: protocol.BlockPos{},
		BlockFace:     0,
		HotBarSlot:    safecast.To[int32](held),
		HeldItem:      item,
		Position:      m.bot.GetCoords().Add(mgl32.Vec3{0, eyeHeight, 0}),
	}
	m.logger.Info("Auto-eat: use-item sent",
		"hotbar_slot", tx.HotBarSlot,
		"item_id", item.Stack.NetworkID,
		"count", item.Stack.Count)
	if err := m.bot.WritePacket(&packet.InventoryTransaction{TransactionData: &tx}); err != nil {
		m.logger.Warn("Auto-eat: use-item not sent", "err", err)
		return false
	}
	return true
}

// EnableAutoEat enables/disables auto-eat
func (m *Manager) EnableAutoEat(enabled bool) {
	m.mu.Lock()
	m.autoEatOn = enabled
	m.mu.Unlock()
}
