package combat

import (
	"strings"
	"time"
)

// ===================== SHIELD BLOCKING =====================

// offhandEquipper is the optional view of the bot that can move an item into
// the off-hand container. It is an assertion rather than a method on the
// combat Bot interface for the same reason the health reader is: three test
// doubles implement that interface, and "can wear a shield" is not worth
// pushing through all of them. A bot without the ability simply reports that
// it could not raise the shield, which is the honest answer.
type offhandEquipper interface {
	EquipToOffhand(slot uint32) error
}

// sneakHolder is the optional view of the bot that can hold the combat
// crouch. On Bedrock a shield blocks only while the player is sneaking, so
// the raise/lower pair has to own the crouch flag on the input stream.
type sneakHolder interface {
	SetSneakHeld(held bool)
}

// RaiseShield puts the bot in the blocking state: shield into the off hand,
// crouch held down. Both halves matter — the server resolves blocking off
// the off-hand slot and the sneaking input, and the old behaviour (shield in
// the hotbar plus a fake use-item transaction) was a shield the server never
// counted, which is why blocking never happened in game.
func (cm *CombatManager) RaiseShield() bool {
	if !cm.HasShield() {
		cm.logger.Debug("RaiseShield: no shield found")
		return false
	}

	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()
	if inv[40].Count > 0 && strings.Contains(strings.ToLower(names[inv[40].NetworkID]), "shield") {
		// Already wearing it in the off hand; only the crouch is missing.
		if holder, ok := cm.bot.(sneakHolder); ok {
			holder.SetSneakHeld(true)
		}
		cm.mu.Lock()
		cm.shieldUp = true
		cm.mu.Unlock()
		cm.logger.Info("Shield raised (already in off hand, crouch held)")
		return true
	}

	shieldSlot := findBest(cm.slotNames(), WeaponShield)
	if shieldSlot < 0 {
		return false
	}

	equipper, canOffhand := cm.bot.(offhandEquipper)
	if !canOffhand {
		cm.logger.Warn("Cannot raise shield: the body cannot move items to the off hand")
		return false
	}
	if err := equipper.EquipToOffhand(uint32(shieldSlot)); err != nil {
		cm.logger.Warn("Failed to move shield to off hand", "slot", shieldSlot, "error", err)
		return false
	}

	if holder, ok := cm.bot.(sneakHolder); ok {
		holder.SetSneakHeld(true)
	}
	cm.mu.Lock()
	cm.shieldUp = true
	cm.mu.Unlock()
	cm.logger.Info("Shield raised", "offhand_slot", 40, "crouch", true)
	return true
}

// LowerShield drops the blocking state: crouch released. The shield stays in
// the off hand — moving it back costs a transaction and the next raise would
// only pay it again.
func (cm *CombatManager) LowerShield() {
	if holder, ok := cm.bot.(sneakHolder); ok {
		holder.SetSneakHeld(false)
	}
	cm.mu.Lock()
	wasUp := cm.shieldUp
	cm.shieldUp = false
	cm.mu.Unlock()
	if wasUp {
		cm.logger.Info("Shield lowered")
	}
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
func (cm *CombatManager) rangedSlot(kind WeaponKind) (slot uint32, found, arrows bool) {
	inventory := cm.slotNames()

	for _, name := range inventory {
		short := name
		if i := strings.LastIndexByte(short, ':'); i >= 0 {
			short = short[i+1:]
		}
		short = strings.ToLower(strings.TrimSpace(short))
		if short == "arrow" || strings.HasSuffix(short, "_arrow") {
			arrows = true
			break
		}
	}

	if best := findBest(inventory, kind); best >= 0 {
		return uint32(best), true, arrows
	}
	return 0, false, arrows
}

// shootOnce runs a whole draw-and-release for a weapon of a kind in one call.
//
// It is the shape the on-demand "shoot" action needs: nothing else is driving
// the tick, so the draw is waited out here rather than carried across ticks.
// The combat loop uses the tick-driven path instead.
func (cm *CombatManager) shootOnce(kind WeaponKind, targetID uint64) bool {
	slot, found, arrows := cm.rangedSlot(kind)
	if !found {
		cm.logger.Debug("shoot: no weapon of that kind in the inventory")
		return false
	}
	if kind == WeaponBow && !arrows {
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
