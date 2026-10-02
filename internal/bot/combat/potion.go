// Potions: the only ranged weapon whose result arrives after the throw, and
// the only one where "I sent it" and "it hit" are different claims.
//
// Every other weapon in this package can be judged the instant it leaves the
// hand. An arrow is a miss or it is not. A potion is not: the release packet is
// written long before the bottle lands, and nothing about having sent it is
// evidence about where it ended up. A bot that reports a potion hit from having
// thrown a potion reports a hit every single time — which is worse than never
// reporting one, because the log looks right and the bot learns nothing from
// it.
//
// So this file is two halves that are deliberately not allowed to touch.
//
// The first half is the decision: is this the moment to throw, and at what. It
// is a pure function like PlanDragonFight and it knows nothing about outcomes.
//
// The second half is the reading. The server does say a potion landed, and the
// key it says it with is protocol.EntityDataKeyVisibleMobEffects — a compound
// tag on the target's actor holding the effect IDs currently on it and their
// remaining durations. That was verified by round-tripping a real SetActorData
// through this module's own writer and reader, not taken from memory: key 131
// arrives as a map whose values are int32 durations keyed by effect ID as a
// decimal string.
//
// The reading is exposed as an interface rather than a concrete bot call,
// because the bot does not currently retain this key. EntityMetaState in
// observation.go keeps the tamed, collared and baby flags and nothing else, so
// a potion thrown today has no reader at all. The interface is the seam; the
// wiring that fills it is described on PotionObserver and is not in this
// package's scope.

package combat

import (
	"strconv"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// ===================== WHAT TO THROW =====================

// PotionKind is which potion is in the hand.
type PotionKind int

const (
	// PotionNone is no potion.
	PotionNone PotionKind = iota
	// PotionSplash is a splash potion: it bursts on contact and hits whatever
	// is standing in the same block.
	PotionSplash
	// PotionLingering is a lingering potion: it bursts and leaves a cloud
	// that keeps working. Better against something that will walk through the
	// area, worse against something that will walk around it.
	PotionLingering
)

// String names the kind for a log line.
func (k PotionKind) String() string {
	switch k {
	case PotionSplash:
		return "splash"
	case PotionLingering:
		return "lingering"
	default:
		return "none"
	}
}

// PotionAction is what to do about the fight this tick.
type PotionAction int

const (
	// PotionIdle is "do nothing this tick": no potion carried, nothing worth
	// spending one on, no line of sight, or still cooling down from the last.
	PotionIdle PotionAction = iota
	// PotionThrowSplash is the splash throw.
	PotionThrowSplash
	// PotionThrowLingering is the lingering throw.
	PotionThrowLingering
)

// String names the action for a log line.
func (a PotionAction) String() string {
	switch a {
	case PotionThrowSplash:
		return "throw a splash potion"
	case PotionThrowLingering:
		return "throw a lingering potion"
	default:
		return "wait"
	}
}

// Potion reach and timing, in blocks and seconds.
const (
	// PotionMinRange is how close a target may be before a throw is wasted. A
	// bottle released at a target's feet is slower than a swing and the bot
	// has a sword.
	PotionMinRange float32 = 2.0
	// PotionMaxRange is how far a bottle is worth throwing. A thrown potion
	// arcs — much more than an arrow — so past this it is landing somewhere
	// behind the target rather than on it.
	PotionMaxRange float32 = 16.0
	// PotionGroupMin is how many hostiles have to be in the fight for a potion
	// to be the right weapon at all. One target is a bow's job, and spending
	// the pack's last bottle on the first skeleton that walks past is how a
	// bot arrives at the hard part of a fight with nothing left.
	PotionGroupMin = 2
	// PotionThrowInterval is the pause between throws. A potion is a
	// single-use item, so this is not about the animation; it stops a bot
	// from spending three bottles in the same second against a group it could
	// have handled with one.
	PotionThrowInterval = 3 * time.Second
	// PotionSettleTime is how long a thrown bottle is in the air before it is
	// worth reading the target at all. Reading earlier is guaranteed to find
	// nothing and would report every throw as a miss.
	PotionSettleTime = 500 * time.Millisecond
	// PotionReadTimeout is how long the reading stays open. After this the
	// answer is "no reading", not "missed" — the distinction is the whole
	// point of the file.
	PotionReadTimeout = 3 * time.Second
)

// Potion flight, used to aim. A thrown bottle arcs harder than a bolt, so the
// correction is larger than either of the other two.
const (
	potionSpeed   = 3.0
	potionGravity = 0.08
)

// PotionAimPoint is where to aim so the bottle bursts at the target's feet.
//
// A thrown potion is lobbed, not loosed, so it drops a long way over its
// flight and the correction is bigger than the bow's or the bolt's. Aiming level
// at a target fifteen blocks away puts the bottle on the floor well short of it.
func PotionAimPoint(from, target mgl32.Vec3) mgl32.Vec3 {
	return projectileAimPoint(from, target, bowAimHeight, potionSpeed, potionGravity)
}

// PotionSituation is one reading of the fight, taken as a value so the plan
// cannot change underneath the reasoning.
type PotionSituation struct {
	// HasSplashPotion and HasLingeringPotion are what is in the pack.
	HasSplashPotion    bool
	HasLingeringPotion bool
	// TargetDistance is the horizontal distance to the target, in blocks.
	TargetDistance float32
	// NearbyHostiles is how many hostiles are in the fight at all. This is
	// what separates a potion from an arrow.
	NearbyHostiles int
	// LineOfSight is whether the target can be seen.
	LineOfSight bool
	// Underwater changes the answer completely: a thrown bottle arcs
	// unpredictably through water and is likely to burst on the block behind
	// the target. choice.go already answers this fight with a trident.
	Underwater bool
	// State is the potion currently in flight, if any.
	State PotionState
}

// PlanPotion decides whether to throw, and which one.
//
// The order is what the bottle is for, then whether it can be thrown at all,
// then pacing. The group test comes first on purpose: a potion is a
// area-denial and damage weapon, and a bot that spends one on a single mob has
// made the fight harder for itself.
func PlanPotion(s PotionSituation, now time.Time) PotionAction {
	if s.Underwater {
		return PotionIdle
	}
	if !s.HasSplashPotion && !s.HasLingeringPotion {
		return PotionIdle
	}
	if s.NearbyHostiles < PotionGroupMin {
		return PotionIdle
	}
	if !s.LineOfSight || !inThrowBand(s.TargetDistance, PotionMinRange, PotionMaxRange) {
		return PotionIdle
	}
	if !s.State.LastThrow.IsZero() && now.Sub(s.State.LastThrow) < PotionThrowInterval {
		return PotionIdle
	}
	// Splash first: it hits now rather than hoping the target walks into a
	// cloud it can see and step around.
	if s.HasSplashPotion {
		return PotionThrowSplash
	}
	return PotionThrowLingering
}

// PotionState is the throw currently in flight. It outlives the tick that
// started it, because the answer to a throw arrives seconds later.
type PotionState struct {
	// Kind and Slot are what was thrown and from where, so a reading can be
	// matched to the throw that caused it.
	Kind PotionKind
	Slot uint32
	// LastThrow is when the bottle left the hand, and is zero when nothing is
	// in flight.
	LastThrow time.Time
}

// RecordThrow marks a potion as thrown and in flight.
func (s *PotionState) RecordThrow(kind PotionKind, slot uint32, now time.Time) {
	s.Kind = kind
	s.Slot = slot
	s.LastThrow = now
}

// InFlight reports whether a throw is still awaiting a reading.
func (s PotionState) InFlight() bool {
	return !s.LastThrow.IsZero()
}

// ReadyForRead reports whether the target is worth reading yet.
//
// It opens at PotionSettleTime, because reading a target before the bottle has
// landed finds nothing and reports a miss that did not happen, and it never
// closes, because a late reading is still the truth while an absent one is not.
func (s PotionState) ReadyForRead(now time.Time) bool {
	if s.LastThrow.IsZero() {
		return false
	}
	return !now.Before(s.LastThrow.Add(PotionSettleTime))
}

// Clear drops the in-flight throw once it has been read or given up on.
func (s *PotionState) Clear() {
	s.Kind = PotionNone
	s.Slot = 0
	s.LastThrow = time.Time{}
}

// ===================== READING THE RESULT =====================

// PotionEffect is a Bedrock mob effect ID, as it appears in the visible-effects
// compound tag on an entity.
//
// The IDs are the vanilla ones. They are named here so a caller can say "did
// blindness land" without hard-coding a number, but nothing in the protocol
// labels them — the tag carries bare integers, and these names are this
// package's mapping of them.
type PotionEffect int

// The effects a thrown potion can plausibly put on a target.
const (
	EffectSpeed         PotionEffect = 1
	EffectSlowness      PotionEffect = 2
	EffectHaste         PotionEffect = 3
	EffectMiningFatigue PotionEffect = 4
	EffectBlindness     PotionEffect = 5
	EffectPoison        PotionEffect = 19
	EffectWither        PotionEffect = 20
	EffectHealing       PotionEffect = 21
	EffectStrength      PotionEffect = 22
	EffectFireRes       PotionEffect = 25
)

// Effects is what was read off one entity's metadata.
type Effects struct {
	// Observed is whether there was a reading at all. This is separate from
	// being empty, and the difference is the whole file: an entity that was
	// never read is not an entity with no effects on it.
	Observed bool
	// durations is the effect ID to remaining ticks, as the compound tag
	// carries it.
	durations map[PotionEffect]int
}

// EffectsFromMeta reads the visible-effects tag out of an entity's metadata.
//
// An absent key is not an observation — it is a gap, and reporting it as "this
// entity has nothing on it" is how a bot ends up calling a target it simply is
// not tracking un-enchanted. A key that is present but empty *is* a real
// reading: the server said this entity has nothing on it.
//
// Unusable entries are skipped rather than guessed at. A non-numeric key or a
// duration that is not a number is not an effect, and reading one as "some
// unknown effect is active" would let a malformed entry make every target look
// poisoned.
func EffectsFromMeta(meta protocol.EntityMetadata) Effects {
	raw, ok := meta[uint32(protocol.EntityDataKeyVisibleMobEffects)]
	if !ok {
		return Effects{}
	}
	tags, ok := raw.(map[string]any)
	if !ok {
		return Effects{}
	}

	effects := Effects{Observed: true, durations: make(map[PotionEffect]int, len(tags))}
	for key, value := range tags {
		id, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		duration, ok := metadataInt64(value)
		if !ok {
			continue
		}
		effects.durations[PotionEffect(id)] = int(duration)
	}
	return effects
}

// Has reports whether an effect was read on the entity.
func (e Effects) Has(effect PotionEffect) bool {
	_, ok := e.durations[effect]
	return ok
}

// Duration is the effect's remaining ticks, or zero when it is not present.
func (e Effects) Duration(effect PotionEffect) int {
	return e.durations[effect]
}

// Count is the number of usable effects read, which is what a log line
// reports and what the tests assert on: a map of two unusable entries has to
// read as zero effects, not as two.
func (e Effects) Count() int { return len(e.durations) }

// PotionHit reports whether a throw put an effect on a target.
//
// Both readings are required. A "before" that was never taken means there is no
// change to observe, and an "after" that was never taken means there is nothing
// to observe it against. Either gap returns false, because the answer this
// function is being asked for is "did this throw do something", and a missing
// reading is not evidence that it did.
//
// PotionObservation.Outcome is the fuller form of the same question, carrying
// the reasons a hit cannot be claimed; this is the plain predicate.
func PotionHit(before, after Effects, effect PotionEffect) bool {
	if !before.Observed || !after.Observed {
		return false
	}
	return after.Has(effect) && !before.Has(effect)
}

// PotionOutcome is what can honestly be said about a throw.
type PotionOutcome int

const (
	// PotionUnobserved is the answer when the target could not be read. It is
	// the case this file exists for: the bottle was thrown and nothing came
	// back, and the correct report is silence, not a hit.
	PotionUnobserved PotionOutcome = iota
	// PotionLanded is the effect being read on the target afterwards.
	PotionLanded
	// PotionMissed is the target being read, cleanly, with no effect on it.
	PotionMissed
	// PotionExpired is the reading window closing with nothing to show. It is
	// distinct from PotionMissed because nothing was read at all, and from
	// PotionUnobserved because the window has now formally closed.
	PotionExpired
)

// String names the outcome for a log line.
func (o PotionOutcome) String() string {
	switch o {
	case PotionLanded:
		return "landed"
	case PotionMissed:
		return "missed"
	case PotionExpired:
		return "no reading before the deadline"
	default:
		return "not observed"
	}
}

// PotionObservation is one throw and the reading taken after it.
type PotionObservation struct {
	// Before is the target's effects before the throw.
	Before Effects
	// After is the target's effects at the reading.
	After Effects
	// AfterKnown is whether the target could be read at all.
	AfterKnown bool
	// Effect is the effect this throw was supposed to put on the target.
	Effect PotionEffect
	// Expired is whether the reading window closed with no reading.
	Expired bool
}

// Outcome is the one sentence that can be said about the throw.
//
// The order is the whole contract. An expired window is a dead end and says so.
// Without a reading there is nothing to report, ever. Without a "before" there
// is no change to observe, so even a target read full of effects afterwards is
// only an unobserved throw. Only a genuine before-and-after pair can land or
// miss, and of those two only the first is a claim of success.
func (o PotionObservation) Outcome() PotionOutcome {
	if o.Expired {
		return PotionExpired
	}
	if !o.Before.Observed || !o.AfterKnown || !o.After.Observed {
		return PotionUnobserved
	}
	if o.After.Has(o.Effect) && !o.Before.Has(o.Effect) {
		return PotionLanded
	}
	return PotionMissed
}

// Claim is the sentence to log, and it is empty whenever nothing is known.
//
// An empty string here is load-bearing rather than cosmetic: a log line that
// says "threw a potion" and nothing else is true, and a bot whose own record
// of its successes is only ever the throws it sent will believe it is landing
// them all.
func (o PotionObservation) Claim() string {
	switch o.Outcome() {
	case PotionLanded:
		return "the effect is on the target"
	case PotionMissed:
		return "the target was read and the effect is not on it"
	default:
		return ""
	}
}

// PotionObserver is the seam a potion throw needs and the bot does not yet
// provide.
//
// The bot records entity metadata in RecordEntityMeta, but EntityMetaState
// retains only the tamed, collared and baby flags — the visible-effects
// compound tag is dropped on the floor. Until that is widened, every throw
// produces PotionUnobserved, which is the correct and honest answer and also
// means the potion code is inert.
//
// Wiring it needs, outside this package's scope:
//
//	internal/bot/observation.go — widen EntityMetaState with the effects read
//	    from protocol.EntityDataKeyVisibleMobEffects, and expose a reader for
//	    it alongside the existing EntityMeta.
//	internal/bot/combat/<tick file> — hold a PotionState, call PlanPotion,
//	    send the release, then re-read the target after PotionSettleTime and
//	    record the PotionObservation outcome.
type PotionObserver interface {
	// EntityEffects returns the effects last read off an entity. A target that
	// is not tracked, or whose metadata has not arrived, must be reported as
	// not observed rather than as clean — that distinction is the caller's to
	// preserve.
	EntityEffects(runtimeID uint64) (Effects, bool)
}
