package combat

import (
	"bedrock-ai/internal/safecast"
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
			HotBarSlot:      safecast.To[int32](shieldSlot),
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

// rangedSlot finds the slot holding the weapon of a kind, whether one is
// carried at all, and whether any ammunition is in the inventory. Both lookups
// were open-coded per weapon before; the draw sequence is shared, so they are
// too.
func (cm *CombatManager) rangedSlot(kind WeaponKind) (slot uint32, found, HasArrows bool) {
	inventory := cm.slotNames()

	for _, name := range inventory {
		short := name
		if i := strings.LastIndexByte(short, ':'); i >= 0 {
			short = short[i+1:]
		}
		short = strings.ToLower(strings.TrimSpace(short))
		if short == "arrow" || strings.HasSuffix(short, "_arrow") {
			HasArrows = true
			break
		}
	}

	if best := findBest(inventory, kind); best >= 0 {
		return uint32(best), true, HasArrows
	}
	return 0, false, HasArrows
}

// shootOnce runs a whole draw-and-release for a weapon of a kind in one call.
//
// It is the shape the on-demand "shoot" action needs: nothing else is driving
// the tick, so the draw is waited out here rather than carried across ticks.
// The combat loop uses the tick-driven path instead.
func (cm *CombatManager) shootOnce(kind WeaponKind, targetID uint64) bool {
	slot, found, HasArrows := cm.rangedSlot(kind)
	if !found {
		cm.logger.Debug("shoot: no weapon of that kind in the inventory")
		return false
	}
	if kind == WeaponBow && !HasArrows {
		cm.logger.Debug("shoot: no arrows, so the bow is just a stick")
		return false
	}

	target, ok := cm.currentTarget(targetID)
	if !ok {
		return false
	}
	if !cm.hasLineOfSight(target) {
		cm.logger.Debug("shoot: target not visible", "target", target.Name)
		return false
	}

	choice := WeaponChoice{Slot: slot, Kind: kind, Name: cm.slotNames()[slot]}
	cm.bot.LookAt(BowAimPoint(cm.bot.GetCoords(), target.Position))
	time.Sleep(200 * time.Millisecond)

	now := time.Now()
	cm.beginDraw(choice, now)

	hold := fullBowDraw
	if kind == WeaponCrossbow {
		hold = crossbowLoadTime
	}
	time.Sleep(hold)

	cm.fireShot(Shot{Kind: kind, Slot: slot}, time.Now(), target)
	return true
}

// BowAttack shoots an arrow at the given target: draw, hold, release.
func (cm *CombatManager) BowAttack(targetID uint64) bool {
	return cm.shootOnce(WeaponBow, targetID)
}

// CrossbowAttack loads and fires the crossbow at the given target.
func (cm *CombatManager) CrossbowAttack(targetID uint64) bool {
	return cm.shootOnce(WeaponCrossbow, targetID)
}

// HasBow checks if a bow is available
func (cm *CombatManager) HasBow() bool {
	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()

	hasBow := false
	HasArrows := false
	for _, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "bow") && !strings.Contains(name, "crossbow") {
			hasBow = true
		}
		if strings.Contains(name, "arrow") {
			HasArrows = true
		}
	}
	return hasBow && HasArrows
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
