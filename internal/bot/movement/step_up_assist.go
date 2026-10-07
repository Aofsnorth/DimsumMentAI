package movement

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// stepUpHeight is the tallest ledge a grounded body walks onto without
// jumping (Bedrock's player step height is 0.5625; slabs are 0.5).
const stepUpHeight = 0.6

// applyStepUpAssist lifts a grounded body onto a low ledge it just walked into.
//
// Without it, a half-block rise (a slab beside smooth stone, a full block
// beside a slab) had no mechanism at all: the wall scan ignores low ledges, so
// the body walked into the block, read as airborne and never climbed — the
// bot stood pressed against the slab. The server accepts the rise because the
// client does the same thing.
func (tc *TickContext) applyStepUpAssist(pos mgl32.Vec3) mgl32.Vec3 {
	if !tc.IsGrounded || tc.ShouldJump || tc.VelY > 0.05 || tc.IsOnLadder {
		return pos
	}
	if pos.X() == tc.CurrPos.X() && pos.Z() == tc.CurrPos.Z() {
		return pos
	}
	if tc.B == nil || tc.B.WorldModel == nil || tc.B.WorldCache == nil {
		return pos
	}

	feet := pos.Y()
	best := feet
	minX := int32(math.Floor(float64(pos.X() - 0.3)))
	maxX := int32(math.Floor(float64(pos.X() + 0.3)))
	minZ := int32(math.Floor(float64(pos.Z() - 0.3)))
	maxZ := int32(math.Floor(float64(pos.Z() + 0.3)))
	minY := int32(math.Floor(float64(feet)))
	maxY := int32(math.Floor(float64(feet + stepUpHeight)))

	for bx := minX; bx <= maxX; bx++ {
		for bz := minZ; bz <= maxZ; bz++ {
			for by := minY; by <= maxY; by++ {
				if !tc.B.WorldModel.IsSolid(bx, by, bz) {
					continue
				}
				top := tc.surfaceTopY(bx, by, bz)
				if top > best && top > feet+0.01 && top <= feet+stepUpHeight {
					best = top
				}
			}
		}
	}
	if best <= feet {
		return pos
	}

	// The lifted body needs head room: nothing solid from its new feet level
	// up to 1.8 above, in any cell it overlaps.
	firstCell := int32(math.Ceil(float64(best) - 1e-4))
	lastCell := int32(math.Floor(float64(best + 1.79)))
	for bx := minX; bx <= maxX; bx++ {
		for bz := minZ; bz <= maxZ; bz++ {
			for by := firstCell; by <= lastCell; by++ {
				if tc.B.WorldModel.IsSolid(bx, by, bz) {
					return pos
				}
			}
		}
	}

	tc.NextY = best
	tc.VelY = 0
	tc.IsGrounded = true
	return mgl32.Vec3{pos.X(), best, pos.Z()}
}
