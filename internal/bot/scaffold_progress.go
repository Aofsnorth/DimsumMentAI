package bot

import (
	"log/slog"
	"time"

	"bedrock-ai/internal/debuglog"
)

// Scaffold attempt accounting.
//
// A path node that asks for a scaffold block is re-executed every tick until the
// path moves past it. Nothing in that loop ever gave up, so a node whose block
// could not go in — the server refused it, the jump never left the ground, the
// cell was out of reach — was retried for as long as the bot was left running,
// reporting the same failure to the player once per attempt.
//
// Two things close that hole. A step whose cell already holds support is not a
// failure at all (see scaffold.CellSatisfied), and a step that has failed enough
// times is abandoned so the path can be rebuilt around whatever is blocking it.
// The second is the backstop: without it, any future failure mode that does not
// advance the path becomes a hang.

// MaxScaffoldAttempts is how many times one path node may try to build its block
// before the path is dropped and replanned around it.
//
// Three is enough for the failure that happens in normal play — the first
// attempt is refused while the server is still settling, the second after the
// jump, the third after the world model caught up. Anything beyond that is not
// going to improve on its own, and the cost of waiting is a bot that cannot
// move.
const MaxScaffoldAttempts = 3

// NoteScaffoldAttempt records an attempt at a path node and reports whether that
// attempt may run. The counter is per node rather than per path, so a long path
// does not gradually exhaust itself as it walks.
func (b *Bot) NoteScaffoldAttempt(x, y, z int32) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	if x != b.ScaffoldStepX || y != b.ScaffoldStepY || z != b.ScaffoldStepZ {
		b.ScaffoldStepX, b.ScaffoldStepY, b.ScaffoldStepZ = x, y, z
		b.ScaffoldAttempts = 0
	}
	if b.ScaffoldAttempts >= MaxScaffoldAttempts {
		return false
	}
	b.ScaffoldAttempts++
	return true
}

// AdvancePastScaffoldStep moves the path along by one node when the step at that
// position has nothing left to do.
//
// The node is matched on position before the index moves, so a path that was
// dropped or rebuilt while the scaffold ran is left alone rather than having an
// unrelated node skipped.
func (b *Bot) AdvancePastScaffoldStep(x, y, z int32) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	if b.PathIndex >= len(b.CurrentPath) {
		return false
	}
	node := b.CurrentPath[b.PathIndex]
	if node.X != x || node.Y != y || node.Z != z {
		return false
	}
	b.PathIndex++
	if b.PathIndex >= len(b.CurrentPath) {
		b.CurrentPath = nil
		b.PathIndex = 0
	}
	return true
}

// AbandonScaffoldStep drops the current path so the next navigation rebuilds it
// around the node that could not be built, rather than walking the bot into the
// same obstruction again.
//
// Dropping the path is the same recovery the rest of this package already uses
// for a blocked route (breakPathObstacleLocked, handleStuckRecalcLocked) and it
// cannot loop: with no path there is no scaffold node left to retry, and
// RepalculatePath is throttled by LastPathRecalcTime, so replanning is bounded
// too.
func (b *Bot) AbandonScaffoldStep(action string, x, y, z int32) {
	b.Mu.Lock()
	node := b.PathIndex
	hadPath := len(b.CurrentPath) > 0
	b.CurrentPath = nil
	b.PathIndex = 0
	b.TicksStuck = 0
	b.StuckWindowStart = time.Time{}
	b.Mu.Unlock()

	if !hadPath {
		return
	}
	b.Logger.Warn("scaffold: giving up on this step, dropping the path to replan",
		slog.String("action", action),
		slog.Int("x", int(x)),
		slog.Int("y", int(y)),
		slog.Int("z", int(z)),
		slog.Int("path_index", node),
		slog.Int("attempts", MaxScaffoldAttempts))
}

// MaxHeadroomDetours is how many times a path node may send the planner looking
// for a way around a block above it before the bot breaks through instead.
//
// One is enough for a world that has another way and the detour is found on the
// rebuild. Two is for a world that does not: the path is dropped, replanned
// around the obstruction, and comes back to the same ceiling, and at that point
// the only difference between breaking through and standing still is that one of
// them ends.
const MaxHeadroomDetours = 2

// NoteHeadroomDetour records that a node has been abandoned for a block
// directly above it, and reports whether this attempt is the last one before
// the bot stops looking for a way round.
//
// The counter is per node, like the scaffold attempt counter beside it, so a
// long climb does not exhaust itself as it walks. A different node resets it,
// which is the case that matters most: a bot that detoured once at a boulder
// and has since moved on must not tunnel through the next one on sight.
func (b *Bot) NoteHeadroomDetour(x, y, z int32) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	if x != b.HeadroomNodeX || y != b.HeadroomNodeY || z != b.HeadroomNodeZ {
		b.HeadroomNodeX, b.HeadroomNodeY, b.HeadroomNodeZ = x, y, z
		b.HeadroomDetours = 0
	}
	b.HeadroomDetours++
	lastResort := b.HeadroomDetours >= MaxHeadroomDetours

	// #region agent log
	debuglog.Log("F", "scaffold_progress.go:NoteHeadroomDetour", "headroom detour counted", map[string]any{
		"x":          int(x),
		"y":          int(y),
		"z":          int(z),
		"detours":    b.HeadroomDetours,
		"lastResort": lastResort,
		"runId":      "headroom-v1",
	})
	// #endregion
	return lastResort
}

// RefundHeadroomDetour gives back a count taken by NoteHeadroomDetour, and is
// how a step that found nothing in the way keeps its budget.
//
// The counter answers "how many times has this node sent the planner looking
// for a way around something?", and a step whose overhead was empty never sent
// it anywhere. Counting those would mean a bot climbed four blocks of open sky
// and arrived at the first real obstruction already out of detours — so the
// first obsidian it ever met would be tunnelled rather than walked around,
// which is the one outcome the budget exists to prevent.
func (b *Bot) RefundHeadroomDetour(x, y, z int32) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	if x != b.HeadroomNodeX || y != b.HeadroomNodeY || z != b.HeadroomNodeZ {
		return
	}
	if b.HeadroomDetours > 0 {
		b.HeadroomDetours--
	}
}
