package combat

import (
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"
	"context"
	"math"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (cm *CombatManager) Tick(ctx context.Context) {
	cm.mu.Lock()
	if !cm.inCombat {
		cm.mu.Unlock()
		return
	}
	targetID := cm.targetID
	friendly := cm.friendlyMode
	cm.mu.Unlock()

	target, ok := cm.currentTarget(targetID)
	if !ok {
		cm.mu.Lock()
		if cm.inCombat && cm.targetID == targetID {
			cm.recentKills[targetID] = time.Now()
			cm.inCombat = false
			cm.targetID = 0
			cm.pvpTarget = ""
			cm.logger.Info("Target eliminated or despawned")
			cm.mu.Unlock()
			cm.bot.StopMovement()

			go func() {
				time.Sleep(1 * time.Second)
				cm.bot.InjectAIEvent("[SYSTEM: Target eliminated. Drop collected or none found. Tell the player naturally.]")
			}()
			return
		}
		cm.mu.Unlock()
		return
	}

	if friendly && target.Type == "player" && target.Health <= 4 {
		cm.logger.Info("Friendly PVP: stopping attack as target is low health", "target", target.Name)
		cm.Disengage()
		cm.bot.ReportActionStatus("", event.ActionStatus{Action: "combat", Success: true, Error: "friendly PVP stop"})
		return
	}

	botPos := cm.bot.GetCoords()
	dist := cm.distance(botPos, target.Position)

	if dist > 32 {
		cm.logger.Info("Target too far, disengaging", "distance", dist)
		cm.Disengage()
		return
	}

	// What to hold, decided before anything else in the tick. Doing it here
	// rather than on engage means the bot reconsiders as the fight changes: a
	// creeper that backs off gets met with a bow instead of the sword it was
	// holding two seconds ago.
	situation := cm.situation(dist)
	if choice := ChooseWeapon(cm.slotNames(), situation); choice.Kind == WeaponNone {
		cm.logger.Warn("Combat with nothing to fight with")
	} else {
		cm.holdWeapon(choice)
	}
	cm.applyShield(situation)

	targetCenter := target.Position.Add(mgl32.Vec3{0, 1.2, 0})
	cm.bot.LookAt(targetCenter)

	if dist > 3.0 {
		cm.bot.NavigateTo(target.Position)
	} else {
		cm.bot.StopMovement()
	}

	if dist <= 3.5 && time.Since(cm.lastAttack) >= 500*time.Millisecond {
		if !cm.hasLineOfSight(target) {
			cm.logger.Info("Target out of sight; not attacking", "name", target.Name)
			return
		}
		cm.attack(targetID, target.Position)
		cm.lastAttack = time.Now()
		// Every swing costs the held tool a point of life. Counting it here is
		// the only place durability is actually consumed, which is what makes
		// the count mean anything.
		cm.durability.Record(cm.bot.GetHeldItemSlot())
	}
}

// slotNames is the inventory as a plain slot-to-name map, which is the shape
// the pure decision functions take.
func (cm *CombatManager) slotNames() map[uint32]string {
	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()
	out := make(map[uint32]string, len(inv))
	for slot, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		if name, ok := names[stack.NetworkID]; ok {
			out[slot] = name
		}
	}
	return out
}

// situation reads the fight into the shape the decisions take.
func (cm *CombatManager) situation(dist float32) Situation {
	botPos := cm.bot.GetCoords()
	s := Situation{TargetDistance: dist, NearbyHostiles: 1, MeleeHostiles: 1}

	entities := cm.bot.GetEntities()
	for id, ent := range entities {
		if id == cm.targetID || ent.Health <= 0 || !isHostileEntity(ent) {
			continue
		}
		s.NearbyHostiles++
		if cm.distance(botPos, ent.Position) <= meleeRange {
			s.MeleeHostiles++
		}
	}

	inventory := cm.slotNames()
	s.HasArrows = hasArrows(inventory)
	s.Health, s.MaxHealth = cm.botHealth()
	return s
}

// hasArrows looks for ammunition rather than assuming a bow implies one.
//
// A bow with nothing to shoot is a stick, and a bot that switches to it because
// the target moved out of range and then stands there holding it is worse than
// one that never switched.
func hasArrows(inventory map[uint32]string) bool {
	for _, name := range inventory {
		short := name
		if i := strings.LastIndexByte(short, ':'); i >= 0 {
			short = short[i+1:]
		}
		short = strings.ToLower(strings.TrimSpace(short))
		if short == "arrow" || strings.HasSuffix(short, "_arrow") {
			return true
		}
	}
	return false
}

// healthReader is the optional view of the bot that carries its vitals.
//
// It is an optional assertion rather than a method on combat.Bot because the
// combat package has three test doubles to keep in step, and a health number
// that is only meaningful for the shield decision does not justify pushing that
// change through all of them. A bot that does not expose health simply never
// gets the desperate case, which is a safe default.
type healthReader interface {
	GetStatusDetails() (health, hunger int, coords string)
}

// botHealth reads the bot's health, defaulting to full when it is not available.
func (cm *CombatManager) botHealth() (health, maxHealth int) {
	reader, ok := cm.bot.(healthReader)
	if !ok {
		return 20, 20
	}
	health, _, _ = reader.GetStatusDetails()
	return health, 20
}

// holdWeapon equips the chosen item, unless the bot is already holding it.
//
// The reason is logged on a change and not on every tick: a combat tick runs
// many times a second, and a log line per tick is how a log becomes unreadable.
func (cm *CombatManager) holdWeapon(choice WeaponChoice) {
	if cm.bot.GetHeldItemSlot() == choice.Slot {
		return
	}
	if err := cm.bot.EquipItem(choice.Slot); err != nil {
		cm.logger.Warn("Failed to equip chosen weapon", "slot", choice.Slot, "error", err)
		return
	}
	cm.logger.Info("Combat: changed weapon",
		"weapon", choice.Name,
		"kind", choice.Kind.String(),
		"reason", choice.Reason,
	)
}

// applyShield raises or lowers the shield to match the plan.
//
// The combat manager already had a RaiseShield it never called, and a shieldUp
// flag it wrote and never read. Both are live now, which is the smallest change
// that makes the defensive half of combat exist at all.
func (cm *CombatManager) applyShield(s Situation) {
	cm.mu.Lock()
	up := cm.shieldUp
	cm.mu.Unlock()

	switch PlanShield(cm.slotNames(), s, up) {
	case ShieldRaise:
		if !up {
			cm.RaiseShield()
		}
	case ShieldLower:
		if up {
			cm.LowerShield()
		}
	}
}

// currentTarget resolves the engaged target from tracked actors, falling back
// to the retained PVP username so player targets stay attackable with fresh
// positions each tick.
func (cm *CombatManager) currentTarget(targetID uint64) (*entity.Info, bool) {
	entities := cm.bot.GetEntities()
	if target, ok := entities[targetID]; ok {
		if target.Health > 0 {
			return target, true
		}
		return nil, false
	}
	cm.mu.Lock()
	pvpTarget := cm.pvpTarget
	cm.mu.Unlock()
	if pvpTarget == "" {
		return nil, false
	}
	id, pos, ok := cm.bot.FindPlayer(pvpTarget)
	if !ok || id != targetID {
		return nil, false
	}
	return &entity.Info{
		ID:       targetID,
		Type:     "player",
		Name:     pvpTarget,
		Position: pos,
		Health:   20,
	}, true
}

// hasLineOfSight revalidates that every cell between the bot and the target
// is loaded and non-solid before swinging.
func (cm *CombatManager) hasLineOfSight(target *entity.Info) bool {
	origin := cm.bot.GetCoords()
	start := origin.Add(mgl32.Vec3{0, 1.62, 0})
	end := target.Position.Add(mgl32.Vec3{0, 1.2, 0})
	return entity.HasLineOfSight(cm.bot.GetLocalWorldModel(), cm.bot, start, end)
}

func (cm *CombatManager) attack(targetID uint64, targetPos mgl32.Vec3) {
	botRuntimeID := cm.bot.GetEntityRuntimeID()

	_ = cm.bot.WritePacket(&packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: botRuntimeID,
		SwingSource:     packet.AnimateSwingSourceAttack,
	})

	slot := cm.bot.GetHeldItemSlot()
	inv := cm.bot.GetInventorySlots()
	item, ok := inv[slot]

	var rawItem protocol.ItemInstance
	if ok && item.Count > 0 {
		rawItem = protocol.ItemInstance{
			Stack: item,
		}
	} else {
		rawItem = protocol.ItemInstance{}
	}

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: targetID,
			ActionType:            1,
			HotBarSlot:            safecast.To[int32](slot),
			HeldItem:              rawItem,
			Position:              cm.bot.GetCoords(),
			ClickedPosition:       mgl32.Vec3{0, 0, 0},
		},
	}

	if err := cm.bot.WritePacket(tx); err != nil {
		cm.logger.Error("Failed to write combat attack transaction", "error", err)
	}
}

func (cm *CombatManager) equipBestWeapon() {
	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()

	for _, weaponName := range weaponPriority {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name, ok := names[item.NetworkID]
			if !ok {
				continue
			}
			if containsIgnoreCase(name, weaponName) {
				if err := cm.bot.EquipItem(slot); err == nil {
					cm.logger.Info("Equipped best weapon for combat", "name", name, "slot", slot)
					return
				}
			}
		}
	}
	cm.logger.Info("No weapon found in inventory; fighting with fists")
}

func (cm *CombatManager) distance(a, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
