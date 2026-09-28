// Choosing how to fight, separately from fighting.
//
// The combat manager already knows how to close distance, swing, and check line
// of sight. What it did not know was what to hold while doing it: it walked a
// static tier list and took the first match, so a bot with a bow and a sword
// picked the sword regardless of whether the creeper was four blocks away or
// forty, and a bot standing in a swarm picked a sword when an axe would have hit
// three of them at once.
//
// The decisions live here, over plain data, for the same reason the survival
// manager's decisions live where they do: a choice that has to be tested by
// starting a fight cannot be tested at all. Nothing in this file touches a
// packet, a body, or a connection.

package combat

import "strings"

// WeaponKind is what sort of weapon was chosen, not which one.
//
// The kind is what the decision actually turns on — reach, sweep, burst — while
// the specific tier within a kind is a straight "best of what you are carrying".
type WeaponKind int

const (
	// WeaponNone means the bot has nothing to fight with. It is a real answer,
	// not a failure: fists are a legitimate last resort and pretending otherwise
	// would only produce a bot that stands still instead of running.
	WeaponNone WeaponKind = iota
	WeaponSword
	// WeaponAxe hits harder per swing and sweeps a wide arc, so it clears a
	// group where a sword takes them one at a time.
	WeaponAxe
	// WeaponBow and WeaponCrossbow keep the bot out of reach entirely.
	WeaponBow
	WeaponCrossbow
	// WeaponTrident throws from a distance and is the best thing in water.
	WeaponTrident
	// WeaponShield is defensive and is never chosen to deal damage.
	WeaponShield
)

// String names the kind for a log line.
func (k WeaponKind) String() string {
	switch k {
	case WeaponSword:
		return "sword"
	case WeaponAxe:
		return "axe"
	case WeaponBow:
		return "bow"
	case WeaponCrossbow:
		return "crossbow"
	case WeaponTrident:
		return "trident"
	case WeaponShield:
		return "shield"
	default:
		return "fists"
	}
}

// Situation is everything the weapon choice turns on. It is a value on purpose:
// the decision is made from one reading, so the fight cannot change underneath
// the reasoning.
type Situation struct {
	// TargetDistance is how far the current target is, in blocks.
	TargetDistance float32
	// MeleeHostiles is how many hostiles are close enough to hit the bot. This
	// is the number that decides between a sword and an axe.
	MeleeHostiles int
	// NearbyHostiles is how many are in the fight at all, at any range.
	NearbyHostiles int
	// Underwater changes the answer completely: a bow is useless and a trident
	// is the best thing in the game.
	Underwater bool
	// HasArrows. A bow with nothing to shoot is a stick.
	HasArrows bool
	// Health and MaxHealth decide whether blocking or hitting is the better
	// use of the next second.
	Health    int
	MaxHealth int
}

// WeaponChoice is what to hold and why.
type WeaponChoice struct {
	// Slot is the inventory slot to equip, meaningful only when Kind is not
	// WeaponNone.
	Slot uint32
	// Name is the item as it is actually written, for the log.
	Name string
	Kind WeaponKind
	// Reason is the one-line justification. It is carried rather than logged at
	// the call site because the reasoning is the thing worth being able to read
	// back: "why did it swap weapons" is the question every log reader asks and
	// almost none of them answer.
	Reason string
}

// Distances at which the answer changes.
const (
	// rangedThreshold is where a bow starts beating a sword. A sword's reach is
	// about three blocks; past roughly two sword-lengths the bot spends more time
	// closing than swinging, and a bow spends that time shooting instead.
	rangedThreshold float32 = 8.0
	// meleeRange is how close a hostile has to be to be a threat worth blocking
	// against.
	meleeRange float32 = 4.0
	// lowHealthFraction is when blocking stops being sensible. Below it the bot
	// is better off committing to the hit than to the guard, because nothing it
	// does at one health is going to be subtle.
	lowHealthFraction = 0.4
)

// weaponTiers is the best-to-worst order within each kind.
//
// This is the existing priority list, split up. A single list across all kinds
// can only express "which is nicest", which is why the previous behaviour could
// not tell a sword from a bow; the choice between kinds has to come first, and
// only then does the tier decide within the kind.
var weaponTiers = map[WeaponKind][]string{
	WeaponSword:    {"netherite_sword", "diamond_sword", "iron_sword", "stone_sword", "golden_sword", "wooden_sword"},
	WeaponAxe:      {"netherite_axe", "diamond_axe", "iron_axe", "stone_axe", "golden_axe", "wooden_axe"},
	WeaponBow:      {"netherite_bow", "bow"},
	WeaponCrossbow: {"crossbow"},
	WeaponTrident:  {"trident"},
	WeaponShield:   {"shield"},
}

// chooseKind decides which KIND of weapon the situation calls for.
//
// The order is the whole decision, and it runs from most situational to least:
// water and distance and numbers of enemies all override the default, because
// each of them is a case where the default is simply the wrong tool.
func chooseKind(s Situation) (WeaponKind, string) {
	switch {
	case s.Underwater:
		// Underwater a bow is a club and a melee swing may not even connect. A
		// trident throws from range, which is the only reason a bot in water is
		// still dangerous.
		return WeaponTrident, "underwater: a trident throws and a bow does not"

	case s.TargetDistance >= rangedThreshold && s.HasArrows:
		// Prefer the crossbow: it does not need to be re-drawn between shots, so
		// at range it is strictly the better of the two bows.
		return WeaponCrossbow, "target is out of melee reach and there are arrows"

	case s.MeleeHostiles >= 2:
		// An axe swing sweeps a wide arc, so it lands on all of them. A sword
		// takes them one at a time and the first one you kill is the one that
		// was hitting you.
		return WeaponAxe, "more than one hostile in melee: an axe sweeps the group"

	default:
		return WeaponSword, "one target at sword range"
	}
}

// ChooseWeapon picks what to hold for a fight, from a slot-to-name view of the
// inventory.
//
// It is pure and takes the inventory as plain data so the whole decision can be
// exercised without a bot. A decision that has to be tested by starting a fight
// is a decision that never gets tested.
func ChooseWeapon(inventory map[uint32]string, s Situation) WeaponChoice {
	kind, reason := chooseKind(s)

	// The crossbow case is conditional on there being a crossbow at all: a bot
	// with a bow and arrows should still shoot. Falling through to the generic
	// search for the chosen kind covers that, because Bow is a separate kind.
	if kind == WeaponCrossbow && findBest(inventory, WeaponCrossbow) < 0 {
		kind = WeaponBow
		reason = "target is out of melee reach and there are arrows"
	}

	slot := findBest(inventory, kind)
	if slot < 0 {
		if kind != WeaponSword {
			// Fall back rather than give up. A bot holding a bow that cannot
			// reach is far worse off than one holding the sword it was ignoring.
			slot = findBest(inventory, WeaponSword)
			if slot >= 0 {
				reason += ", but that was not carried"
				return WeaponChoice{Slot: uint32(slot), Name: inventory[uint32(slot)], Kind: WeaponSword, Reason: reason}
			}
		}
		return WeaponChoice{Kind: WeaponNone, Reason: "nothing to fight with"}
	}
	return WeaponChoice{
		Slot:   uint32(slot),
		Name:   inventory[uint32(slot)],
		Kind:   kind,
		Reason: reason,
	}
}

// findBest returns the inventory slot holding the best item of a kind, or -1.
//
// It walks the tier list and then the slots, so a player with two swords always
// gets the better one — and, because slots are walked in index order rather than
// map order, always gets the same one, which is what keeps a log readable and a
// bot from swapping between two identical items forever.
func findBest(inventory map[uint32]string, kind WeaponKind) int {
	for _, tier := range weaponTiers[kind] {
		best := -1
		for slot := 0; slot < 64; slot++ {
			name, held := inventory[uint32(slot)]
			if !held {
				continue
			}
			if matchesItem(name, tier) {
				best = slot
				break
			}
		}
		if best >= 0 {
			return best
		}
	}
	return -1
}

// HasKind reports whether the inventory holds anything of a kind.
func HasKind(inventory map[uint32]string, kind WeaponKind) bool {
	return findBest(inventory, kind) >= 0
}

// matchesItem matches an item name against a tier, tolerating namespaces and
// a "minecraft:" prefix that some servers put on everything.
//
// The order matters: the suffix is checked first so "minecraft:bow" does not get
// picked up by a tier that is a prefix of something else.
func matchesItem(itemName, tier string) bool {
	name := strings.ToLower(strings.TrimSpace(itemName))
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	return name == tier || strings.HasSuffix(name, "_"+tier)
}

// ShieldPlan is what to do about the shield.
type ShieldPlan int

const (
	// ShieldHold leaves the shield where it is.
	ShieldHold ShieldPlan = iota
	// ShieldRaise puts it up.
	ShieldRaise
	// ShieldLower puts it away, freeing the off hand.
	ShieldLower
)

// String names the plan for a log line.
func (p ShieldPlan) String() string {
	switch p {
	case ShieldRaise:
		return "raise"
	case ShieldLower:
		return "lower"
	default:
		return "hold"
	}
}

// PlanShield decides whether the shield goes up.
//
// The three states it distinguishes are the three that were previously
// conflated into "the bot has a shield, so put it up", which produced a bot
// permanently blocking with no weapon and a bot that never blocked at all.
//
//   - Raise: something is hitting the bot and it is healthy enough to afford
//     the seconds.
//   - Lower: the threat is gone, or the bot is close enough to finish the
//     target that the damage it is taking does not matter.
//   - Hold: nothing has changed, and swapping costs a tick for nothing.
func PlanShield(inventory map[uint32]string, s Situation, currentlyUp bool) ShieldPlan {
	if !HasKind(inventory, WeaponShield) {
		// A bot without a shield cannot raise one. Lowering when it is already
		// down would be a no-op logged as a decision.
		if currentlyUp {
			return ShieldLower
		}
		return ShieldHold
	}

	threatened := s.MeleeHostiles > 0 && s.TargetDistance <= meleeRange

	// Committed to the kill. Blocking now trades a certain hit for a certain
	// death, so the shield comes down and the sword comes up.
	desperate := s.MaxHealth > 0 && float64(s.Health) <= float64(s.MaxHealth)*lowHealthFraction

	switch {
	case threatened && !desperate:
		return ShieldRaise
	case currentlyUp && (!threatened || desperate):
		return ShieldLower
	default:
		return ShieldHold
	}
}
