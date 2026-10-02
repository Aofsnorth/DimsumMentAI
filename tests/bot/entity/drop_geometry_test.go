package entity_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/bot/entity"
)

// fakeGround is a test double implementing entity.GroundReader from an explicit
// set of solid cells, so landing/cliff logic can be exercised deterministically.
type fakeGround struct {
	solid map[[3]int32]bool
}

func newFakeGround(cells ...[3]int32) *fakeGround {
	g := &fakeGround{solid: make(map[[3]int32]bool, len(cells))}
	for _, c := range cells {
		g.solid[c] = true
	}
	return g
}

func (g *fakeGround) IsSolid(x, y, z int32) bool {
	return g.solid[[3]int32{x, y, z}]
}

func TestDropYawFacesTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		bot     [3]float32
		target  [3]float32
		wantYaw float32
	}{
		{name: "target +z south", bot: [3]float32{0, 64, 0}, target: [3]float32{0, 64, 5}, wantYaw: 0},
		{name: "target -x west", bot: [3]float32{0, 64, 0}, target: [3]float32{-5, 64, 0}, wantYaw: 90},
		{name: "target -z north", bot: [3]float32{0, 64, 0}, target: [3]float32{0, 64, -5}, wantYaw: 180},
		{name: "target +x east", bot: [3]float32{0, 64, 0}, target: [3]float32{5, 64, 0}, wantYaw: 270},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := entity.DropYaw(tt.bot, tt.target)
			if math.Abs(float64(got-tt.wantYaw)) > 0.001 {
				t.Fatalf("DropYaw(%v, %v) = %.3f, want %.3f", tt.bot, tt.target, got, tt.wantYaw)
			}
		})
	}
}

func TestDropPitchForDistanceInterpolates(t *testing.T) {
	t.Parallel()
	if got := entity.DropPitchForDistance(0); got != entity.DropPitchNear {
		t.Fatalf("DropPitchForDistance(0) = %.3f, want near %.3f", got, entity.DropPitchNear)
	}
	if got := entity.DropPitchForDistance(entity.DropAimMaxDistance); got != entity.DropPitchFar {
		t.Fatalf("DropPitchForDistance(max) = %.3f, want far %.3f", got, entity.DropPitchFar)
	}
	mid := entity.DropPitchForDistance(entity.DropAimMaxDistance / 2)
	wantMid := float32(entity.DropPitchNear + (entity.DropPitchFar-entity.DropPitchNear)*0.5)
	if math.Abs(float64(mid-wantMid)) > 0.001 {
		t.Fatalf("DropPitchForDistance(mid) = %.3f, want %.3f", mid, wantMid)
	}
	if got := entity.DropPitchForDistance(entity.DropAimMaxDistance * 3); got != entity.DropPitchFar {
		t.Fatalf("DropPitchForDistance(beyond) = %.3f, want far %.3f", got, entity.DropPitchFar)
	}
}

func TestComputeDropAimUsesCliffSafePitchOverGap(t *testing.T) {
	t.Parallel()
	world := newFakeGround()
	aim := entity.ComputeDropAim(world, [3]float32{0, 64, 0}, [3]float32{0, 64, 3})
	if aim.Pitch != entity.DropPitchCliffSafe {
		t.Fatalf("cliff aim pitch = %.3f, want cliff-safe %.3f", aim.Pitch, entity.DropPitchCliffSafe)
	}
}

func TestComputeDropAimUsesArcOverSolidGround(t *testing.T) {
	t.Parallel()
	world := newFakeGround([3]int32{0, 63, 1})
	aim := entity.ComputeDropAim(world, [3]float32{0, 64, 0}, [3]float32{0, 64, 3})
	if aim.Pitch == entity.DropPitchCliffSafe {
		t.Fatalf("solid-ground aim used cliff-safe pitch unexpectedly")
	}
	if math.Abs(float64(aim.Yaw-0)) > 0.001 {
		t.Fatalf("aim yaw = %.3f, want 0 (facing +z)", aim.Yaw)
	}
}

func TestComputeDropAimHandlesRecipientOnBot(t *testing.T) {
	t.Parallel()
	world := newFakeGround()
	aim := entity.ComputeDropAim(world, [3]float32{0, 64, 0}, [3]float32{0, 64, 0})
	if aim.Pitch == entity.DropPitchCliffSafe {
		t.Fatalf("recipient-on-bot used cliff-safe pitch; want normal toss")
	}
}

func TestDropStandoffTargetSitsAtConsistentDistance(t *testing.T) {
	t.Parallel()
	// Bot far to the -z side of the recipient; standoff should pull the target
	// to DropStandoffDistance from the recipient along the same line.
	bot := [3]float32{0, 64, -10}
	recipient := [3]float32{0, 64, 0}
	got := entity.DropStandoffTarget(bot, recipient)

	dx := got[0] - recipient[0]
	dz := got[2] - recipient[2]
	dist := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if math.Abs(float64(dist-entity.DropStandoffDistance)) > 0.001 {
		t.Fatalf("standoff distance = %.3f, want %.3f", dist, entity.DropStandoffDistance)
	}
	// Must remain on the bot's side of the recipient (negative z here).
	if got[2] >= recipient[2] {
		t.Fatalf("standoff z = %.3f, want on bot side (< %.3f)", got[2], recipient[2])
	}
}

func TestDropStandoffTargetReturnsRecipientWhenOnTop(t *testing.T) {
	t.Parallel()
	recipient := [3]float32{5, 70, 5}
	got := entity.DropStandoffTarget([3]float32{5, 70, 5}, recipient)
	if got != recipient {
		t.Fatalf("standoff on top = %v, want recipient %v", got, recipient)
	}
}

func TestDropStandoffTargetNaturalCloseApproach(t *testing.T) {
	t.Parallel()
	// approachRoll below the chance forces a point-blank approach; distanceRoll
	// 0 picks the minimum close standoff.
	bot := [3]float32{0, 64, -10}
	recipient := [3]float32{0, 64, 0}
	got := entity.DropStandoffTargetNatural(bot, recipient, 0, 0)

	dist := float32(math.Abs(float64(got[2] - recipient[2])))
	if math.Abs(float64(dist-entity.DropCloseStandoffMin)) > 0.001 {
		t.Fatalf("close-approach standoff = %.3f, want %.3f", dist, entity.DropCloseStandoffMin)
	}
	if dist >= entity.DropStandoffDistance {
		t.Fatalf("close approach %.3f should be nearer than normal standoff %.3f", dist, entity.DropStandoffDistance)
	}
}

func TestDropStandoffTargetNaturalNormalWithJitterStaysBounded(t *testing.T) {
	t.Parallel()
	bot := [3]float32{0, 64, -10}
	recipient := [3]float32{0, 64, 0}
	// approachRoll of 1 (>= chance) forces the normal standoff path; sweep the
	// distance roll and assert the result stays within the jittered band.
	for i := 0; i <= 10; i++ {
		roll := float32(i) / 10
		got := entity.DropStandoffTargetNatural(bot, recipient, 1, roll)
		dist := float32(math.Abs(float64(got[2] - recipient[2])))
		lo := float32(entity.DropStandoffDistance + entity.DropStandoffJitterMin)
		hi := float32(entity.DropStandoffDistance + entity.DropStandoffJitterMax)
		if dist < lo-0.001 || dist > hi+0.001 {
			t.Fatalf("normal standoff %.3f out of band [%.3f, %.3f] at roll %.2f", dist, lo, hi, roll)
		}
	}
}

func TestComputeDropAimWithJitterTucksDownAtPointBlank(t *testing.T) {
	t.Parallel()
	world := newFakeGround([3]int32{0, 63, 1})
	// Recipient within DropCloseDistance: expect a downward (positive) pitch.
	aim := entity.ComputeDropAimWithJitter(world, [3]float32{0, 64, 0}, [3]float32{0, 64, 1}, 0)
	if aim.Pitch <= 0 {
		t.Fatalf("point-blank pitch = %.3f, want downward (> 0)", aim.Pitch)
	}
}

func TestComputeDropAimWithJitterAppliesBoundedWobble(t *testing.T) {
	t.Parallel()
	world := newFakeGround([3]int32{0, 63, 3})
	base := entity.ComputeDropAimWithJitter(world, [3]float32{0, 64, 0}, [3]float32{0, 64, 3}, 0)
	high := entity.ComputeDropAimWithJitter(world, [3]float32{0, 64, 0}, [3]float32{0, 64, 3}, 1)
	low := entity.ComputeDropAimWithJitter(world, [3]float32{0, 64, 0}, [3]float32{0, 64, 3}, -1)

	if math.Abs(float64(high.Pitch-base.Pitch)-entity.DropPitchJitterRange) > 0.001 {
		t.Fatalf("max jitter = %.3f, want %.3f", high.Pitch-base.Pitch, entity.DropPitchJitterRange)
	}
	if math.Abs(float64(low.Pitch-base.Pitch)+entity.DropPitchJitterRange) > 0.001 {
		t.Fatalf("min jitter = %.3f, want %.3f", low.Pitch-base.Pitch, -entity.DropPitchJitterRange)
	}
	// Rolls beyond [-1,1] must be clamped, not extrapolated.
	over := entity.ComputeDropAimWithJitter(world, [3]float32{0, 64, 0}, [3]float32{0, 64, 3}, 5)
	if over.Pitch != high.Pitch {
		t.Fatalf("over-range roll pitch = %.3f, want clamped %.3f", over.Pitch, high.Pitch)
	}
}
