package agi_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
)

var thresholds = agi.Thresholds{LowHP: 8, LowHunger: 6}

// TestEvaluateUrgencyRanksDangerAboveComfort is the ordering that keeps the
// bot alive. Read the other way round, a comfortable bot would keep eating
// while a creeper walks up, because "I am hungry" was checked before "I am at
// four hearts".
func TestEvaluateUrgencyRanksDangerAboveComfort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		snap agi.Snapshot
		want agi.Urgency
	}{
		{
			name: "critical when nearly dead even though also starving",
			snap: agi.Snapshot{HP: 3, Hunger: 1},
			want: agi.UrgencyCritical,
		},
		{
			name: "high when hurt",
			snap: agi.Snapshot{HP: 7, Hunger: 20},
			want: agi.UrgencyHigh,
		},
		{
			name: "high when starving",
			snap: agi.Snapshot{HP: 20, Hunger: 4},
			want: agi.UrgencyHigh,
		},
		{
			name: "low when someone is simply nearby",
			snap: agi.Snapshot{HP: 20, Hunger: 20, Nearby: []agi.Person{{Name: "Artheny", Distance: 3}}},
			want: agi.UrgencyLow,
		},
		{
			name: "none when healthy and alone",
			snap: agi.Snapshot{HP: 20, Hunger: 20},
			want: agi.UrgencyNone,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := agi.EvaluateUrgency(tc.snap, thresholds); got != tc.want {
				t.Errorf("EvaluateUrgency() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEvaluateUrgencyIgnoresUnknownVitals guards the reflex layer against
// acting on a reading it does not actually have. Zero means "not known yet",
// not "you are dead" — treating it as real would have the bot flee and eat
// during the first tick after joining, before the server has reported health.
func TestEvaluateUrgencyIgnoresUnknownVitals(t *testing.T) {
	t.Parallel()

	if got := agi.EvaluateUrgency(agi.Snapshot{HP: 0, Hunger: 0}, thresholds); got != agi.UrgencyNone {
		t.Errorf("EvaluateUrgency(unknown vitals) = %v, want %v", got, agi.UrgencyNone)
	}
}

// TestDecideReflexFleesBeforeItEats is the same priority rule at the level the
// bot actually acts on.
func TestDecideReflexFleesBeforeItEats(t *testing.T) {
	t.Parallel()

	got := agi.DecideReflex(agi.Snapshot{HP: 2, Hunger: 0}, thresholds)
	if got.Kind != agi.ReflexFlee {
		t.Errorf("DecideReflex(dying and starving) = %v, want ReflexFlee", got.Kind)
	}
}

// TestDecideReflexRunsAtMostOneThing keeps the bot from looking like a loop.
// A character that eats, turns and runs in the same tick is not multitasking,
// it is a bot walking down a list.
func TestDecideReflexRunsAtMostOneThing(t *testing.T) {
	t.Parallel()

	got := agi.DecideReflex(agi.Snapshot{
		HP:     1,
		Hunger: 1,
		Nearby: []agi.Person{{Name: "Artheny", Distance: 2}},
	}, thresholds)
	if got.Kind != agi.ReflexFlee {
		t.Errorf("DecideReflex() = %v, want a single ReflexFlee rather than several reflexes", got.Kind)
	}
}

// TestDecideReflexLooksAtNearestNewcomer covers the acknowledgement behaviour:
// a player walks up, the bot turns towards them.
func TestDecideReflexLooksAtNearestNewcomer(t *testing.T) {
	t.Parallel()

	got := agi.DecideReflex(agi.Snapshot{
		HP:     20,
		Hunger: 20,
		Nearby: []agi.Person{
			{Name: "Far", Distance: 9, HasLineOf: true},
			{Name: "Near", Distance: 2, HasLineOf: true},
		},
	}, thresholds)
	if got.Kind != agi.ReflexLook {
		t.Fatalf("DecideReflex() = %v, want ReflexLook", got.Kind)
	}
	if got.Person != "Near" {
		t.Errorf("DecideReflex() looked at %q, want the nearest player %q", got.Person, "Near")
	}
}

// TestDecideReflexDoesNotReissueAHeldGaze stops the head snapping back and
// forth. The bot has already greeted this player; turning again is the single
// most obviously robotic tell there is.
//
// The acknowledgement is Greeted rather than LookingAt: the gaze itself only
// lasts vision_hold_sec, and re-firing every time it lapsed starved the rest of
// the brain (see TestReflexLookDoesNotStarveTheDecisionLayer).
func TestDecideReflexDoesNotReissueAHeldGaze(t *testing.T) {
	t.Parallel()

	got := agi.DecideReflex(agi.Snapshot{
		HP:     20,
		Hunger: 20,
		Nearby: []agi.Person{{Name: "Near", Distance: 2, HasLineOf: true, Greeted: true}},
	}, thresholds)
	if got.Kind != agi.ReflexNone {
		t.Errorf("DecideReflex() = %v, want ReflexNone for a player already greeted", got.Kind)
	}

	// Still looking at them, which is the older and narrower condition.
	held := agi.DecideReflex(agi.Snapshot{
		HP:     20,
		Hunger: 20,
		Nearby: []agi.Person{{Name: "Near", Distance: 2, HasLineOf: true, LookingAt: true, Greeted: true}},
	}, thresholds)
	if held.Kind != agi.ReflexNone {
		t.Errorf("DecideReflex() = %v, want ReflexNone while the gaze is still held", held.Kind)
	}
}

// TestReflexLookDoesNotStarveTheDecisionLayer is the freeze this fixes.
//
// ReflexLook consumes the whole tick: Tick returns as soon as any reflex fires,
// so the natural-mode brain behind it never runs. When "already looked at" was
// decided by the current gaze, a standing player re-armed the reflex the moment
// the 5s hold lapsed — and on a 1-2s tick that is most ticks, so the bot looked
// at the player forever and never once decided to do anything.
//
// A player who has been greeted stays greeted for the cooldown, so one arrival
// costs one reflex.
func TestReflexLookDoesNotStarveTheDecisionLayer(t *testing.T) {
	t.Parallel()

	// The player is in view, in the same place, and the head has since turned
	// away (LookingAt false). Before the fix this re-fired ReflexLook.
	snap := agi.Snapshot{
		HP:     20,
		Hunger: 20,
		Nearby: []agi.Person{{Name: "Near", Distance: 2, HasLineOf: true, Greeted: true}},
	}

	for i := 0; i < 50; i++ {
		if got := agi.DecideReflex(snap, thresholds); got.Kind != agi.ReflexNone {
			t.Fatalf("tick %d: DecideReflex() = %v, want ReflexNone; the vision reflex is re-firing and consuming every tick",
				i, got.Kind)
		}
	}

	// A genuinely new arrival still gets looked at.
	fresh := agi.Snapshot{
		HP:     20,
		Hunger: 20,
		Nearby: []agi.Person{{Name: "Newcomer", Distance: 2, HasLineOf: true}},
	}
	if got := agi.DecideReflex(fresh, thresholds); got.Kind != agi.ReflexLook {
		t.Errorf("DecideReflex() = %v, want ReflexLook for a player who has not been greeted", got.Kind)
	}
}

// TestDecideRefreshIgnoresPlayersBehindWalls is the guarantee that makes the
// vision reflex believable. The bot still knows someone is there — it is
// tracked, and it appears in the prompt — but it must not turn its head towards
// a wall. A character that looks at people through walls is the most obvious
// tell that it is not really looking at anything.
func TestDecideReflexIgnoresPlayersBehindWalls(t *testing.T) {
	t.Parallel()

	got := agi.DecideReflex(agi.Snapshot{
		HP:     20,
		Hunger: 20,
		Nearby: []agi.Person{{Name: "Hidden", Distance: 2, HasLineOf: false}},
	}, thresholds)
	if got.Kind != agi.ReflexNone {
		t.Errorf("DecideReflex() = %v, want ReflexNone for a player out of sight", got.Kind)
	}
}

// TestCanSpeakEnforcesCooldown is the anti-chatter guarantee. An agent that
// greets someone on consecutive ticks is a spammer; this is the only thing
// standing between the model and that outcome.
func TestCanSpeakEnforcesCooldown(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cooldown := 2 * time.Minute

	if agi.CanSpeak(now.Add(-10*time.Second), now, cooldown, false) {
		t.Error("spoke again inside the cooldown window")
	}
	if !agi.CanSpeak(now.Add(-3*time.Minute), now, cooldown, false) {
		t.Error("refused to speak long after the cooldown expired")
	}
	if agi.CanSpeak(time.Time{}, now, cooldown, false) != true {
		t.Error("a bot that has never spoken should be allowed to")
	}
}

// TestCanSpeakStaysQuietWhileBusy stops the bot talking over its own task.
func TestCanSpeakStaysQuietWhileBusy(t *testing.T) {
	t.Parallel()

	now := time.Now()
	if agi.CanSpeak(time.Time{}, now, time.Minute, true) {
		t.Error("spoke while already busy with another activity")
	}
}

// TestWanderTargetKeepsMovingAwayFromTheOrigin is what stops the bot from
// jittering on the spot. Re-rolling the destination every tick looks alive in
// one frame and broken in the next; a drifting heading actually travels.
func TestWanderTargetKeepsMovingAwayFromTheOrigin(t *testing.T) {
	t.Parallel()

	origin := [3]float32{0, 64, 0}
	distinct := make(map[[2]float32]bool)
	for seed := 0; seed < 8; seed++ {
		x, _, z := agi.WanderTarget(seed, origin, 12)
		if x == origin[0] && z == origin[2] {
			t.Fatalf("seed %d produced the current position as a destination", seed)
		}
		distinct[[2]float32{x, z}] = true
	}
	if len(distinct) < 4 {
		t.Errorf("wander targets only produced %d distinct destinations over 8 ticks", len(distinct))
	}
}

// TestDescribePeopleIsNeverAmbiguous keeps an empty list distinguishable from
// a missing one, which is the difference between "nobody is here" and the model
// inventing a crowd.
func TestDescribePeopleIsNeverAmbiguous(t *testing.T) {
	t.Parallel()

	if got := agi.DescribePeople(nil); got != "none" {
		t.Errorf("DescribePeople(nil) = %q, want %q", got, "none")
	}

	got := agi.DescribePeople([]agi.Person{
		{Name: "Far", Distance: 9},
		{Name: "Near", Distance: 2, SpeakingTo: true},
	})
	if got == "" {
		t.Fatal("DescribePeople() returned empty for a non-empty list")
	}
	// Nearest first, so the model reads the closest person as the subject.
	if idx, near := indexOf(got, "Near"), indexOf(got, "Far"); idx > near {
		t.Errorf("DescribePeople() = %q, want the nearest player first", got)
	}
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
