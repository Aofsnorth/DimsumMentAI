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

	// The dragon is the one target the loop below must not be pointed at. It
	// spends most of the fight out of reach, and charging an unreachable target
	// is how a bot that is winning the argument on paper loses the fight. The
	// branch goes before the distance check, because a dragon forty blocks up
	// is not a target to give up on — the crystals are what decide that, and
	// they are a hundred blocks away from it.
	if isEnderDragonTarget(target) {
		cm.tickDragonFight(target)
		return
	}

	botPos := cm.bot.GetCoords()
	dist := cm.distance(botPos, target.Position)

	if dist > 32 {
		cm.logger.Info("Target too far, disengaging", "distance", dist)
		cm.Disengage()
		return
	}

	// Mob-specific movement comes before the weapon decision: a tactic that
	// ends the fight (the creeper flee) must win over everything else in the
	// tick.
	plan := MobMovePlan(normalizedMobName(target), HorizontalDistance(botPos, target.Position), cm.bot.Appetite())
	if plan.Flee {
		// Put something between the body and the blast before leaving, if there
		// is time and a block to put. This is the composed answer rather than
		// the reflex one: the reflex is "run", and running works, but a player
		// who sees a creeper at two blocks drops a block first and walks away
		// behind it.
		//
		// It is best-effort by construction. The placement goes through the
		// confirmed path, so a bot with nothing to place, or with no time
		// before the fuse, simply runs — which is the old behaviour, still
		// correct, and never worse than what came before.
		if plan.BlockUp {
			if slot, _, held := cm.bot.FindScaffoldItem(); held {
				if err := cm.bot.EquipItem(slot); err == nil {
					if placed := cm.bot.PlaceShield(context.Background(), target.Position); placed {
						cm.logger.Info("Tactic: dropped a block between the body and the threat",
							"mob", normalizedMobName(target), "distance", dist)
					}
				}
			} else {
				cm.logger.Debug("Tactic: no block to put up, fleeing anyway",
					"mob", normalizedMobName(target), "distance", dist)
			}
		}
		cm.logger.Info("Tactic: disengaging and fleeing", "mob", normalizedMobName(target), "distance", dist)
		cm.Disengage()
		cm.bot.NavigateTo(RetreatPoint(botPos, target.Position, plan.SafeDistance))
		return
	}

	// HoldGround is the reckless disposition's answer to a creeper: stay and
	// find out what happens. It is checked after the flee branch so a tier that
	// both flees and holds ground still does the thing that keeps it alive.
	if plan.HoldGround {
		cm.logger.Info("Tactic: holding ground", "mob", normalizedMobName(target),
			"distance", dist, "appetite", cm.bot.Appetite().String())
	}

	// What to hold, decided before anything else in the tick. Doing it here
	// rather than on engage means the bot reconsiders as the fight changes: a
	// creeper that backs off gets met with a bow instead of the sword it was
	// holding two seconds ago.
	situation := cm.situation(dist)
	choice := ChooseWeapon(cm.slotNames(), situation)
	if choice.Kind == WeaponNone {
		cm.logger.Warn("Combat with nothing to fight with")
	} else {
		cm.holdWeapon(choice)
	}
	cm.applyShield(situation)

	ranged := choice.Kind == WeaponBow || choice.Kind == WeaponCrossbow

	aimPoint := target.Position.Add(mgl32.Vec3{0, 1.2, 0})
	if ranged {
		// A dropped arrow needs the aim lifted by the distance, or the shot
		// lands in front of the target rather than in it.
		aimPoint = BowAimPoint(botPos, target.Position)
	} else if plan.Look == LookFeet {
		// Endermen take eye contact as a challenge, so aim at the feet while
		// still tracking the target.
		aimPoint = target.Position.Add(mgl32.Vec3{0, 0.2, 0})
	}
	cm.bot.LookAt(aimPoint)

	cm.moveByPlan(botPos, target.Position, dist, plan)

	if ranged {
		// A bow is a hold and a release, not a swing. It replaces the melee
		// path entirely while it is the chosen weapon.
		cm.shootRanged(choice, situation.HasArrows, target)
		return
	}

	cm.swingAt(target)
}

// swingAt is the melee half of a tick: the range check, the cooldown, the sight
// check, the hit, and the durability that has to be paid for it.
//
// These are one decision rather than five, and the dragon path needed the same
// five. A duplicated cooldown is the kind of thing that works right up until a
// second call site runs it at a different rate, and then the bot is swinging
// faster than the server will answer.
func (cm *CombatManager) swingAt(target *entity.Info) {
	if cm.distance(cm.bot.GetCoords(), target.Position) > 3.5 {
		return
	}
	if time.Since(cm.lastAttack) < 500*time.Millisecond {
		return
	}
	if !cm.hasLineOfSight(target) {
		cm.logger.Info("Target out of sight; not attacking", "name", target.Name)
		return
	}
	cm.attack(target.ID, target.Position)
	cm.lastAttack = time.Now()
	// Every swing costs the held tool a point of life. Counting it here is the
	// only place durability is actually consumed, which is what makes the count
	// mean anything.
	cm.durability.Record(cm.bot.GetHeldItemSlot())
}

// isEnderDragonTarget reports whether the engaged entity is the dragon.
//
// It reads the name and the type rather than the type alone, because the type
// is whatever the server put in the AddActor packet and a misread here would
// point the whole fight at the wrong thing.
func isEnderDragonTarget(target *entity.Info) bool {
	return IsEnderDragon(target.Name) || IsEnderDragon(target.Type)
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

// moveByPlan turns the mob tactic into movement: strafing inside a distance
// band, holding the line outside it, or plain closing on the target when no
// band applies.
func (cm *CombatManager) moveByPlan(botPos, targetPos mgl32.Vec3, dist float32, plan MovePlan) {
	if plan.Strafe && plan.BandMin > 0 {
		hd := HorizontalDistance(botPos, targetPos)
		switch {
		case hd < plan.BandMin:
			// Pushed inside the band: back away along the line.
			cm.bot.NavigateTo(RetreatPoint(botPos, targetPos, plan.BandMax))
			return
		case hd > plan.BandMax:
			cm.bot.NavigateTo(targetPos)
			return
		case within(hd, plan.BandMin, plan.BandMax):
			sign := float32(1)
			// Flip the strafe side every couple of seconds so the motion is
			// not one predictable circle.
			if int(time.Now().Unix()/2)%2 == 1 {
				sign = -1
			}
			cm.bot.NavigateTo(StrafePoint(botPos, targetPos, hd, sign))
			return
		}
	}
	if dist > 3.0 {
		cm.bot.NavigateTo(targetPos)
	} else {
		cm.bot.StopMovement()
	}
}

// shootRanged runs the draw/hold/release state machine for the bow or
// crossbow currently held. It is driven by the combat tick (~200ms), so the
// draw is held across ticks rather than blocked on.
func (cm *CombatManager) shootRanged(choice WeaponChoice, hasAmmo bool, target *entity.Info) {
	cm.mu.Lock()
	s := cm.Shot
	cm.mu.Unlock()
	now := time.Now()

	switch PlanShot(choice.Kind, hasAmmo, now, s) {
	case ShotDraw:
		cm.beginDraw(choice, now)
	case ShotFire:
		cm.fireShot(s, now, target)
	}
}

// beginDraw starts holding the weapon down. In Bedrock the draw begins with a
// UseItem transaction; the release comes later as a ReleaseItem transaction.
func (cm *CombatManager) beginDraw(choice WeaponChoice, now time.Time) {
	inv := cm.bot.GetInventorySlots()
	if err := cm.bot.EquipItem(choice.Slot); err != nil {
		cm.logger.Warn("Failed to equip ranged weapon for the draw", "slot", choice.Slot, "error", err)
		return
	}
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:    protocol.UseItemActionClickAir,
			TriggerType:   protocol.TriggerTypePlayerInput,
			BlockPosition: protocol.BlockPos{0, -1, 0},
			BlockFace:     255,
			HotBarSlot:    safecast.To[int32](choice.Slot),
			HeldItem:      protocol.ItemInstance{Stack: inv[choice.Slot]},
			Position:      cm.bot.GetCoords(),
		},
	}
	if err := cm.bot.WritePacket(tx); err != nil {
		cm.logger.Error("Failed to write bow draw transaction", "error", err)
		return
	}
	cm.mu.Lock()
	cm.Shot.recordDraw(choice.Kind, choice.Slot, now)
	cm.mu.Unlock()
	cm.logger.Info("Drawing ranged weapon", "kind", choice.Kind.String())
}

// fireShot releases the draw, letting the arrow or bolt go. The head position
// is the eye height, which is what the release is measured from.
func (cm *CombatManager) fireShot(s Shot, now time.Time, target *entity.Info) {
	head := cm.bot.GetCoords().Add(mgl32.Vec3{0, 1.62, 0})
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.ReleaseItemTransactionData{
			ActionType:   protocol.ReleaseItemActionRelease,
			HotBarSlot:   safecast.To[int32](s.Slot),
			HeldItem:     protocol.ItemInstance{Stack: cm.bot.GetInventorySlots()[s.Slot]},
			HeadPosition: head,
		},
	}
	if err := cm.bot.WritePacket(tx); err != nil {
		cm.logger.Error("Failed to write shot release transaction", "error", err)
		return
	}
	cm.mu.Lock()
	cm.Shot.recordRelease(now)
	// A crossbow holds its bolt once loaded, so the next release fires
	// instantly instead of paying the load time again.
	cm.Shot.Loaded = s.Kind == WeaponCrossbow
	cm.mu.Unlock()
	cm.logger.Info("Shot released", "target", target.Name, "kind", s.Kind.String())
}

// normalizedMobName is the target's mob name in the canonical form the tactic
// table is keyed on.
func normalizedMobName(target *entity.Info) string {
	if name := entity.NormalizeName(target.Name); name != "" {
		return name
	}
	return entity.NormalizeName(target.Type)
}

// HorizontalDistance is the ground distance between two points, ignoring
// height: tactics care about how far the bot is across the ground, not
// through the air.
func HorizontalDistance(a, b mgl32.Vec3) float32 {
	dx, dz := a.X()-b.X(), a.Z()-b.Z()
	return float32(math.Sqrt(float64(dx*dx + dz*dz)))
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
	s.HasArrows = HasArrows(inventory)
	s.Health, s.MaxHealth = cm.botHealth()
	return s
}

// HasArrows looks for ammunition rather than assuming a bow implies one.
//
// A bow with nothing to shoot is a stick, and a bot that switches to it because
// the target moved out of range and then stands there holding it is worse than
// one that never switched.
func HasArrows(inventory map[uint32]string) bool {
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
