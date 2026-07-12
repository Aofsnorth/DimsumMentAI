package gathering

import (
	"context"
	"log/slog"
	"math"
	"time"

	"bedrock-ai/internal/event"

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

// treeCandidate describes a possible tree base for wood gathering.
type treeCandidate struct {
	base    protocol.BlockPos
	hDist   float64
	dyAbs   float64
	score   float64
	matched bool // true when block name matches the preferred wood type
}

func (tc *TreeChopper) GatherWood(ctx context.Context, targetCount int, preferred string) {
	bot := tc.rg.bot

	tc.logger.Debug("Starting wood gathering", "target", targetCount)
	bot.ReportActionStatus("", event.ActionStatus{
		Action:  "chop",
		Item:    "log",
		Count:   0,
		Success: true,
	})

	candidates, maxRadius := tc.findTreeCandidates(bot.GetCoords(), preferred)
	if len(candidates) == 0 {
		tc.logger.Warn("No log blocks found nearby", "maxRadius", maxRadius)
		bot.ReportActionStatus("", event.ActionStatus{
			Action:  "chop",
			Item:    "log",
			Success: false,
			Error:   "no trees found nearby",
		})
		return
	}

	tc.selectAndChopCandidates(ctx, candidates, targetCount)
}

func (tc *TreeChopper) findTreeCandidates(botPos mgl32.Vec3, preferred string) ([]treeCandidate, int32) {
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	searchRadii := []int32{16, 32, 48}
	var candidates []treeCandidate

	for _, radius := range searchRadii {
		candidates = tc.scanRadiusForCandidates(bx, by, bz, radius, preferred)
		if len(candidates) > 0 {
			if radius > 16 {
				tc.logger.Info("Found trees only after widening search", "radius", radius, "candidates", len(candidates))
			}
			break
		}
		tc.logger.Debug("No logs in radius, widening search", "radius", radius)
	}

	return candidates, searchRadii[len(searchRadii)-1]
}

func (tc *TreeChopper) scanRadiusForCandidates(bx, by, bz, radius int32, preferred string) []treeCandidate {
	var candidates []treeCandidate
	visitedBase := make(map[protocol.BlockPos]bool)
	botPos := protocol.BlockPos{bx, by, bz}

	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			for dy := int32(-4); dy <= 8; dy++ {
				tx, ty, tz := bx+dx, by+dy, bz+dz
				candidate, ok := tc.evaluateTreeCandidate(protocol.BlockPos{tx, ty, tz}, botPos, preferred, visitedBase)
				if ok {
					candidates = append(candidates, candidate)
				}
			}
		}
	}

	return candidates
}

func (tc *TreeChopper) evaluateTreeCandidate(pos, botPos protocol.BlockPos, preferred string, visitedBase map[protocol.BlockPos]bool) (treeCandidate, bool) {
	name, ok := tc.rg.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok || !isLogBlockName(name) {
		return treeCandidate{}, false
	}

	base := tc.traceToBase(pos)
	if visitedBase[base] {
		return treeCandidate{}, false
	}
	visitedBase[base] = true

	belowName, belowOK := tc.rg.bot.GetBlockName(base.X(), base.Y()-1, base.Z())
	if !belowOK || isLogBlockName(belowName) {
		return treeCandidate{}, false
	}

	dyBase := float64(base.Y() - botPos.Y())
	if dyBase > 4 {
		return treeCandidate{}, false
	}

	hDist := math.Sqrt(float64((base.X()-botPos.X())*(base.X()-botPos.X()) + (base.Z()-botPos.Z())*(base.Z()-botPos.Z())))
	dyAbs := math.Abs(dyBase)
	score := hDist + dyAbs*0.5
	matched := preferred == "" || matchesPreferredLog(name, preferred)
	if !matched {
		score += 64
	}

	return treeCandidate{base, hDist, dyAbs, score, matched}, true
}

func (tc *TreeChopper) selectAndChopCandidates(ctx context.Context, candidates []treeCandidate, targetCount int) {
	sortTreeCandidates(candidates)

	maxAttempts := 3
	if len(candidates) < maxAttempts {
		maxAttempts = len(candidates)
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		best := candidates[attempt]
		tc.logSelectedCandidate(best, candidates, attempt)
		if tc.ChopTreeAt(ctx, best.base, targetCount) {
			tc.logger.Info("Wood gathering finished", "attempt", attempt+1, "target", targetCount)
			return
		}
		tc.logger.Warn("Tree base unreachable, trying next candidate", "attempt", attempt+1, "pos", best.base)
	}
	tc.logger.Warn("All tree-base candidates exhausted", "tried", maxAttempts)
	tc.rg.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "chop",
		Item:    "log",
		Success: false,
		Error:   "kehalang sesuatu",
	})
}

func sortTreeCandidates(candidates []treeCandidate) {
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j-1].score > candidates[j].score; j-- {
			candidates[j-1], candidates[j] = candidates[j], candidates[j-1]
		}
	}
}

func (tc *TreeChopper) logSelectedCandidate(best treeCandidate, candidates []treeCandidate, attempt int) {
	tc.logger.Info("Selected tree base",
		"pos", best.base,
		"hDist", best.hDist,
		"dyAbs", best.dyAbs,
		"score", best.score,
		"matched_preferred", best.matched,
		"candidates", len(candidates),
		"attempt", attempt+1,
	)
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
