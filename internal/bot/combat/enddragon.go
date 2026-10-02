// The Ender Dragon fight, decided over plain data.
//
// The dragon is the one fight the generic engage loop cannot be pointed at. It
// spends most of the fight out of reach, it flings the bot into the void for
// standing still, and the fastest way to hurt it is to shoot something that is
// not it. None of that is expressible in MobMovePlan, which is keyed on a mob
// name and answers with "strafe" or "charge" — charging a dragon is suicide and
// strafing a dragon is a slower suicide.
//
// So the fight gets its own decision layer, built the same way choice.go and
// tactics.go are: a pure function over a value, table-tested, with the packets
// left on the other side of it. Nothing here touches a connection.

package combat

import (
	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
)

// Distances the dragon fight changes behaviour at, in blocks.
const (
	// dragonDiveDistance is how close the dragon has to be for a pass to count
	// as a pass at the bot rather than a circle to ignore. The dragon's melee
	// reach is short, but it is moving, so the bot starts leaving before it is
	// technically in range.
	dragonDiveDistance float32 = 6.0
	// DragonRetreatDistance is where backing off from a dragon stops. It is
	// outside the dive distance by enough that a single tick of movement
	// decides the fight rather than two, which is what stops a bot from
	// oscillating in and out of the danger radius for the whole fight.
	DragonRetreatDistance float32 = 12.0
	// dragonPerchHeight is how far above the bot the dragon can be and still be
	// considered reachable. Perched, it settles a few blocks over the fountain;
	// circling, it is thirty up and nothing anyone on the ground can touch.
	dragonPerchHeight float32 = 8.0
	// dragonMeleeDistance is the swing threshold, the same 3.5 the generic loop
	// uses, so a dragon that has come down to the fountain is hit rather than
	// circled.
	dragonMeleeDistance float32 = 3.5
	// CrystalRange is how far the bot will engage a crystal. It matches the 32
	// blocks the generic loop gives up at: a pillar further out than that is
	// not a fight, it is a walk.
	CrystalRange float32 = 32.0
	// CrystalBandMin and CrystalBandMax are the distance band the bot holds
	// while shooting a crystal. Inside the floor it would rather have the space
	// back; past the ceiling the arrow spends so long in the air that the
	// dragon arrives before it does.
	CrystalBandMin float32 = 6.0
	CrystalBandMax float32 = 16.0
	// BeamCorridorRadius and beamCorridorDrop describe the column under the
	// dragon that its healing beam travels down. Radius is how far off the
	// dragon's axis the bot has to be to be clear of it; drop is how high the
	// dragon has to be for standing under it to mean anything at all, because
	// a perched dragon is not overhead and has no beam to be caught in.
	BeamCorridorRadius float32 = 3.0
	beamCorridorDrop   float32 = 8.0
	// beamEscapeDistance is where the escape from that column stops: well
	// outside the corridor, but still a walk rather than a flight.
	beamEscapeDistance float32 = 8.0
	// crystalAimHeight is the middle of an End crystal, which is where the
	// arrow has to go. It is not a two-block mob, so it does not get the 1.2
	// block body centre BowAimPoint gives everything else.
	crystalAimHeight float32 = 1.0
)

// DragonAction is what the bot does about the fight this tick.
type DragonAction int

const (
	// DragonReposition is the "nothing to hit right now" answer: the dragon is
	// unreachable, or there is no crystal worth closing on. It is a real
	// action, not a failure — it moves the bot to where the fight will be.
	DragonReposition DragonAction = iota
	// DragonShootCrystal makes a crystal the target. A crystal is the fastest
	// way to hurt the dragon and the only thing that stops the healing, so it
	// outranks the dragon itself even when the dragon is in sword reach.
	DragonShootCrystal
	// DragonMeleePerch is the swing: the dragon has come down to the fountain,
	// or a crystal is in reach and there is nothing to shoot it with.
	DragonMeleePerch
	// DragonRetreatAndHeal breaks off. The bot cannot eat — that is the
	// survival package's job — so this is the distance and the shield, which
	// is all the fight itself can do about it.
	DragonRetreatAndHeal
)

// String names the action for a log line.
func (a DragonAction) String() string {
	switch a {
	case DragonShootCrystal:
		return "shoot the crystal"
	case DragonMeleePerch:
		return "hit what is in reach"
	case DragonRetreatAndHeal:
		return "break off and heal"
	default:
		return "reposition"
	}
}

// DragonSituation is one reading of the fight. It is a value, like Situation in
// choice.go, so the plan is made from a single snapshot and the fight cannot
// change underneath the reasoning.
type DragonSituation struct {
	// BotPosition and DragonPosition are the two ends the geometry turns on:
	// the height difference is what separates a dragon at the fountain from
	// one circling overhead, and the horizontal distance is what separates a
	// dive from a fly-by.
	BotPosition    mgl32.Vec3
	DragonPosition mgl32.Vec3
	// DragonDistance is the straight-line distance to the dragon, in blocks.
	DragonDistance float32
	// CrystalsRemaining is how many End crystals are still up. Zero is the
	// whole fight changing shape: with none left the dragon stops healing and
	// the fight becomes a duel instead of a siege.
	CrystalsRemaining int
	// NearestCrystalDistance is how far the closest live crystal is. It is not
	// derivable from CrystalsRemaining, and the plan needs both: a count
	// without a distance cannot tell a crystal in reach from one across the
	// arena.
	NearestCrystalDistance float32
	// Weapon and HasArrows are the shooting capability, read the same way the
	// weapon choice reads it.
	Weapon    WeaponKind
	HasArrows bool
	// Health and MaxHealth decide when trading stops being an option.
	Health    int
	MaxHealth int
}

// PlanDragonFight decides the posture for this tick.
//
// The order is the whole decision and it runs survival first, because both of
// the other answers assume the bot is still standing at the end of them: a
// crystal is worth nothing to a bot that has just been knocked into the void,
// and a swing at the fountain is worth nothing to a bot at three hearts.
func PlanDragonFight(s DragonSituation) DragonAction {
	if s.struggling() || IsDragonDiving(s.BotPosition, s.DragonPosition) {
		return DragonRetreatAndHeal
	}

	if s.CrystalsRemaining > 0 {
		if s.canShoot() && s.NearestCrystalDistance <= CrystalRange {
			return DragonShootCrystal
		}
		if s.NearestCrystalDistance <= dragonMeleeDistance {
			// No bow and no crossbow: a crystal still has to be broken by hand
			// if the bot is standing next to it. It is the fallback, not the
			// plan, which is why it only applies inside swing range.
			return DragonMeleePerch
		}
		return DragonReposition
	}

	// No crystals: the dragon cannot heal, so a dragon at the fountain is a
	// stationary target with a health bar and the fight is just hitting it.
	if IsDragonPerched(s.BotPosition, s.DragonPosition) && s.DragonDistance <= dragonMeleeDistance {
		return DragonMeleePerch
	}
	return DragonReposition
}

// struggling reports whether the bot is too hurt to keep trading.
//
// The threshold is the same fraction the shield decision uses, deliberately: a
// single "below this, stop committing" number for the whole package. A bot that
// cannot read its own health is not assumed to be dying, because assuming so
// would make it run from every fight it has ever been in.
func (s DragonSituation) struggling() bool {
	if s.MaxHealth <= 0 {
		return false
	}
	return float64(s.Health) <= float64(s.MaxHealth)*lowHealthFraction
}

// canShoot reports whether what the bot is holding can put an arrow into a
// crystal. A crossbow fires whatever bolt it is already holding, so it counts
// without arrows in the pack; a bow does not. This is PlanShot's rule, applied
// the same way here so the two cannot disagree about whether a shot is coming.
func (s DragonSituation) canShoot() bool {
	switch s.Weapon {
	case WeaponCrossbow:
		return true
	case WeaponBow:
		return s.HasArrows
	default:
		return false
	}
}

// IsDragonPerched reports whether the dragon is low enough to be hit.
//
// This is a height test on its own: a dragon hovering at the fountain's height
// two hundred blocks away passes it. The caller adds the distance, because
// reach and altitude are separate facts and only one of them is height.
func IsDragonPerched(botPos, dragonPos mgl32.Vec3) bool {
	return dragonPos.Y()-botPos.Y() <= dragonPerchHeight
}

// IsDragonDiving reports whether the dragon is a pass at the bot rather than a
// circle to ignore.
//
// Velocity is not readable from the tracked entity data, so this is a proxy and
// it is deliberately a conservative one: close, and above the perch height. The
// height half is what keeps the melee window open, because a dragon sitting at
// the fountain is both close and above the bot, and reading that as a dive
// would mean the bot never gets a hit in for the whole fight.
func IsDragonDiving(botPos, dragonPos mgl32.Vec3) bool {
	return dragonPos.Y()-botPos.Y() > dragonPerchHeight &&
		HorizontalDistance(botPos, dragonPos) <= dragonDiveDistance
}

// InHealingBeam reports whether the bot is standing in the column the dragon
// drains its crystals through.
//
// The beam runs from the dragon down to the crystal it is feeding on, so the
// place to avoid is directly under the dragon — and only while there is
// something to heal. With every crystal destroyed the dragon has no beam at
// all, and standing under it is merely being in the way.
func InHealingBeam(botPos, dragonPos mgl32.Vec3, crystalsRemaining int) bool {
	if crystalsRemaining <= 0 {
		return false
	}
	if dragonPos.Y()-botPos.Y() < beamCorridorDrop {
		return false
	}
	return HorizontalDistance(botPos, dragonPos) <= BeamCorridorRadius
}

// IsEndCrystal reports whether a name is an End crystal.
//
// Namespaces and spacing both vary by server, so this goes through the same
// normalizer the mob tactics use: "minecraft:end_crystal", "end_crystal" and
// "End Crystal" are one thing.
func IsEndCrystal(name string) bool {
	return entity.NormalizeName(name) == "end_crystal"
}

// IsEnderDragon reports whether a name is the dragon. It is the test the tick
// branches on, so it has to be exact: a bot that misreads a crystal as the
// dragon would stop shooting the thing it is supposed to be shooting.
func IsEnderDragon(name string) bool {
	switch entity.NormalizeName(name) {
	case "ender_dragon", "dragon":
		return true
	default:
		return false
	}
}

// EndCrystals is the live crystals in a set of tracked actors.
//
// A crystal with no health is already gone: the tracker writes 20 for every
// actor it adds and the fight is only ever run against actors it is actively
// tracking, so a zero here is a real death rather than missing data.
func EndCrystals(actors map[uint64]*entity.Info) []*entity.Info {
	var out []*entity.Info
	for _, info := range actors {
		if info == nil || info.Health <= 0 {
			continue
		}
		if !IsEndCrystal(info.Name) && !IsEndCrystal(info.Type) {
			continue
		}
		out = append(out, info)
	}
	return out
}

// PickCrystal chooses which crystal to deal with this tick.
//
// Nearest wins, and the entity ID breaks a tie. The tie-break is not tidiness:
// Go randomises map iteration, so without it a bot standing between two
// pillars picks a different one every tick, draws a full bow at each in turn,
// and hits neither.
func PickCrystal(crystals []*entity.Info, botPos mgl32.Vec3, maxDistance float32) *entity.Info {
	var best *entity.Info
	bestDistance := float32(0)
	for _, c := range crystals {
		if c == nil {
			continue
		}
		d := HorizontalDistance(botPos, c.Position)
		if d > maxDistance {
			continue
		}
		if best == nil || d < bestDistance || (d == bestDistance && c.ID < best.ID) {
			best, bestDistance = c, d
		}
	}
	return best
}

// DragonDestination is where the bot is actually told to go this tick.
//
// The healing beam overrides the preferred point, because the beam is a source
// of damage and everything else the fight wants is only a choice. It is checked
// in both directions: where the bot stands now, and where it was about to walk,
// so it cannot step out of the column and straight back into it on the way to
// a crystal. With no crystals left there is no beam to dodge and the preferred
// point stands.
func DragonDestination(botPos, dragonPos, preferred mgl32.Vec3, crystalsRemaining int) mgl32.Vec3 {
	if InHealingBeam(botPos, dragonPos, crystalsRemaining) || InHealingBeam(preferred, dragonPos, crystalsRemaining) {
		return beamEscapePoint(botPos, dragonPos)
	}
	return preferred
}

// beamEscapePoint is the way out of the dragon's column: away from it, by more
// than the corridor is wide, keeping the bot's own height so the pathfinder is
// walking and not falling.
func beamEscapePoint(botPos, dragonPos mgl32.Vec3) mgl32.Vec3 {
	return RetreatPoint(botPos, dragonPos, beamEscapeDistance)
}

// DragonDodgePoint is retreat and strafe at once, built from the two primitives
// the mob tactics already use: the full retreat distance, on a bearing a quarter
// turn off the line the dragon is already on.
//
// Backwards down that line is a retreat the dragon simply follows, and a fight
// where the bot can never leave is a fight it loses on the clock. side picks
// which way, and flipping it between ticks is what keeps the dodge from being
// one predictable arc.
func DragonDodgePoint(botPos, dragonPos mgl32.Vec3, side float32) mgl32.Vec3 {
	// The dragon is the anchor, so the distance here is measured from it: the
	// bot ends up exactly DragonRetreatDistance out and a quarter turn off the
	// line it started on.
	return StrafePoint(botPos, dragonPos, DragonRetreatDistance, side)
}

// CrystalChoice is the weapon to hold against a crystal.
//
// It deliberately does not go through ChooseWeapon. That function tiers a sword
// in once the target is inside eight blocks, which is right for a mob and wrong
// here: a crystal dies to a single arrow, so the best answer to one four blocks
// away is the shot already in the bow rather than walking up to the thing that
// is healing the dragon.
func CrystalChoice(inventory map[uint32]string) WeaponChoice {
	if slot := findBest(inventory, WeaponCrossbow); slot >= 0 {
		return WeaponChoice{
			Slot:   uint32(slot),
			Name:   inventory[uint32(slot)],
			Kind:   WeaponCrossbow,
			Reason: "crossbow: a crystal dies to a shot and needs no draw between them",
		}
	}
	if slot := findBest(inventory, WeaponBow); slot >= 0 {
		return WeaponChoice{
			Slot:   uint32(slot),
			Name:   inventory[uint32(slot)],
			Kind:   WeaponBow,
			Reason: "bow: a crystal dies to a single arrow",
		}
	}
	return WeaponChoice{Kind: WeaponNone, Reason: "nothing to shoot a crystal with"}
}

// CrystalAimPoint is BowAimPoint for a crystal: the middle of the target rather
// than a mob's chest, with the same gravity lift on top so the arrow arrives
// where it was aimed.
func CrystalAimPoint(from, crystal mgl32.Vec3) mgl32.Vec3 {
	return liftAimPoint(from, crystal, crystalAimHeight)
}
