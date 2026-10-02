// The End fight on the tick: turning the plan in enddragon.go into movement,
// aim and packets.
//
// Everything decided here was decided in enddragon.go; this file is only the
// part that touches the bot. The split is the same one tactics.go and
// combat_attack.go have between them, and it exists for the same reason: the
// interesting part of a boss fight is the decision, and a decision buried
// between packet writes is a decision that never gets tested.

package combat

import (
	"math"
	"time"

	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
)

// tickDragonFight runs the Ender Dragon fight in place of the generic engage
// loop.
//
// It is entered from the target's identity rather than from the dimension. A
// dragon entity is only ever tracked in the End, so the entity is the stricter
// test of the two, and it is the one that cannot be fooled by a bot that has
// just arrived and has not seen enough end stone to classify the terrain yet.
func (cm *CombatManager) tickDragonFight(dragon *entity.Info) {
	botPos := cm.bot.GetCoords()
	// One reading of the arena for the whole tick: GetEntities copies, and
	// asking three times would give three answers at three different moments.
	crystals := EndCrystals(cm.bot.GetEntities())
	s := cm.dragonSituation(dragon, botPos, crystals)
	action := PlanDragonFight(s)

	cm.logDragonPlan(action, s)
	cm.applyShield(cm.dragonShield(s))

	switch action {
	case DragonShootCrystal:
		cm.fightCrystal(s, botPos, dragon, crystals)
	case DragonMeleePerch:
		cm.fightPerch(s, botPos, dragon, crystals)
	case DragonRetreatAndHeal:
		cm.fightRetreat(botPos, dragon)
	default:
		cm.fightReposition(s, botPos, dragon, crystals)
	}
}

// dragonSituation reads the fight into the value the plan is decided on.
func (cm *CombatManager) dragonSituation(dragon *entity.Info, botPos mgl32.Vec3, crystals []*entity.Info) DragonSituation {
	inventory := cm.slotNames()
	health, maxHealth := cm.botHealth()

	s := DragonSituation{
		BotPosition:       botPos,
		DragonPosition:    dragon.Position,
		DragonDistance:    cm.distance(botPos, dragon.Position),
		CrystalsRemaining: len(crystals),
		// The weapon that matters in this fight is the one that reaches a
		// crystal, not the one that reaches a mob, so that is the one the plan
		// is told about.
		Weapon:    CrystalChoice(inventory).Kind,
		HasArrows: HasArrows(inventory),
		Health:    health,
		MaxHealth: maxHealth,
	}
	// The nearest crystal over the whole arena, not the nearest one in range:
	// a crystal the bot cannot reach yet is the reason to reposition, and
	// reporting zero for it would look exactly like a crystal underfoot.
	if nearest := PickCrystal(crystals, botPos, math.MaxFloat32); nearest != nil {
		s.NearestCrystalDistance = HorizontalDistance(botPos, nearest.Position)
	}
	return s
}

// dragonShield feeds the generic shield rule from the dragon reading, so the
// one place that decides about shields still does. A dragon on top of the bot
// is a melee threat exactly as a skeleton is; one circling at range is not.
func (cm *CombatManager) dragonShield(s DragonSituation) Situation {
	melee := 0
	if s.DragonDistance <= meleeRange {
		melee = 1
	}
	return Situation{
		TargetDistance: s.DragonDistance,
		NearbyHostiles: 1,
		MeleeHostiles:  melee,
		HasArrows:      s.HasArrows,
		Health:         s.Health,
		MaxHealth:      s.MaxHealth,
	}
}

// logDragonPlan says what the fight is doing, but only when it changes. A
// combat tick runs many times a second, and the reason for the current posture
// is worth reading once rather than five times a second.
func (cm *CombatManager) logDragonPlan(action DragonAction, s DragonSituation) {
	if cm.dragonActionSet && cm.dragonAction == action {
		return
	}
	cm.dragonAction, cm.dragonActionSet = action, true
	cm.logger.Info("Ender Dragon: plan changed",
		"action", action.String(),
		"distance", s.DragonDistance,
		"crystals", s.CrystalsRemaining,
		"health", s.Health,
	)
}

// fightCrystal makes a crystal the target: the fastest way to hurt the dragon
// and the only thing that stops the healing.
func (cm *CombatManager) fightCrystal(s DragonSituation, botPos mgl32.Vec3, dragon *entity.Info, crystals []*entity.Info) {
	crystal := PickCrystal(crystals, botPos, CrystalRange)
	if crystal == nil {
		// The plan and the arena disagree: the last crystal went up between
		// the reading and this line. Repositioning is always safe, and drawing
		// a bow at the dragon instead is the exact mistake this fight exists
		// to prevent.
		cm.fightReposition(s, botPos, dragon, crystals)
		return
	}

	if !cm.hasLineOfSight(crystal) {
		// Obsidian between the bot and the pillar. The arrow would hit the
		// pillar, so close the gap instead of drawing at it — a shot at a
		// crystal behind a wall is a full bow spent on the wall.
		cm.fightCrystalGround(s, botPos, dragon, crystal.Position)
		return
	}

	choice := CrystalChoice(cm.slotNames())
	if choice.Kind == WeaponNone {
		cm.fightReposition(s, botPos, dragon, crystals)
		return
	}

	cm.holdWeapon(choice)
	cm.bot.LookAt(CrystalAimPoint(botPos, crystal.Position))
	cm.fightCrystalGround(s, botPos, dragon, crystal.Position)
	// Re-read the ammunition rather than trusting the reading the plan was
	// made on: the whole reason the bow can be useless is that the arrows ran
	// out, and that can happen between two ticks.
	cm.shootRanged(choice, HasArrows(cm.slotNames()), crystal)
}

// fightCrystalGround is the standing-room half of a crystal shot: hold the
// shooting band, give the space back if pushed into the crystal, and close if
// the arrow would be spending too long in the air.
func (cm *CombatManager) fightCrystalGround(s DragonSituation, botPos mgl32.Vec3, dragon *entity.Info, crystal mgl32.Vec3) {
	hd := HorizontalDistance(botPos, crystal)
	ground, moving := botPos, false
	switch {
	case hd < CrystalBandMin:
		ground, moving = RetreatPoint(botPos, crystal, CrystalBandMin), true
	case hd > CrystalBandMax:
		ground, moving = crystal, true
	}

	// The beam guard is applied after the band, because it can turn a hold into
	// a move: standing still under the dragon is the one thing this fight must
	// never do, and it outranks a comfortable shooting position.
	if dest := DragonDestination(botPos, dragon.Position, ground, s.CrystalsRemaining); dest != ground || moving {
		cm.bot.NavigateTo(dest)
		return
	}
	// Inside the band with nothing to dodge: hold, rather than strafe. The End
	// island is small and everything past its edge is a fall into the void, so
	// a lateral move here buys distance the fight does not need at the price of
	// a death it very much does.
	cm.bot.StopMovement()
}

// fightPerch is the swing. While crystals are up a crystal within reach is the
// better target, because a broken crystal stops the healing and a hit on the
// dragon does not; with none left, the dragon itself is a stationary target
// with a health bar.
func (cm *CombatManager) fightPerch(s DragonSituation, botPos mgl32.Vec3, dragon *entity.Info, crystals []*entity.Info) {
	target := dragon
	if crystal := PickCrystal(crystals, botPos, dragonMeleeDistance); crystal != nil {
		target = crystal
	}

	choice := ChooseWeapon(cm.slotNames(), Situation{
		TargetDistance: cm.distance(botPos, target.Position),
		NearbyHostiles: 1,
		MeleeHostiles:  1,
		Health:         s.Health,
		MaxHealth:      s.MaxHealth,
	})
	if choice.Kind != WeaponNone {
		cm.holdWeapon(choice)
	}
	// The swing runs even with nothing chosen to hold: the generic loop does
	// the same, and a bot that refuses to swing because its inventory is empty
	// loses the only fight it could have won.
	cm.swingAt(target)
}

// fightRetreat breaks off: away from the dragon, sideways off the line it is
// on, still watching it. Healing itself is the survival package's business —
// all this can do is make the bot survive long enough to be healed.
func (cm *CombatManager) fightRetreat(botPos mgl32.Vec3, dragon *entity.Info) {
	// The side flips on the same two-second beat the mob tactics use, so a
	// dodge is never the same arc twice in a row.
	side := float32(1)
	if int(time.Now().Unix()/2)%2 == 1 {
		side = -1
	}
	cm.bot.NavigateTo(DragonDodgePoint(botPos, dragon.Position, side))
	cm.bot.LookAt(dragon.Position)
}

// fightReposition is the no-target answer, and the two halves of it are very
// different on purpose.
//
// A crystal out of reach is worth walking at: getting to it is the only way the
// fight gets better. Nothing to shoot at is not — with no crystal in range and
// the dragon thirty blocks up there is no point in the arena it has to be in,
// and chasing the dragon's shadow around a small island is how a bot walks off
// the obsidian. So it holds, and the next thing that happens is the dragon
// coming down where it already is.
func (cm *CombatManager) fightReposition(s DragonSituation, botPos mgl32.Vec3, dragon *entity.Info, crystals []*entity.Info) {
	if crystal := PickCrystal(crystals, botPos, math.MaxFloat32); crystal != nil {
		// The height is the bot's own, so the order is "walk there" and never
		// "fly there", and the beam guard still applies to the destination.
		ground := mgl32.Vec3{crystal.Position.X(), botPos.Y(), crystal.Position.Z()}
		cm.bot.NavigateTo(DragonDestination(botPos, dragon.Position, ground, s.CrystalsRemaining))
	} else {
		cm.bot.StopMovement()
	}
	cm.bot.LookAt(dragon.Position)
}
