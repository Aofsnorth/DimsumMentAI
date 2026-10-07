package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"

	"github.com/go-gl/mathgl/mgl32"
)

// A slab in front of a grounded body is walked onto, not jumped into: a jump
// clips the slab and the host resets the body every ~150ms.
func TestSlabAheadIsALowStep(t *testing.T) {
	t.Parallel()

	b, slab, full := newSlabBot(t)
	for x := int32(0); x <= 10; x++ {
		b.WorldCache.SetBlockRID(x, 69, 5, full)
	}
	b.WorldCache.SetBlockRID(6, 70, 5, slab)

	tc := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{5.5, 70, 5.5}, Dx: 1, Dz: 0, Dist: 1}
	if !tc.AheadIsLowStepForTest() {
		t.Fatal("slab ahead must read as a low step")
	}
}

func TestFullBlockAheadIsNotALowStep(t *testing.T) {
	t.Parallel()

	b, _, full := newSlabBot(t)
	for x := int32(0); x <= 10; x++ {
		b.WorldCache.SetBlockRID(x, 69, 5, full)
	}
	b.WorldCache.SetBlockRID(6, 70, 5, full)

	tc := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{5.5, 70, 5.5}, Dx: 1, Dz: 0, Dist: 1}
	if tc.AheadIsLowStepForTest() {
		t.Fatal("full block ahead needs a jump")
	}
}

func TestFlatGroundAheadIsNotALowStep(t *testing.T) {
	t.Parallel()

	b, _, full := newSlabBot(t)
	for x := int32(0); x <= 10; x++ {
		b.WorldCache.SetBlockRID(x, 69, 5, full)
	}
	tc := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{5.5, 70, 5.5}, Dx: 1, Dz: 0, Dist: 1}
	if tc.AheadIsLowStepForTest() {
		t.Fatal("flat ground is not a step")
	}
}
