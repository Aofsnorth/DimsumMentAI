package gathering

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type TreeChopper struct {
	rg     *ResourceGatherer
	logger *slog.Logger
}

func NewTreeChopper(rg *ResourceGatherer, logger *slog.Logger) *TreeChopper {
	return &TreeChopper{
		rg:     rg,
		logger: logger,
	}
}

func (tc *TreeChopper) GatherWood(ctx context.Context, targetCount int, preferred string) {
	bot := tc.rg.bot
	botPos := bot.GetCoords()

	tc.logger.Debug("Starting wood gathering", "target", targetCount)
	tc.rg.bot.SendChat("Aku cari pohon dulu ya!")

	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	// Collect every log in range, trace each to its base, score by horizontal
	// distance + vertical penalty, then pick the lowest-scoring base. This
	// avoids the previous bug where the picker locked onto a log in the
	// canopy (y delta ~7) that NavigateToBlock could never reach.
	type candidate struct {
		base    protocol.BlockPos
		hDist   float64
		dyAbs   float64
		score   float64
		matched bool // true when block name matches `preferred`
	}
	var candidates []candidate
	visitedBase := make(map[protocol.BlockPos]bool)

	for dx := int32(-16); dx <= 16; dx++ {
		for dz := int32(-16); dz <= 16; dz++ {
			for dy := int32(-3); dy <= 8; dy++ {
				tx, ty, tz := bx+dx, by+dy, bz+dz
				name, ok := tc.rg.bot.GetBlockName(tx, ty, tz)
				if !ok || !isLogBlockName(name) {
					continue
				}
				base := tc.traceToBase(protocol.BlockPos{tx, ty, tz})
				if visitedBase[base] {
					continue
				}
				visitedBase[base] = true

				// Reject bases that aren't ground-anchored — likely floating
				// chunks or partially-loaded trees that the bot can't stand
				// next to safely.
				belowName, belowOK := tc.rg.bot.GetBlockName(base.X(), base.Y()-1, base.Z())
				if !belowOK || isLogBlockName(belowName) {
					// traceToBase ran out of iterations or world data isn't
					// loaded under the base; skip rather than risk a bad nav.
					continue
				}

				dyBase := float64(base.Y() - by)
				if dyBase > 4 {
					// Out of safe reach without scaffolding.
					continue
				}
				hDist := math.Sqrt(float64((base.X()-bx)*(base.X()-bx) + (base.Z()-bz)*(base.Z()-bz)))
				dyAbs := math.Abs(dyBase)
				score := hDist + dyAbs*0.5
				matched := preferred == "" || matchesPreferredLog(name, preferred)
				if !matched {
					// Soft-prefer match: penalise non-matching logs heavily so
					// they only win when no preferred logs exist nearby.
					score += 64
				}
				candidates = append(candidates, candidate{base, hDist, dyAbs, score, matched})
			}
		}
	}

	if len(candidates) == 0 {
		tc.logger.Warn("No log blocks found nearby")
		tc.rg.bot.SendChat("Aku belum nemu pohon yang kebaca di sekitar sini.")
		return
	}

	// Sort ascending by score so we can try the next-best base when the
	// primary one is unreachable (pathfinder stuck on a ledge, etc).
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j-1].score > candidates[j].score; j-- {
			candidates[j-1], candidates[j] = candidates[j], candidates[j-1]
		}
	}

	// Try up to 3 candidate bases before giving up. The previous version
	// locked onto a single base and reported "Gak bisa nyampe" even when a
	// perfectly good tree sat two blocks further away.
	maxAttempts := 3
	if len(candidates) < maxAttempts {
		maxAttempts = len(candidates)
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		best := candidates[attempt]
		tc.logger.Info("Selected tree base",
			"pos", best.base,
			"hDist", best.hDist,
			"dyAbs", best.dyAbs,
			"score", best.score,
			"matched_preferred", best.matched,
			"candidates", len(candidates),
			"attempt", attempt+1,
		)
		if tc.ChopTreeAt(ctx, best.base, targetCount) {
			tc.logger.Info("Wood gathering finished", "attempt", attempt+1, "target", targetCount)
			return
		}
		tc.logger.Warn("Tree base unreachable, trying next candidate", "attempt", attempt+1, "pos", best.base)
	}
	tc.logger.Warn("All tree-base candidates exhausted", "tried", maxAttempts)
	tc.rg.bot.SendChat("Aku gak bisa nyampe ke pohonnya, mungkin kehalang sesuatu.")
}

func (tc *TreeChopper) ChopTreeAt(ctx context.Context, startPos protocol.BlockPos, targetCount int) bool {
	tc.logger.Debug("Directed to chop tree", "pos", startPos)
	if targetCount <= 0 {
		targetCount = 1
	}

	targetVec := mgl32.Vec3{float32(startPos.X()) + 0.5, float32(startPos.Y()), float32(startPos.Z()) + 0.5}
	tc.rg.bot.LookAt(targetVec)
	time.Sleep(100 * time.Millisecond)

	// Up to 3 attempts: tree base may be temporarily unreachable while the
	// bot's pathfinder corrects an earlier mis-step (e.g. ledge it just fell
	// off). Tolerance bumped to 3.5 so standing one block away counts as
	// "reached" — the chopTree BFS will handle the rest.
	reached := false
	for attempt := 0; attempt < 3; attempt++ {
		if tc.rg.bot.NavigateToBlock(startPos.X(), startPos.Y(), startPos.Z(), 3.5) {
			reached = true
			break
		}
		tc.logger.Debug("Navigate attempt failed, retrying", "attempt", attempt+1, "pos", startPos)
		time.Sleep(300 * time.Millisecond)
	}
	if !reached {
		tc.logger.Warn("Could not reach tree base", "pos", startPos)
		return false
	}
	tc.rg.bot.StopMovement()

	// startPos already IS the base (GatherWood traced it before selecting),
	// so we don't trace again here. Callers from elsewhere that pass a
	// canopy log can rely on chopTree's BFS to walk the trunk upward.
	tc.chopTree(ctx, startPos, targetCount)
	return true
}

// equippedAxeName returns the bot's currently held item name (empty when no
// item is held). Used by chopTree to compute the correct per-log break time
// based on whether an axe was equipped via equipBestAxe.
func (tc *TreeChopper) equippedAxeName() string {
	bot := tc.rg.bot
	slot := bot.GetHeldItemSlot()
	inv := bot.GetInventorySlots()
	item, ok := inv[slot]
	if !ok || item.Count == 0 {
		return ""
	}
	names := bot.GetItemNames()
	return names[item.NetworkID]
}

func (tc *TreeChopper) traceToBase(pos protocol.BlockPos) protocol.BlockPos {
	current := pos

	for i := 0; i < 15; i++ {
		below := protocol.BlockPos{current.X(), current.Y() - 1, current.Z()}
		name, ok := tc.rg.bot.GetBlockName(below.X(), below.Y(), below.Z())
		if ok && isLogBlockName(name) {
			current = below
		} else {
			break
		}
	}
	return current
}
