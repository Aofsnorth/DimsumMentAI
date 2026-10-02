// The thrown weapons: the crossbow and the trident, decided over plain data.
//
// choice.go already knows a bot should hold a crossbow at range and a trident
// underwater. What it could not express is what happens after the swap, because
// both of these are not swings — they are a state that has to survive across
// ticks, and both of them cost the bot its weapon while they run.
//
// The crossbow is a two-state machine: a load that is waited out, and a fire
// that is only legal from the loaded state. The trap is firing early. A bolt
// released before the load completes is not a weak shot, it is no shot at all,
// and a planner that treats "enough time has passed" as permission to fire has
// quietly assumed the very thing it was supposed to be checking.
//
// The trident is the same shape with a cost the crossbow does not have: the
// weapon leaves the hand and does not come back on its own schedule. It is
// either in the slot or it is in the air, and throwing again while it is in the
// air is throwing nothing. So the trident's state tracks the loyalty cycle —
// thrown, returning, back in hand, or lost — and the throw is gated on the bot
// actually holding the thing.
//
// The loyalty *return* is the honest limit of this file. The protocol does tell
// the bot a trident is on its way home: protocol.EntityDataFlagReturnTrident is
// a bit in the flags field of the trident's own actor, and EntityFlagSet below
// reads it. That is a real signal and it is used — but it is a signal about the
// projectile, not about the hotbar. Only finding the trident back in its slot
// completes the recovery, and finding it is the job of the caller through the
// HeldInSlot reading. Nothing here picks the item up off the ground.
//
// Everything is a pure function over a value, like PlanDragonFight, and nothing
// in this file touches a packet or a connection.

package combat

import (
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// ===================== SHARED THROW GEOMETRY =====================

// Thrown-weapon reach, in blocks.
//
// The lower bound is not a reach limit — both weapons work at contact range —
// it is where a throw stops being the right answer. A crossbow or trident
// released at a target's feet is slower than swinging, and the difference
// between "a weapon that works" and "a stick" is the whole reason the bot
// picked one.
const (
	// CrossbowMinRange is how close a target may be before the bot closes in
	// instead. Just under two blocks is where a bolt's flight time stops
	// mattering and a swing's does.
	CrossbowMinRange float32 = 1.5
	// CrossbowMaxRange is how far a bolt is worth sending. Past this the arrow
	// spends long enough in the air that closing the distance is faster, and
	// the same rule the generic engage loop uses at 32 keeps this consistent
	// with it.
	CrossbowMaxRange float32 = 32.0
	// TridentMinRange and TridentMaxRange are the trident's band. A trident
	// throws further than a crossbow and hits harder, so it keeps the bot
	// dangerous from further away, and it is the only answer underwater where
	// a bow is a club.
	TridentMinRange float32 = 2.0
	TridentMaxRange float32 = 24.0
)

// Thrown-projectile flight, used to aim.
//
// These are gameplay figures rather than protocol facts. The protocol describes
// a release and a head position; it does not describe a projectile's speed, so
// these are the same kind of estimate arrowSpeed in shot.go is — chosen so the
// correction grows the right amount over the distances the band allows.
const (
	// boltSpeed is a crossbow bolt's blocks-per-tick. A bolt is thrown rather
	// than loosed, so it is faster and much flatter than an arrow.
	boltSpeed = 6.0
	// boltGravity is the vertical pull on a bolt per tick. Lower than an
	// arrow's for the same reason.
	boltGravity = 0.02
	// tridentSpeed is a thrown trident's blocks-per-tick.
	tridentSpeed = 4.0
	// tridentGravity is the vertical pull on a thrown trident per tick.
	tridentGravity = 0.03
)

// CrossbowLoadTime is how long a crossbow must be held before the bolt is in
// it.
//
// It matches crossbowLoadTime in shot.go, which is the same wait the existing
// bow draw state machine already pays. They are declared separately because
// shot.go's is unexported and this package's planner is the one that has to
// guarantee the two agree; TestCrossbowLoadTimeIsTheOneTheShotStateMachineUses
// pins the relationship rather than letting them drift apart silently.
//
// The value is gameplay, not protocol: Bedrock sends a release and a head
// position, not a "the crossbow is loaded" message, so the load is timed by
// the bot and can only be confirmed by what the server does next.
const CrossbowLoadTime = crossbowLoadTime

// CrossbowReloadInterval is the pause between a shot and the next load.
//
// It covers the reload animation rather than pretending the crossbow can be
// reloaded instantly. It is not the same as the load time: the load is the
// hold, and this is everything around it.
const CrossbowReloadInterval = 1500 * time.Millisecond

// CrossbowMaxRange_FireInterval is the minimum gap between two shots from an
// already-loaded crossbow, which is short because no load is needed.
const CrossbowFireInterval = 250 * time.Millisecond

// CrossbowAimPoint is where to aim so the bolt arrives at the target.
//
// A bolt is thrown, not loosed, so it flies faster and flatter than an arrow —
// but it is still pulled down over the whole flight, and a bolt aimed level at a
// target twenty blocks away lands at its feet. The same gravity lift the bow
// uses, with a projectile that drops less, so the correction is smaller but
// still there.
func CrossbowAimPoint(from, target mgl32.Vec3) mgl32.Vec3 {
	return projectileAimPoint(from, target, bowAimHeight, boltSpeed, boltGravity)
}

// TridentAimPoint is where to aim so the trident arrives at the target.
//
// A trident is thrown in a flatter, faster arc than an arrow but a heavier one
// than a bolt, so it sits between the two. The correction is the same
// arithmetic with the trident's own figures.
func TridentAimPoint(from, target mgl32.Vec3) mgl32.Vec3 {
	return projectileAimPoint(from, target, bowAimHeight, tridentSpeed, tridentGravity)
}

// projectileAimPoint is the gravity lift for a projectile with its own speed
// and drop. It is liftAimPoint with the arrow's numbers pulled out into
// arguments, so a second kind of shot does not have to duplicate the algebra.
func projectileAimPoint(from, target mgl32.Vec3, centre, speed, gravity float32) mgl32.Vec3 {
	point := target.Add(mgl32.Vec3{0, centre, 0})
	ticks := float64(HorizontalDistance(from, target)) / float64(speed)
	drop := float32(0.5 * gravity * float32(ticks*ticks))
	return point.Add(mgl32.Vec3{0, drop, 0})
}

// inThrowBand reports whether a target is far enough to be worth throwing at
// and near enough for the throw to land.
func inThrowBand(distance, min, max float32) bool {
	return distance >= min && distance <= max
}

// EntityFlagSet reports whether a flag is set in an entity's metadata.
//
// The flags arrive as a bitfield under protocol.EntityDataKeyFlags, not as one
// key per flag, so reading a flag is a shift and a mask. The numeric types are
// all accepted because the same field arrives as an int64 on this module and
// as an int32 or a float on hosts that re-encode it; a reader that only accepts
// one of them silently reports "no flags" on every other host, which for the
// trident return flag means a bot that never learns its weapon is coming back.
func EntityFlagSet(meta protocol.EntityMetadata, flag uint32) bool {
	value, ok := meta[uint32(protocol.EntityDataKeyFlags)]
	if !ok {
		return false
	}
	bits, ok := metadataInt64(value)
	if !ok {
		return false
	}
	return bits&(int64(1)<<flag) != 0
}

// metadataInt64 reads a metadata value as an integer, tolerating the numeric
// types a host may deliver for the same field.
func metadataInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case uint64:
		return int64(v), true
	case int32:
		return int64(v), true
	case uint32:
		return int64(v), true
	case int16:
		return int64(v), true
	case uint16:
		return int64(v), true
	case int8:
		return int64(v), true
	case uint8:
		return int64(v), true
	case float32:
		return int64(v), true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}

// ===================== CROSSBOW =====================

// CrossbowPhase is which of the crossbow's two real states the weapon is in.
type CrossbowPhase int

const (
	// CrossbowEmpty is the resting state: no bolt in the weapon. A crossbow
	// here is a stick, and it cannot fire however long the bot waits.
	CrossbowEmpty CrossbowPhase = iota
	// CrossbowLoading is a load in flight. The hold has begun and the bolt is
	// not in the weapon yet.
	CrossbowLoading
	// CrossbowLoaded is the only state a shot may leave from. The bolt is in.
	CrossbowLoaded
)

// String names the phase for a log line.
func (p CrossbowPhase) String() string {
	switch p {
	case CrossbowLoading:
		return "loading"
	case CrossbowLoaded:
		return "loaded"
	default:
		return "empty"
	}
}

// CrossbowAction is what the crossbow state machine does next.
type CrossbowAction int

const (
	// CrossbowIdle is "do nothing this tick": still loading, cooling down
	// between shots, or holding a target that cannot be reached.
	CrossbowIdle CrossbowAction = iota
	// CrossbowStartLoad is the begin-hold: the packet that starts the load.
	CrossbowStartLoad
	// CrossbowFire is the release. Legal only from CrossbowLoaded.
	CrossbowFire
)

// String names the action for a log line.
func (a CrossbowAction) String() string {
	switch a {
	case CrossbowStartLoad:
		return "load"
	case CrossbowFire:
		return "fire"
	default:
		return "wait"
	}
}

// CrossbowState is the crossbow's state, which has to outlive the tick that
// started it: the load is a hold of over a second while the tick loop keeps
// running.
//
// It mirrors Shot in shot.go rather than replacing it. That file is the tick
// wiring for the bow and the crossbow together and is not in scope here; this
// is the decision layer that wiring can consult, and the two are pinned
// together by the tests rather than merged.
type CrossbowState struct {
	// Slot is the hotbar slot the crossbow is in, carried so the release
	// names the same item the server saw begin the load.
	Slot uint32
	// Phase is the weapon's real state.
	Phase CrossbowPhase
	// PhaseStart is when the current load began, and is zero when no load is
	// in flight.
	PhaseStart time.Time
	// LastShot gates the next load, keeping shots at a pace the animation can
	// actually reach.
	LastShot time.Time
}

// RecordLoad marks the beginning of a load.
func (s *CrossbowState) RecordLoad(slot uint32, now time.Time) {
	s.Slot = slot
	s.Phase = CrossbowLoading
	s.PhaseStart = now
}

// LoadDue reports whether a load in flight has run out its time.
//
// This is a question and not an action, and the split matters. Completing the
// load is a change of state the caller records, not a packet, so the planner
// must not treat elapsed time as permission to fire — otherwise the bolt goes
// out of a crossbow the bot still believes is loading, which is the exact bug
// the two-state machine exists to prevent.
func (s CrossbowState) LoadDue(now time.Time) bool {
	if s.Phase != CrossbowLoading || s.PhaseStart.IsZero() {
		return false
	}
	return !now.Before(s.PhaseStart.Add(CrossbowLoadTime))
}

// MarkLoaded records that the load finished and the bolt is in the weapon.
func (s *CrossbowState) MarkLoaded(now time.Time) {
	if s.Phase != CrossbowLoading {
		return
	}
	s.Phase = CrossbowLoaded
	s.PhaseStart = now
}

// RecordFire marks the shot as gone and empties the weapon.
//
// The crossbow goes back to empty, not loaded: a bolt has left the weapon, and
// the next one has to be loaded from scratch.
func (s *CrossbowState) RecordFire(now time.Time) {
	s.Phase = CrossbowEmpty
	s.PhaseStart = time.Time{}
	s.LastShot = now
}

// CrossbowSituation is one reading of the crossbow fight, taken as a value so
// the plan cannot change underneath the reasoning.
type CrossbowSituation struct {
	// HasBolts is whether the bot is carrying ammunition. It gates the *load*,
	// never the fire: a loaded crossbow already holds the bolt it is about to
	// spend, and refusing to fire it because the pack is empty would strand the
	// one shot the bot is actually holding.
	HasBolts bool
	// TargetDistance is the horizontal distance to the target, in blocks.
	TargetDistance float32
	// LineOfSight is whether the target can be seen. A bolt into a wall is a
	// wasted bolt and a wasted reload.
	LineOfSight bool
	// State is the crossbow's in-flight state.
	State CrossbowState
}

// PlanCrossbow decides the crossbow's next step.
//
// The order is the whole decision. Reach and line of sight come first because a
// shot at something unreachable is the most expensive mistake available, then
// the already-loaded case, which is the only one that can fire, and only then
// the load — which is the expensive one and is gated on actually having bolts
// to load.
//
// Note what is absent: there is no branch that fires because the load time has
// elapsed. Elapsed time moves the machine between states through MarkLoaded; it
// is never on its own a reason to shoot.
func PlanCrossbow(s CrossbowSituation, now time.Time) CrossbowAction {
	if !s.LineOfSight || !inThrowBand(s.TargetDistance, CrossbowMinRange, CrossbowMaxRange) {
		return CrossbowIdle
	}

	switch s.State.Phase {
	case CrossbowLoaded:
		if !s.State.LastShot.IsZero() && now.Sub(s.State.LastShot) < CrossbowFireInterval {
			return CrossbowIdle
		}
		return CrossbowFire

	case CrossbowLoading:
		// The hold is still running. The caller completes it through LoadDue
		// and MarkLoaded; until then there is nothing to send.
		return CrossbowIdle

	default:
		if !s.HasBolts {
			// Loading with nothing to load would start an animation that cannot
			// finish into a shot.
			return CrossbowIdle
		}
		if !s.State.LastShot.IsZero() && now.Sub(s.State.LastShot) < CrossbowReloadInterval {
			return CrossbowIdle
		}
		return CrossbowStartLoad
	}
}

// ===================== TRIDENT =====================

// TridentPhase is where the trident is in its loyalty cycle.
type TridentPhase int

const (
	// TridentHeld is the trident in the hotbar: the only state a throw may
	// leave from.
	TridentHeld TridentPhase = iota
	// TridentInFlight is a trident that has been thrown and has not started
	// coming back.
	TridentInFlight
	// TridentReturning is a trident the server has flagged as flying home. It
	// is not in the hand yet.
	TridentReturning
	// TridentLost is a trident that never came back — impaled in a block, or
	// gone across a chunk border. The bot has no weapon until it finds one.
	TridentLost
)

// String names the phase for a log line.
func (p TridentPhase) String() string {
	switch p {
	case TridentInFlight:
		return "in flight"
	case TridentReturning:
		return "returning"
	case TridentLost:
		return "lost"
	default:
		return "in hand"
	}
}

// Trident intervals, in the same spirit as the bow's.
const (
	// TridentThrowInterval is the pause between throws. A trident's windup is
	// short, so this is mostly there to stop a bot spamming the release.
	TridentThrowInterval = 700 * time.Millisecond
	// TridentReturnTimeout is how long the bot waits for a trident that has
	// not started coming back before it stops waiting and treats the weapon
	// as gone. A thrown trident that will return normally comes back well
	// inside this; one that is stuck never comes back at all, and a bot that
	// waits forever for it stands in a fight with nothing in its hand.
	TridentReturnTimeout = 10 * time.Second
)

// TridentAction is what the trident state machine does next.
type TridentAction int

const (
	// TridentIdle is "do nothing this tick": out of band, no line of sight, or
	// nothing in the hand to throw.
	TridentIdle TridentAction = iota
	// TridentThrow is the release.
	TridentThrow
	// TridentWait is the hold-and-wait: the weapon is in the air and the bot
	// should keep its distance rather than throw again or charge in.
	TridentWait
	// TridentRecover is the give-up: the trident is not in the hand and is not
	// coming back, so the bot should stop treating it as a weapon and go and
	// find it.
	TridentRecover
)

// String names the action for a log line.
func (a TridentAction) String() string {
	switch a {
	case TridentThrow:
		return "throw"
	case TridentWait:
		return "wait for it to come back"
	case TridentRecover:
		return "recover"
	default:
		return "idle"
	}
}

// TridentFlight is what the server has said about a trident in the air.
//
// Returning is read from protocol.EntityDataFlagReturnTrident, which is a bit
// in the flags field of the trident's own actor. It is a real signal and it is
// used, but it is about the projectile, not the hotbar.
type TridentFlight struct {
	// Returning is the server's return flag on the thrown trident.
	Returning bool
	// Observed is whether there was a reading at all. Without it, nothing is
	// known: a bot with no observer wired must not conclude a trident is
	// returning, and must not conclude it is lost.
	Observed bool
}

// TridentState is the trident's loyalty state, which outlives the tick that
// threw it.
type TridentState struct {
	// Slot is the hotbar slot the trident was thrown from.
	Slot uint32
	// Phase is where the trident is in its cycle.
	Phase TridentPhase
	// ThrownAt is when it left the hand, and is zero when it never has.
	ThrownAt time.Time
	// LastThrow gates the next throw.
	LastThrow time.Time
	// HeldInSlot is the caller's reading of whether the trident is back in
	// the hotbar. This is the only thing that completes a recovery, and it is
	// deliberately the caller's to supply: the bot has to look at its own
	// inventory, which this package does not own.
	HeldInSlot bool
}

// RecordThrow marks the trident as thrown and out of the hand.
func (s *TridentState) RecordThrow(slot uint32, now time.Time) {
	s.Slot = slot
	s.Phase = TridentInFlight
	s.ThrownAt = now
	s.LastThrow = now
	s.HeldInSlot = false
}

// UpdateLoyalty folds one reading into the state and reports whether the
// recovery completed — that is, whether the trident is back in the hand.
//
// It is deliberately conservative in three ways. The server's return flag
// promotes the trident to returning but does not put it in the hand, because a
// trident on its way back is still not something the bot can throw. A trident
// that has been out past TridentReturnTimeout is given up on, because a bot
// waiting forever for a weapon that is impaled in a wall is a bot standing still
// in a fight. And with no reading at all, nothing is claimed: a trident that is
// not observed to be back is not reported as back.
//
// HeldInSlot is checked first and on its own, because finding the trident in
// the hotbar *is* an observation — it is the bot looking at its own inventory,
// and it does not depend on having heard anything about the projectile. Gating
// it on the flight reading would mean a trident that landed cleanly was never
// recognised as recovered on exactly the tick it landed.
//
// The timeout is checked before the flight observation for the opposite reason:
// an impaled trident is precisely the case that produces *no* packets — it is
// stuck in a block, not in the air — so a guard that returned early on an
// absent reading would never give up on the one trident it most needed to.
func (s *TridentState) UpdateLoyalty(flight TridentFlight, now time.Time) bool {
	// Back in the hotbar is the one true recovery, whatever the flag says.
	if s.HeldInSlot {
		s.Phase = TridentHeld
		s.ThrownAt = time.Time{}
		return true
	}

	// Out of time and not in the hand: the weapon is gone, whether or not
	// anything has been heard about it.
	if s.Phase == TridentInFlight && !s.ThrownAt.IsZero() && now.Sub(s.ThrownAt) > TridentReturnTimeout {
		s.Phase = TridentLost
		return false
	}

	if !flight.Observed {
		return false
	}

	if flight.Returning {
		s.Phase = TridentReturning
	}
	return false
}

// TridentSituation is one reading of the trident fight.
type TridentSituation struct {
	// HasTrident is whether the bot is holding a trident right now. A trident
	// in the air is not in the hand, and throwing then would throw nothing.
	HasTrident bool
	// TargetDistance is the horizontal distance to the target, in blocks.
	TargetDistance float32
	// LineOfSight is whether the target can be seen.
	LineOfSight bool
	// State is the trident's loyalty state.
	State TridentState
}

// PlanTrident decides the trident's next step.
//
// The order is the loyalty cycle first and the fight second. Where the trident
// *is* decides whether there is a weapon at all, and no amount of good range
// makes a throw out of an empty hand worth sending.
//
// HasTrident — the caller's live reading of the hotbar — is the authority on
// that, and it deliberately overrides the recorded phase. The phase is
// bookkeeping that can go stale the moment the trident lands or is picked up,
// and a stale phase that outvoted the inventory would leave the bot waiting on
// a weapon it is already holding, or throwing a weapon it has already lost.
func PlanTrident(s TridentSituation, now time.Time) TridentAction {
	switch s.State.Phase {
	case TridentInFlight, TridentReturning:
		if !s.HasTrident {
			if !s.State.ThrownAt.IsZero() && now.Sub(s.State.ThrownAt) > TridentReturnTimeout {
				return TridentRecover
			}
			return TridentWait
		}
	case TridentLost:
		if !s.HasTrident {
			// Gone and not replaced. Nothing to fight with but the sword.
			return TridentRecover
		}
	}
	return s.afterRecovery(now)
}

// afterRecovery is the "the weapon is in the hand" half of the decision:
// reach, line of sight, and pacing, in that order.
func (s TridentSituation) afterRecovery(now time.Time) TridentAction {
	if !s.HasTrident {
		return TridentIdle
	}
	if !s.LineOfSight || !inThrowBand(s.TargetDistance, TridentMinRange, TridentMaxRange) {
		return TridentIdle
	}
	if !s.State.LastThrow.IsZero() && now.Sub(s.State.LastThrow) < TridentThrowInterval {
		return TridentWait
	}
	return TridentThrow
}
