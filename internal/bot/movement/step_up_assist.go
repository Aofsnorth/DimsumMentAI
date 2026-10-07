package movement

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// stepUpHeight is the tallest ledge a grounded body walks onto without
// jumping (Bedrock's player step height is 0.5625; slabs are 0.5).
const stepUpHeight = 0.6

// routeReaches reports whether the node the bot is walking to stands at or
// above the given feet height.
func (tc *TickContext) routeReaches(y float32) bool {
	tc.B.Mu.Lock()
	defer tc.B.Mu.Unlock()
	if tc.B.PathIndex < 0 || tc.B.PathIndex >= len(tc.B.CurrentPath) {
		return false
	}
	return float32(tc.B.CurrentPath[tc.B.PathIndex].Y) >= y-0.01
}

// applyStepUpAssist lifts a grounded body onto a low ledge it just walked into.
//
// Without it, a half-block rise (a slab beside smooth stone, a full block
// beside a slab) had no mechanism at all: the wall scan ignores low ledges, so
// the body walked into the block, read as airborne and never climbed — the
// bot stood pressed against the slab. The server accepts the rise because the
// client does the same thing.
func (tc *TickContext) applyStepUpAssist(pos mgl32.Vec3) mgl32.Vec3 {
	// A falling body counts too: gravity pulls it 0.08 below a ledge top while
	// the horizontal step has already slid it over the ledge's edge, so it
	// ends the tick buried in the block. The host refuses that position and
	// snaps the body back, every tick, forever (bot pinned at a slab/ledge
	// seam). Only a rising body (a jump) is left alone.
	if tc.ShouldJump || tc.VelY > 0.05 || tc.IsOnLadder || tc.IsParkourJump {
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
	airborne := !tc.IsGrounded
	const half = 0.299
	minX := int32(math.Floor(float64(pos.X() - half)))
	maxX := int32(math.Floor(float64(pos.X() + half)))
	minZ := int32(math.Floor(float64(pos.Z() - half)))
	maxZ := int32(math.Floor(float64(pos.Z() + half)))
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
	solidBest := best
	carpetLift := false
	if ct, ok := tc.carpetSurface(pos.X(), pos.Z(), feet); ok && ct > best && ct > feet+0.01 && ct <= feet+stepUpHeight {
		best = ct
		carpetLift = best > solidBest
	}
	if best <= feet {
		return pos
	}

	// An airborne body is only lifted when the route is going to that ledge or
	// higher. A carpet lift is exempt: the model reads the body as airborne
	// while it overlaps the carpet cell, and the host never lifts it itself.
	// Skipping the lift is what made the host reset the body every tick. A body that just stepped off an edge also sags below the ledge
	// top and overlaps its lip; lifting it then cancels the fall every tick and
	// leaves the bot floating over air, which the host answers with a teleport
	// back (bot pinned at the top of a planned drop).
	if airborne && !carpetLift && !tc.routeReaches(best) {
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

// carpetSurface returns the top of the highest carpet the body's box overlaps
// at feet level. The pathfinder treats carpet as passable, but the host's
// collision shape for it is 1/16 high: a body whose box touches it at the same
// Y is rejected and reset, so the body must stand on top of it like a client
// stepping up.
func (tc *TickContext) carpetSurface(x, z, feetY float32) (float32, bool) {
	// Geyser only: BDS and the other hosts behave as before.
	if tc.B == nil || tc.B.WorldCache == nil || !tc.B.WorldCache.GeyserCompat() {
		return 0, false
	}
	const half = 0.299
	cy := int32(math.Floor(float64(feetY + 0.001)))
	minX := int32(math.Floor(float64(x - half)))
	maxX := int32(math.Floor(float64(x + half)))
	minZ := int32(math.Floor(float64(z - half)))
	maxZ := int32(math.Floor(float64(z + half)))
	var top float32
	found := false
	for bx := minX; bx <= maxX; bx++ {
		for bz := minZ; bz <= maxZ; bz++ {
			if !tc.B.WorldCache.IsCarpetAt(bx, cy, bz) {
				continue
			}
			t := tc.surfaceTopY(bx, cy, bz)
			if !found || t > top {
				top = t
				found = true
			}
		}
	}
	return top, found
}

// aheadIsLowStep reports whether the nearest obstruction in the direction of
// travel is a ledge the grounded body simply walks onto (a slab, a carpet-high
// rise): every solid cell it would touch tops out within stepUpHeight of the
// feet and nothing at head height blocks.
//
// The pathfinder treats a slab as a full block and plans a step_jump to the
// next level, but jumping into a half-block is worse than walking: the rising
// body clips the slab, the host rejects the position and resets it every
// ~150ms. A walk-up (applyStepUpAssist) is the correct move.
func (tc *TickContext) aheadIsLowStep() bool {
	if tc.B == nil || tc.B.WorldModel == nil || tc.B.WorldCache == nil || tc.Dist < 0.01 {
		return false
	}
	dirX := tc.Dx / tc.Dist
	dirZ := tc.Dz / tc.Dist
	feet := tc.CurrPos.Y()
	const half = 0.299
	highest := float32(-1)
	for _, reach := range [...]float32{0.3, 0.6, 0.9} {
		px := tc.CurrPos.X() + dirX*reach
		pz := tc.CurrPos.Z() + dirZ*reach
		minX := int32(math.Floor(float64(px - half)))
		maxX := int32(math.Floor(float64(px + half)))
		minZ := int32(math.Floor(float64(pz - half)))
		maxZ := int32(math.Floor(float64(pz + half)))
		minY := int32(math.Floor(float64(feet)))
		maxY := int32(math.Floor(float64(feet + 1.79)))
		for bx := minX; bx <= maxX; bx++ {
			for bz := minZ; bz <= maxZ; bz++ {
				for by := minY; by <= maxY; by++ {
					if !tc.B.WorldModel.IsSolid(bx, by, bz) {
						continue
					}
					top := tc.surfaceTopY(bx, by, bz)
					if top <= feet+0.01 {
						continue
					}
					if top > feet+stepUpHeight {
						return false
					}
					if top > highest {
						highest = top
					}
				}
			}
		}
	}
	return highest > feet+0.01
}
