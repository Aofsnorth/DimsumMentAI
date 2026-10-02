package combat_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/combat"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Potions: the only ranged weapon in the game whose whole point is something
// that happens *after* the throw.
//
// Every other weapon here can be judged the instant it leaves the hand: an
// arrow is a miss or it is not, a bolt is a hit or it is not. A potion is not.
// The throw is a packet, and the packet is written optimistically by the server
// long before the bottle lands. Nothing about "it was thrown" is evidence that
// "it landed", and a bot that reports a potion hit from having sent the packet
// reports a hit every single time — which is worse than never reporting one,
// because the log looks correct.
//
// So the potion is modelled as two separate claims, and the second one is only
// ever made from a reading the server actually sent.

// potionNow is the reference instant for potion timing.
var potionNow = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

func TestPlanPotion_Decisions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		s    combat.PotionSituation
		now  time.Time
		want combat.PotionAction
	}{
		{
			name: "a splash potion at a grouped target is thrown",
			s: combat.PotionSituation{HasSplashPotion: true, TargetDistance: 9,
				NearbyHostiles: 2, LineOfSight: true},
			now:  potionNow,
			want: combat.PotionThrowSplash,
		},
		{
			name: "a lingering potion at a target is thrown",
			s: combat.PotionSituation{HasLingeringPotion: true, TargetDistance: 9,
				NearbyHostiles: 2, LineOfSight: true},
			now:  potionNow,
			want: combat.PotionThrowLingering,
		},
		{
			name: "splash is preferred over lingering",
			s: combat.PotionSituation{HasSplashPotion: true, HasLingeringPotion: true,
				TargetDistance: 9, NearbyHostiles: 2, LineOfSight: true},
			now:  potionNow,
			want: combat.PotionThrowSplash,
		},
		{
			name: "a potion is not thrown at a single distant mob",
			// One target is a bow's job. The bottle is for a group or for a
			// target that is otherwise unkillable, and spending it on the first
			// skeleton to walk past is how a bot runs out mid-fight.
			s: combat.PotionSituation{HasSplashPotion: true, TargetDistance: 9,
				NearbyHostiles: 1, LineOfSight: true},
			now:  potionNow,
			want: combat.PotionIdle,
		},
		{
			name: "no potion in the pack does nothing",
			s:    combat.PotionSituation{TargetDistance: 9, NearbyHostiles: 3, LineOfSight: true},
			now:  potionNow,
			want: combat.PotionIdle,
		},
		{
			name: "a target behind cover is not thrown at",
			s: combat.PotionSituation{HasSplashPotion: true, TargetDistance: 9,
				NearbyHostiles: 3, LineOfSight: false},
			now:  potionNow,
			want: combat.PotionIdle,
		},
		{
			name: "the throw is paced",
			s: combat.PotionSituation{HasSplashPotion: true, TargetDistance: 9,
				NearbyHostiles: 3, LineOfSight: true,
				State: combat.PotionState{LastThrow: potionNow.Add(-10 * time.Millisecond)}},
			now:  potionNow,
			want: combat.PotionIdle,
		},
		{
			name: "the throw is allowed again once the interval has passed",
			s: combat.PotionSituation{HasSplashPotion: true, TargetDistance: 9,
				NearbyHostiles: 3, LineOfSight: true,
				State: combat.PotionState{LastThrow: potionNow.Add(-combat.PotionThrowInterval - time.Millisecond)}},
			now:  potionNow,
			want: combat.PotionThrowSplash,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := combat.PlanPotion(tc.s, tc.now); got != tc.want {
				t.Fatalf("PlanPotion = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlanPotion_ReachGating(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		distance float32
		want     combat.PotionAction
	}{
		{"just inside the minimum band", combat.PotionMinRange, combat.PotionThrowSplash},
		{"mid range throws", 9, combat.PotionThrowSplash},
		{"below the minimum band is not thrown at", 0.5, combat.PotionIdle},
		{"past the maximum band", combat.PotionMaxRange + 0.5, combat.PotionIdle},
		{"exactly at the maximum band", combat.PotionMaxRange, combat.PotionThrowSplash},
		{"too far to reach at all", 200, combat.PotionIdle},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := combat.PlanPotion(combat.PotionSituation{
				HasSplashPotion: true, TargetDistance: tc.distance,
				NearbyHostiles: 3, LineOfSight: true,
			}, potionNow)
			if got != tc.want {
				t.Fatalf("PlanPotion = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPotionUnderwater(t *testing.T) {
	t.Parallel()

	// Underwater a thrown potion is close to useless: the arc is unpredictable
	// and the bottle is likely to burst on the block behind the target. A
	// trident is the answer down there, which choice.go already knows.
	got := combat.PlanPotion(combat.PotionSituation{
		HasSplashPotion: true, TargetDistance: 9, NearbyHostiles: 3,
		LineOfSight: true, Underwater: true,
	}, potionNow)
	if got != combat.PotionIdle {
		t.Fatalf("PlanPotion underwater = %v, want %v", got, combat.PotionIdle)
	}
}

func TestPotionAimPoint_LiftsTheAimByDistance(t *testing.T) {
	t.Parallel()

	from := mgl32.Vec3{0, 64, 0}
	near := combat.PotionAimPoint(from, mgl32.Vec3{3, 64, 0})
	far := combat.PotionAimPoint(from, mgl32.Vec3{15, 64, 0})

	if near.Y() <= 65.2 {
		t.Fatalf("near aim height = %v, want at least the 65.2 body centre", near.Y())
	}
	if far.Y() <= near.Y() {
		t.Fatalf("far aim height = %v, want above the near aim height %v", far.Y(), near.Y())
	}
}

func TestPotionAction_String(t *testing.T) {
	t.Parallel()

	seen := map[combat.PotionAction]string{
		combat.PotionIdle:           "wait",
		combat.PotionThrowSplash:    "throw a splash potion",
		combat.PotionThrowLingering: "throw a lingering potion",
	}
	for action, want := range seen {
		if got := action.String(); got != want {
			t.Fatalf("PotionAction(%d).String() = %q, want %q", int(action), got, want)
		}
	}
	if got := combat.PotionAction(99).String(); got == "" {
		t.Fatal("an unknown potion action must still name itself for a log line")
	}
}

func TestPotionKind_String(t *testing.T) {
	t.Parallel()

	seen := map[combat.PotionKind]string{
		combat.PotionSplash:    "splash",
		combat.PotionLingering: "lingering",
	}
	for kind, want := range seen {
		if got := kind.String(); got != want {
			t.Fatalf("PotionKind(%d).String() = %q, want %q", int(kind), got, want)
		}
	}
	if got := combat.PotionKind(0).String(); got == "" {
		t.Fatal("an unset potion kind must still name itself for a log line")
	}
}

// ===================== OBSERVING THE EFFECT =====================

// TestEffectsFromMeta_ReadsTheVisibleMobEffectsKey pins the encoding of the one
// key that can say a potion landed. It was printed by round-tripping a real
// SetActorData through the module's own writer and reader: key 131 arrives as a
// compound tag whose values are int32 durations, keyed by effect ID as a
// decimal string.
func TestEffectsFromMeta_ReadsTheVisibleMobEffectsKey(t *testing.T) {
	t.Parallel()

	if protocol.EntityDataKeyVisibleMobEffects != 131 {
		t.Fatalf("EntityDataKeyVisibleMobEffects = %d, want 131", protocol.EntityDataKeyVisibleMobEffects)
	}

	meta := protocol.EntityMetadata{
		uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{
			"5":  int32(600),
			"20": int32(200),
		},
	}
	effects := combat.EffectsFromMeta(meta)

	if got := effects.Duration(combat.EffectBlindness); got != 600 {
		t.Fatalf("blindness duration = %d, want 600", got)
	}
	if got := effects.Duration(combat.EffectWither); got != 200 {
		t.Fatalf("wither duration = %d, want 200", got)
	}
	if effects.Has(combat.EffectSpeed) {
		t.Fatal("an effect that was never sent must not read as present")
	}
	if !effects.Has(combat.EffectBlindness) {
		t.Fatal("a sent effect must read as present")
	}
}

func TestEffectsFromMeta_EmptyAndAbsent(t *testing.T) {
	t.Parallel()

	// Metadata with no effects key is no reading at all, which is different
	// from a reading of "no effects". The distinction is what stops a target
	// that simply is not being tracked from being reported as un-enchanted.
	if effects := combat.EffectsFromMeta(nil); effects.Observed {
		t.Fatal("absent metadata is not an observation")
	}
	if effects := combat.EffectsFromMeta(protocol.EntityMetadata{}); effects.Observed {
		t.Fatal("empty metadata is not an observation")
	}

	// A key that is present but empty is a real reading: the server said this
	// entity has nothing on it.
	effects := combat.EffectsFromMeta(protocol.EntityMetadata{
		uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{},
	})
	if !effects.Observed {
		t.Fatal("an empty effects map is a real observation of no effects")
	}
	if effects.Has(combat.EffectBlindness) {
		t.Fatal("an empty effects map has no effects in it")
	}
}

func TestEffectsFromMeta_ToleratesTheNumericShapesHostsUse(t *testing.T) {
	t.Parallel()

	// The compound tag decodes to int32 on this module, but a host that
	// round-trips through a different NBT path can deliver the duration as
	// another number. A reader that only accepts int32 would call a poisoned
	// target clean.
	for name, value := range map[string]any{
		"int32":   int32(600),
		"int64":   int64(600),
		"int16":   int16(600),
		"float32": float32(600),
		"float64": float64(600),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			effects := combat.EffectsFromMeta(protocol.EntityMetadata{
				uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{"5": value},
			})
			if got := effects.Duration(combat.EffectBlindness); got != 600 {
				t.Fatalf("duration read from a %s value = %d, want 600", name, got)
			}
		})
	}
}

func TestEffectsFromMeta_IgnoresUnusableEntries(t *testing.T) {
	t.Parallel()

	// A non-numeric key or a non-numeric duration is not an effect. Reading
	// one as "some unknown effect is active" would let a malformed key make
	// every target look poisoned.
	effects := combat.EffectsFromMeta(protocol.EntityMetadata{
		uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{
			"not_a_number": int32(600),
			"5":            "not_a_duration",
		},
	})
	if !effects.Observed {
		t.Fatal("a map with unusable entries is still an observation")
	}
	if effects.Has(combat.EffectBlindness) {
		t.Fatal("an entry with an unreadable duration must not count as the effect")
	}
	if effects.Count() != 0 {
		t.Fatalf("read %d effects from a map of two unusable entries, want 0", effects.Count())
	}
}

// TestPotionLanded_OnlyClaimsAnObservedEffect is the crux of the whole file.
// A throw that was sent is not a hit. The only way to report a hit is for the
// target to have been read with the effect on it afterwards.
func TestPotionHit_OnlyClaimsAnObservedEffect(t *testing.T) {
	t.Parallel()

	before := combat.EffectsFromMeta(protocol.EntityMetadata{
		uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{},
	})

	t.Run("a reading with the effect on it is a landed potion", func(t *testing.T) {
		t.Parallel()
		after := combat.EffectsFromMeta(protocol.EntityMetadata{
			uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{"5": int32(600)},
		})
		if !combat.PotionHit(before, after, combat.EffectBlindness) {
			t.Fatal("a target read with the effect on it has been hit")
		}
	})

	t.Run("a reading without the effect is a miss", func(t *testing.T) {
		t.Parallel()
		after := combat.EffectsFromMeta(protocol.EntityMetadata{
			uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{},
		})
		if combat.PotionHit(before, after, combat.EffectBlindness) {
			t.Fatal("a target read with nothing on it has not been hit")
		}
	})

	t.Run("no reading at all is never a hit", func(t *testing.T) {
		t.Parallel()
		after := combat.EffectsFromMeta(nil)
		if combat.PotionHit(before, after, combat.EffectBlindness) {
			t.Fatal("an unread target must never be reported as hit")
		}
	})

	t.Run("an unread target before the throw is never a hit", func(t *testing.T) {
		t.Parallel()
		unread := combat.EffectsFromMeta(nil)
		after := combat.EffectsFromMeta(protocol.EntityMetadata{
			uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{"5": int32(600)},
		})
		if combat.PotionHit(unread, after, combat.EffectBlindness) {
			t.Fatal("without a before reading there is no change to observe")
		}
	})

	t.Run("an effect the target already had is not a new hit", func(t *testing.T) {
		t.Parallel()
		poisoned := combat.EffectsFromMeta(protocol.EntityMetadata{
			uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{"5": int32(600)},
		})
		after := combat.EffectsFromMeta(protocol.EntityMetadata{
			uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{"5": int32(600)},
		})
		if combat.PotionHit(poisoned, after, combat.EffectBlindness) {
			t.Fatal("an unchanged effect was not caused by this throw")
		}
	})
}

func TestPotionOutcome_String(t *testing.T) {
	t.Parallel()

	seen := map[combat.PotionOutcome]string{
		combat.PotionUnobserved: "not observed",
		combat.PotionLanded:     "landed",
		combat.PotionMissed:     "missed",
		combat.PotionExpired:    "no reading before the deadline",
	}
	for outcome, want := range seen {
		if got := outcome.String(); got != want {
			t.Fatalf("PotionOutcome(%d).String() = %q, want %q", int(outcome), got, want)
		}
	}
}

// TestPotionRead_ReportsTheOutcome walks the whole observation, which is the
// only honest way to answer "did that potion hit".
func TestPotionRead_ReportsTheOutcome(t *testing.T) {
	t.Parallel()

	clean := func() combat.Effects {
		return combat.EffectsFromMeta(protocol.EntityMetadata{
			uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{},
		})
	}
	poisoned := func(d int) combat.Effects {
		return combat.EffectsFromMeta(protocol.EntityMetadata{
			uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{"5": int32(d)},
		})
	}

	cases := []struct {
		name string
		obs  combat.PotionObservation
		want combat.PotionOutcome
	}{
		{
			name: "the effect appeared on the target",
			obs: combat.PotionObservation{
				Before: clean(), After: poisoned(600), AfterKnown: true, Effect: combat.EffectBlindness,
			},
			want: combat.PotionLanded,
		},
		{
			name: "the target was read and was clean",
			obs: combat.PotionObservation{
				Before: clean(), After: clean(), AfterKnown: true, Effect: combat.EffectBlindness,
			},
			want: combat.PotionMissed,
		},
		{
			name: "the target could not be read at all",
			// This is the case the task is really about: the throw went out and
			// nothing came back. The answer is "not observed", never "landed".
			obs: combat.PotionObservation{
				Before: clean(), After: combat.Effects{}, AfterKnown: false, Effect: combat.EffectBlindness,
			},
			want: combat.PotionUnobserved,
		},
		{
			name: "the deadline passed with no reading",
			obs: combat.PotionObservation{
				Expired: true, After: combat.Effects{}, AfterKnown: false, Effect: combat.EffectBlindness,
			},
			want: combat.PotionExpired,
		},
		{
			name: "a reading with no baseline is unobserved, not landed",
			obs: combat.PotionObservation{
				After: poisoned(600), AfterKnown: true, Effect: combat.EffectBlindness,
			},
			want: combat.PotionUnobserved,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.obs.Outcome(); got != tc.want {
				t.Fatalf("Outcome = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPotionObservation_Claim reports the sentence a log line would carry, and
// asserts the one thing it must never say.
func TestPotionObservation_Claim(t *testing.T) {
	t.Parallel()

	unread := combat.PotionObservation{AfterKnown: false, Effect: combat.EffectBlindness}
	if got := unread.Claim(); got != "" {
		t.Fatalf("an unobserved potion must claim nothing, got %q", got)
	}

	landed := combat.PotionObservation{
		Before: combat.EffectsFromMeta(protocol.EntityMetadata{
			uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{}}),
		After: combat.EffectsFromMeta(protocol.EntityMetadata{
			uint32(protocol.EntityDataKeyVisibleMobEffects): map[string]any{"5": int32(600)}}),
		AfterKnown: true, Effect: combat.EffectBlindness,
	}
	claim := landed.Claim()
	if claim == "" {
		t.Fatal("an observed hit must say so")
	}
}

// TestPotionState_ThrowThenRead is the state the tick loop keeps: what was
// thrown, and when, so a later reading can be matched to it.
func TestPotionState_ThrowThenRead(t *testing.T) {
	t.Parallel()

	s := combat.PotionState{}
	s.RecordThrow(combat.PotionSplash, 3, potionNow)

	if s.Kind != combat.PotionSplash {
		t.Fatalf("Kind = %v, want %v", s.Kind, combat.PotionSplash)
	}
	if s.Slot != 3 {
		t.Fatalf("Slot = %d, want 3", s.Slot)
	}
	if !s.InFlight() {
		t.Fatal("a recorded throw is in flight")
	}
	if !s.LastThrow.Equal(potionNow) {
		t.Fatalf("LastThrow = %v, want %v", s.LastThrow, potionNow)
	}

	// The reading window closes at the settle time and not before.
	if s.ReadyForRead(potionNow.Add(combat.PotionSettleTime - time.Millisecond)) {
		t.Fatal("a potion must not be read before it has had time to land")
	}
	if !s.ReadyForRead(potionNow.Add(combat.PotionSettleTime)) {
		t.Fatal("a potion must be readable once it has had time to land")
	}
	if !s.ReadyForRead(potionNow.Add(combat.PotionReadTimeout + time.Millisecond)) {
		t.Fatal("a potion stays readable past the settle time")
	}
	if s.ReadyForRead(potionNow.Add(-time.Second)) {
		t.Fatal("a throw that has not happened is not ready to be read")
	}

	// A state that has never thrown anything is not in flight.
	var empty combat.PotionState
	if empty.InFlight() {
		t.Fatal("an untouched potion state is not in flight")
	}
	if empty.ReadyForRead(potionNow) {
		t.Fatal("an untouched potion state is not ready to be read")
	}
}

func TestPotionState_ClearAfterRead(t *testing.T) {
	t.Parallel()

	s := combat.PotionState{}
	s.RecordThrow(combat.PotionSplash, 3, potionNow)
	s.Clear()

	if s.InFlight() {
		t.Fatal("a cleared potion state is not in flight")
	}
	if s.Kind != combat.PotionNone {
		t.Fatalf("Kind after clear = %v, want %v", s.Kind, combat.PotionNone)
	}
}

// TestPotionThrowIntervalIsLongerThanTheSettleTime checks the two intervals are
// consistent: a throw can never be read before the next one is allowed, or the
// reading is attributed to the wrong bottle.
func TestPotionThrowIntervalIsLongerThanTheSettleTime(t *testing.T) {
	t.Parallel()

	if combat.PotionThrowInterval <= combat.PotionSettleTime {
		t.Fatalf("throw interval %v must outlast the settle time %v",
			combat.PotionThrowInterval, combat.PotionSettleTime)
	}
	if combat.PotionReadTimeout <= combat.PotionSettleTime {
		t.Fatalf("read timeout %v must outlast the settle time %v",
			combat.PotionReadTimeout, combat.PotionSettleTime)
	}
}
