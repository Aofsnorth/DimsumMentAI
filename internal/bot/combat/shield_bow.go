package combat

import (
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// ===================== SHIELD BLOCKING =====================

// RaiseShield equips and raises a shield for blocking
func (cm *CombatManager) RaiseShield() bool {
	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()

	// Find shield in inventory (can be in off-hand slot 40 or hotbar)
	var shieldSlot uint32
	found := false

	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "shield") {
			shieldSlot = slot
			found = true
			break
		}
	}

	if !found {
		cm.logger.Debug("RaiseShield: no shield found")
		return false
	}

	// Equip shield (in Bedrock, shield is typically in off-hand)
	if err := cm.bot.EquipItem(shieldSlot); err != nil {
		return false
	}
	time.Sleep(100 * time.Millisecond)

	// Send use item packet to raise shield
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{0, -1, 0},
			BlockFace:       255, // self-use (blocking)
			HotBarSlot:      int32(shieldSlot),
			HeldItem:        protocol.ItemInstance{Stack: inv[shieldSlot]},
			Position:        cm.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0, 0, 0},
		},
	}
	_ = cm.bot.WritePacket(tx)

	cm.mu.Lock()
	cm.shieldUp = true
	cm.mu.Unlock()

	cm.logger.Info("Shield raised")
	return true
}

// LowerShield stops blocking
func (cm *CombatManager) LowerShield() {
	cm.mu.Lock()
	cm.shieldUp = false
	cm.mu.Unlock()

	// Release shield by stopping use (send stop break as a general stop action)
	_ = cm.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: cm.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionAbortBreak,
	})

	cm.logger.Debug("Shield lowered")
}

// HasShield checks if a shield is available in inventory
func (cm *CombatManager) HasShield() bool {
	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()

	for _, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "shield") {
			return true
		}
	}
	return false
}

// ===================== BOW / RANGED COMBAT =====================

// BowAttack shoots an arrow at the current combat target
func (cm *CombatManager) BowAttack(targetID uint64) bool {
	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()

	// Find bow
	var bowSlot uint32
	hasBow := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "bow") && !strings.Contains(name, "crossbow") {
			bowSlot = slot
			hasBow = true
			break
		}
	}

	if !hasBow {
		cm.logger.Debug("BowAttack: no bow found")
		return false
	}

	// Check for arrows
	hasArrows := false
	for _, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "arrow") {
			hasArrows = true
			break
		}
	}

	if !hasArrows {
		cm.logger.Debug("BowAttack: no arrows found")
		return false
	}

	// Equip bow
	if err := cm.bot.EquipItem(bowSlot); err != nil {
		return false
	}

	// Look at target
	entities := cm.bot.GetEntities()
	target, ok := entities[targetID]
	if !ok {
		return false
	}
	cm.bot.LookAt(target.Position.Add(mgl32.Vec3{0, 1.2, 0}))
	time.Sleep(200 * time.Millisecond)

	// Draw bow (start using item)
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{0, -1, 0},
			BlockFace:       255,
			HotBarSlot:      int32(bowSlot),
			HeldItem:        protocol.ItemInstance{Stack: inv[bowSlot]},
			Position:        cm.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0, 0, 0},
		},
	}
	_ = cm.bot.WritePacket(tx)

	// Hold for 1 second to charge
	time.Sleep(1000 * time.Millisecond)

	// Release arrow
	_ = cm.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: cm.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionAbortBreak,
	})

	cm.logger.Info("Arrow shot at target", "target", target.Name)
	return true
}

// CrossbowAttack shoots a loaded crossbow at target
func (cm *CombatManager) CrossbowAttack(targetID uint64) bool {
	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()

	var crossbowSlot uint32
	hasCrossbow := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "crossbow") {
			crossbowSlot = slot
			hasCrossbow = true
			break
		}
	}

	if !hasCrossbow {
		return false
	}

	if err := cm.bot.EquipItem(crossbowSlot); err != nil {
		return false
	}

	entities := cm.bot.GetEntities()
	target, ok := entities[targetID]
	if !ok {
		return false
	}
	cm.bot.LookAt(target.Position.Add(mgl32.Vec3{0, 1.2, 0}))
	time.Sleep(200 * time.Millisecond)

	// Fire crossbow
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{0, -1, 0},
			BlockFace:       255,
			HotBarSlot:      int32(crossbowSlot),
			HeldItem:        protocol.ItemInstance{Stack: inv[crossbowSlot]},
			Position:        cm.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0, 0, 0},
		},
	}
	_ = cm.bot.WritePacket(tx)

	cm.logger.Info("Crossbow shot at target", "target", target.Name)
	return true
}

// HasBow checks if a bow is available
func (cm *CombatManager) HasBow() bool {
	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()

	hasBow := false
	hasArrows := false
	for _, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "bow") && !strings.Contains(name, "crossbow") {
			hasBow = true
		}
		if strings.Contains(name, "arrow") {
			hasArrows = true
		}
	}
	return hasBow && hasArrows
}

// HasRangedWeapon checks for any ranged weapon (bow, crossbow, trident)
func (cm *CombatManager) HasRangedWeapon() bool {
	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()

	for _, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "trident") {
			return true
		}
		if strings.Contains(name, "crossbow") {
			return true
		}
	}
	return cm.HasBow()
}
