package gathering

import (
	"context"
	"fmt"
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

// MaxGatherAttempts caps how many trunks one gather request may work on. It
// bounds the work per request: a request for a large pile walks a few trees and
// then reports what it actually has, instead of touring the entire forest in a
// single command.
const MaxGatherAttempts = 12

func (tc *TreeChopper) GatherWood(ctx context.Context, targetCount int, preferred string) {
	if targetCount <= 0 {
		targetCount = 1
	}

	// What the bot already carries counts toward the target.
	//
	// The target is a number the player asked for, not a number to chop from
	// zero, and the log showed the difference plainly: the bot was holding 39
	// oak logs, was asked for 10, went out and tried to fell a tree, broke it,
	// failed to sweep the drop, and then reported "tidak ada pohon yang bisa
	// ditebang" — a failure message about a tree shortage for a request the bot
	// could have answered from its own inventory without leaving the spot.
	//
	// It is counted before anything is chopped, so the tally that decides
	// success is the same number the player is asking about.
	already := tc.rg.looter.currentItemCount("log")
	if already >= targetCount {
		tc.logger.Info("Wood gathering skipped, already have enough",
			"have", already, "target", targetCount)
		tc.reportGatherResult(targetCount, targetCount, 0)
		return
	}
	targetCount -= already

	tc.logger.Debug("Starting wood gathering", "target", targetCount, "already_have", already)
	// No early ReportActionStatus here — the final tally at the end of
	// fellTreesUntilTarget is the single source of truth. Reporting Success: true
	// up front made the LLM announce "dapet oak log" before the bot had even
	// finished chopping.

	collected, felled := tc.fellTreesUntilTarget(ctx, targetCount, preferred)
	tc.reportGatherResult(collected+already, targetCount+already, felled)
}

// fellTreesUntilTarget keeps felling trunks until it has the requested number
// of logs, runs out of reachable trees, or hits MaxGatherAttempts.
//
// A single trunk only holds a handful of logs, so one chop can never satisfy a
// request like "take 200 logs". The old code chopped one tree, reported whatever
// it got, and stopped — the count was parsed and then quietly dropped on the
// floor. Candidates are re-scanned after every trunk because both the world
// (that tree is gone) and the bot (it walked) have changed.
func (tc *TreeChopper) fellTreesUntilTarget(ctx context.Context, targetCount int, preferred string) (collected, felled int) {
	bot := tc.rg.bot
	felledBases := make(map[protocol.BlockPos]bool)
	var pending []treeCandidate

	for attempt := 0; attempt < MaxGatherAttempts && collected < targetCount; attempt++ {
		select {
		case <-ctx.Done():
			return collected, felled
		default:
		}

		// The candidate list is scanned once and then consumed, not rebuilt per
		// tree. A scan walks (2r+1)² × 13 cells — over a hundred thousand at the
		// widest radius — and re-running it for every trunk turned a five-tree
		// gather into millions of cell queries. The list stays valid because a
		// chopped trunk is skipped rather than removed.
		if len(pending) == 0 {
			var maxRadius int32
			pending, maxRadius = tc.findTreeCandidates(bot.GetCoords(), preferred, felledBases)
			if len(pending) == 0 {
				tc.logger.Info("No further log blocks found nearby",
					"collected", collected, "target", targetCount, "maxRadius", maxRadius)
				return collected, felled
			}
		}

		best := pending[0]
		pending = pending[1:]
		felledBases[best.base] = true
		tc.logSelectedCandidate(best, attempt+1)

		reached, got := tc.ChopTreeAt(ctx, best.base, targetCount-collected)
		if !reached {
			// Unreachable base: do not count it as felled, but do keep going so a
			// tree on a ledge cannot end the whole request.
			tc.logger.Warn("Tree base unreachable, trying next candidate", "pos", best.base)
			continue
		}

		felled++
		collected += got
		tc.logger.Info("Tree felled", "tree", felled, "collected", collected, "target", targetCount)
	}

	return collected, felled
}

// GatherOutcome decides what the bot tells the LLM about a finished gather.
//
// It is pure so the policy can be tested without a live bot. The rule that
// matters: a partial haul is a failure. Announcing success with a short count
// let the model treat "12 of 200 logs" as the finished order, and the player
// never learned the bot had run out of trees.
func GatherOutcome(collected, target, felled int) (success bool, errMsg string) {
	switch {
	case collected >= target:
		return true, ""
	case collected > 0:
		return false, fmt.Sprintf("hanya dapat %d dari %d log (%d pohon ditebang)", collected, target, felled)
	default:
		return false, "tidak ada pohon yang bisa ditebang"
	}
}

func (tc *TreeChopper) reportGatherResult(collected, target, felled int) {
	success, errMsg := GatherOutcome(collected, target, felled)
	if success {
		tc.logger.Info("Wood gathering finished", "collected", collected, "target", target, "trees", felled)
	} else {
		tc.logger.Warn("Wood gathering did not reach target",
			"collected", collected, "target", target, "trees", felled, "reason", errMsg)
	}

	tc.rg.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "chop",
		Item:    "log",
		Count:   collected,
		Success: success,
		Error:   errMsg,
	})
}

func (tc *TreeChopper) findTreeCandidates(botPos mgl32.Vec3, preferred string, skip map[protocol.BlockPos]bool) ([]treeCandidate, int32) {
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	searchRadii := []int32{16, 32, 48}
	var candidates []treeCandidate

	for _, radius := range searchRadii {
		candidates = tc.scanRadiusForCandidates(bx, by, bz, radius, preferred, skip)
		if len(candidates) > 0 {
			if radius > 16 {
				tc.logger.Info("Found trees only after widening search", "radius", radius, "candidates", len(candidates))
			}
			break
		}
		tc.logger.Debug("No logs in radius, widening search", "radius", radius)
	}

	sortTreeCandidates(candidates)
	return candidates, searchRadii[len(searchRadii)-1]
}

// sortTreeCandidates orders bases by score (distance plus a height penalty, with
// a heavy penalty for a wood type the caller did not ask for). The felling loop
// always takes the head of the list, so this is what decides which tree gets cut
// first.
func sortTreeCandidates(candidates []treeCandidate) {
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j-1].score > candidates[j].score; j-- {
			candidates[j-1], candidates[j] = candidates[j], candidates[j-1]
		}
	}
}

func (tc *TreeChopper) scanRadiusForCandidates(bx, by, bz, radius int32, preferred string, skip map[protocol.BlockPos]bool) []treeCandidate {
	visitedBase := make(map[protocol.BlockPos]bool)
	botPos := protocol.BlockPos{bx, by, bz}
	var candidates []treeCandidate

	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			for dy := int32(-4); dy <= 8; dy++ {
				tx, ty, tz := bx+dx, by+dy, bz+dz
				candidate, ok := tc.evaluateTreeCandidate(protocol.BlockPos{tx, ty, tz}, botPos, preferred, visitedBase, skip)
				if ok {
					candidates = append(candidates, candidate)
				}
			}
		}
	}

	return candidates
}

func (tc *TreeChopper) evaluateTreeCandidate(pos, botPos protocol.BlockPos, preferred string, visitedBase, skip map[protocol.BlockPos]bool) (treeCandidate, bool) {
	name, ok := tc.rg.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok || !isLogBlockName(name) {
		return treeCandidate{}, false
	}

	base := tc.traceToBase(pos)
	if visitedBase[base] {
		return treeCandidate{}, false
	}
	visitedBase[base] = true
	// A trunk that is already down (or was unreachable) must not be offered
	// again on the next scan of the same area.
	if skip[base] {
		return treeCandidate{}, false
	}

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

func (tc *TreeChopper) logSelectedCandidate(best treeCandidate, attempt int) {
	tc.logger.Info("Selected tree base",
		"pos", best.base,
		"hDist", best.hDist,
		"dyAbs", best.dyAbs,
		"score", best.score,
		"matched_preferred", best.matched,
		"attempt", attempt,
	)
}

// ChopTreeAt walks to startPos and fells that trunk. It reports whether the base
// was reached and how many logs the chop actually yielded.
func (tc *TreeChopper) ChopTreeAt(ctx context.Context, startPos protocol.BlockPos, targetCount int) (reached bool, collected int) {
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
		return false, 0
	}
	tc.rg.bot.StopMovement()

	// startPos already IS the base (GatherWood traced it before selecting),
	// so we don't trace again here. Callers from elsewhere that pass a
	// canopy log can rely on chopTree's BFS to walk the trunk upward.
	return true, tc.chopTree(ctx, startPos, targetCount)
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
