package movement

import (
	"context"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/bot/scaffold"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// stepOutcome is what a scaffold step actually did, so the caller can tell a
// step that needs retrying from one the path should simply walk past.
type stepOutcome int

const (
	// stepFailed means nothing changed in the world. The node may be retried.
	stepFailed stepOutcome = iota
	// stepSatisfied means the cell already held the support the step was asking
	// for — usually a block the bot placed on an earlier pass. There is nothing
	// to do and nothing to report, and the path must move on or the same node
	// will be executed again next tick.
	stepSatisfied
	// stepPlaced means the server was seen to put a block in the cell.
	stepPlaced
)

// ExecuteScaffoldAction handles breaking blocking blocks or placing blocks to advance the path.
func ExecuteScaffoldAction(b *bot.Bot, node pathfinder.Node) {
	b.Logger.Info("Executing scaffold/mine action", "action", node.Action, "node", node)
	defer func() {
		b.Mu.Lock()
		b.ScaffoldingActive = false
		b.Mu.Unlock()
		b.Logger.Info("Scaffold action complete, resuming movement")
	}()

	// Backstop. Every path node is re-executed every tick until the path moves
	// on, so a node that cannot be built has to be given up on explicitly or it
	// runs until the bot is switched off.
	if !b.NoteScaffoldAttempt(node.X, node.Y, node.Z) {
		b.AbandonScaffoldStep(node.Action, node.X, node.Y, node.Z)
		return
	}

	if node.Action == "mine" {
		mineScaffoldObstruction(b, scaffold.NewStep(protocol.BlockPos{node.X, node.Y, node.Z}))
	} else if node.Action == "place" {
		// A satisfied step still has to be walked past, or the node comes back
		// next tick and the whole thing runs again.
		if buildScaffoldStep(b, node) == stepSatisfied {
			b.AdvancePastScaffoldStep(node.X, node.Y, node.Z)
		}
	}
}

// buildScaffoldStep puts the support a node needs under the bot's own column.
//
// Everything here is anchored to the node, never to where the bot's body
// currently is. The previous version read the body height to decide whether this
// was a "tower" and then placed one block above that, which meant a body lagging
// the path by a block put the block at the wrong level and, when the columns
// differed too, beside the bot instead of under it.
func buildScaffoldStep(b *bot.Bot, node pathfinder.Node) stepOutcome {
	step := scaffold.NewStep(protocol.BlockPos{node.X, node.Y, node.Z})

	// The cell above the step is checked before anything else, including before
	// the "already satisfied" shortcut below.
	//
	// A satisfied support does not mean the node is skipped: the path advances
	// past it, and the bot walks through that column on the way to the next one.
	// A ceiling over a node whose support is already placed is exactly the
	// pocket that traps a bot standing on top of its own staircase, and
	// returning early would skip the one check that frees it.
	//
	// A block overhead is broken when the pickaxe in the bot's bag can take it,
	// because a bot with a diamond pickaxe refusing to clear an obsidian floor
	// is a bot standing still for no reason. When it cannot — obsidian and no
	// diamond pickaxe is the case that matters — the path is dropped so the
	// planner routes around, and only on the last attempt, when going around
	// has already failed once, does it break through.
	tier := b.BestPickaxeTier()
	if action, reason := clearScaffoldHeadroom(b, step, node, tier); action != scaffold.HeadroomFree {
		if action != scaffold.HeadroomBreak {
			b.Logger.Warn("scaffold: the cell above the step is not workable, routing around",
				"headroom", step.Headroom, "decision", action.String(),
				"reason", reason, "node", node)
			b.AbandonScaffoldStep(node.Action, node.X, node.Y, node.Z)
			return stepFailed
		}
		if b.WorldModel != nil {
			b.WorldModel.SetSolid(step.Headroom.X(), step.Headroom.Y(), step.Headroom.Z(), false)
		}
	}

	// Is the support already there? After a successful placement this is the
	// normal state on the very next tick, and treating it as work to redo is what
	// made a climbable step retry until the bot was switched off.
	if name, satisfied := scaffold.CellSatisfied(b, step.Support); satisfied {
		b.Logger.Debug("scaffold: the support is already there",
			"node", node, "support", step.Support, "block", name)
		return stepSatisfied
	}

	// Something in the way. A plant is cleared, anything else is broken if the
	// tools in hand can take it in a time worth spending, and something they
	// cannot ends the step so the path is rebuilt around it instead.
	//
	// The tier is the best pickaxe the bot is carrying, not the one in its
	// hand: the executor breaks while movement is suspended, so it cannot stop
	// to equip, and a bot holding cobblestone with a diamond pickaxe in its
	// inventory has not been told it cannot mine.
	if name, occupied := scaffold.Occupied(b, step.Support); occupied && !scaffold.IsReplaceable(name) {
		if !scaffold.WorthBreakingWith(name, tier) {
			b.Logger.Warn("scaffold: not breaking through, routing around",
				"support", step.Support, "block", name, "node", node,
				"needs", scaffold.RequiredTier(name).String(), "have", tier.String())
			b.AbandonScaffoldStep(node.Action, node.X, node.Y, node.Z)
			return stepFailed
		}
		b.Logger.Info("scaffold: breaking the block in the way",
			"support", step.Support, "block", name)
		broken, reason := scaffold.BreakAndWait(context.Background(), b, step.Support, scaffold.BreakDuration(name))
		if !broken {
			b.Logger.Warn("scaffold: could not break the obstruction",
				"support", step.Support, "block", name, "reason", reason)
			return stepFailed
		}
		if b.WorldModel != nil {
			b.WorldModel.SetSolid(step.Support.X(), step.Support.Y(), step.Support.Z(), false)
		}
	} else if ok, reason := scaffold.ClearCell(context.Background(), b, step.Support, false, tier); !ok {
		b.Logger.Warn("scaffold: cannot place here", "support", step.Support, "reason", reason)
		return stepFailed
	}

	// There has to be something to click. A support with nothing under it is a
	// floating block, and the server refuses a placement with no face to aim at.
	if ok, reason := scaffold.SupportBelowReady(b, step.Click); !ok {
		b.Logger.Warn("scaffold: nothing to place on", "click", step.Click, "reason", reason)
		return stepFailed
	}

	return placeScaffoldBlock(b, step)
}

// mineScaffoldObstruction clears the two cells a node's body would occupy, which
// is what a "mine" action means.
func mineScaffoldObstruction(b *bot.Bot, step scaffold.Step) {
	// The tier of the best pickaxe carried, for the same reason the place path
	// uses it: a pickaxe in the bag is a pickaxe the bot has.
	tier := b.BestPickaxeTier()
	for _, cell := range [2]protocol.BlockPos{step.Feet, protocol.BlockPos{step.Feet.X(), step.Feet.Y() + 1, step.Feet.Z()}} {
		name, occupied := scaffold.Occupied(b, cell)
		if !occupied {
			continue
		}
		if !scaffold.WorthBreakingWith(name, tier) {
			b.Logger.Warn("scaffold: not breaking through, routing around",
				"cell", cell, "block", name,
				"needs", scaffold.RequiredTier(name).String(), "have", tier.String())
			b.AbandonScaffoldStep("mine", step.Feet.X(), step.Feet.Y(), step.Feet.Z())
			return
		}
		mineBlockIfSolid(b, cell.X(), cell.Y(), cell.Z())
	}
}

// clearScaffoldHeadroom clears the cell above a scaffold step and reports what
// the step should do about it.
//
// The detour counter is only spent when there was actually something to detour
// about. A climb runs this on every single step, and counting the free ones
// would mean a bot burned its entire budget on open sky and then tunnelled
// through the first obsidian it ever met — which is the exact opposite of what
// the budget is for.
func clearScaffoldHeadroom(b *bot.Bot, step scaffold.Step, node pathfinder.Node, tier scaffold.ToolTier) (scaffold.HeadroomAction, string) {
	// Peek first: the counter decides what to do, and it is only worth a count
	// if the cell is actually blocked by something the tools cannot take.
	lastResort := b.NoteHeadroomDetour(node.X, node.Y, node.Z)
	action, reason := scaffold.ClearHeadroom(context.Background(), b, step.Headroom, tier, lastResort)
	if action == scaffold.HeadroomFree || action == scaffold.HeadroomBreak {
		b.RefundHeadroomDetour(node.X, node.Y, node.Z)
	}
	return action, reason
}

func mineBlockIfSolid(b *bot.Bot, x, y, z int32) {
	if b.WorldCache == nil {
		return
	}
	isSolid, loaded := b.WorldCache.IsBlockSolid(x, y, z)
	if !loaded || !isSolid {
		return
	}

	b.Logger.Info("Mining blocking block", "x", x, "y", y, "z", z)
	cell := protocol.BlockPos{x, y, z}

	// The break time used to be guessed from whether the name contained "stone"
	// or "ore", which meant an oak log, a chest and a block of obsidian all got
	// the same 800ms. The scaffold package knows the real cost of a block and
	// treats a plant as free.
	name, _ := b.GetBlockName(x, y, z)
	broken, reason := scaffold.BreakAndWait(context.Background(), b, cell, scaffold.BreakDuration(name))
	if !broken {
		b.Logger.Warn("scaffold: could not clear the blocking block",
			"pos", cell, "block", name, "reason", reason)
		return
	}

	// Only after the server has been seen to remove it.
	if b.WorldModel != nil {
		b.WorldModel.SetSolid(x, y, z, false)
	}

	// No item sweep here: this runs mid-path with movement suspended, and a
	// sweep hijacks the destination to chase drops for its whole timeout. The
	// drop from this block lands in the path cell and is picked up as the bot
	// walks through it.
}

// placeScaffoldBlock puts the support block for a step and reports what
// happened. Silent by design: this runs several times per second on an ordinary
// climb, and narrating every block to a player turns ordinary movement into a
// running commentary nobody asked for.
func placeScaffoldBlock(b *bot.Bot, step scaffold.Step) stepOutcome {
	slot, item, ok := b.Gatherer.FindScaffoldItem()
	if !ok {
		// Gathering here is not an option: this runs while ScaffoldingActive has
		// movement suspended, so every NavigateToBlock inside a gather would
		// stall for its full timeout and the bot would stand frozen mid-climb.
		// Dropping the step and the path lets the router replan instead.
		b.Logger.Warn("No blocks to place! Abandoning the scaffold step")
		b.AbandonScaffoldStep("place", step.Feet.X(), step.Feet.Y(), step.Feet.Z())
		return stepFailed
	}

	if err := b.EquipItem(slot); err != nil {
		return stepFailed
	}

	b.Logger.Info("Placing scaffold support",
		"support", step.Support, "click", step.Click, "item", item)

	ctx := context.Background()

	// The jump — only when the body is in the way, and only until it clears the
	// cell — now lives inside PlaceVerified, the one place that knows which cell
	// the block goes into and can send the placement inside the jump window.

	placed, reason := scaffold.PlaceVerified(ctx, b, step.Click, item)
	if !placed {
		// The old code wrote the transaction and then told the world model the
		// block was there, so a refused placement was recorded as a success and
		// the pathfinder walked off to stand on a block that did not exist.
		b.Logger.Warn("scaffold: the server did not place the block",
			"support", step.Support, "click", step.Click, "reason", reason)
		return stepFailed
	}
	// The block is real, so the world model has to know it is there.
	//
	// This is not bookkeeping — it is what lets the bot climb. The grounded
	// test asks the world model whether the cell under the feet is solid, and a
	// placement that confirmed with the server but never told the model leaves
	// the bot standing on air. It then reads as not grounded, the jump request
	// is never consumed, and the next step up fails with "the body never cleared
	// the cell" — forever, because the block it needs to stand on is one the
	// server already put there.
	// The same write has to happen here rather than being left to the movement
	// loop's own repair at movement.go, because that repair only fires on a
	// grounded tick and a bot standing on an unrecorded block is by definition
	// not grounded. Waiting for it is waiting for a condition the bug prevents.
	if b.WorldModel != nil {
		b.WorldModel.SetSolid(step.Support.X(), step.Support.Y(), step.Support.Z(), true)
	}

	b.Logger.Debug("scaffold: support placed", "support", step.Support)
	return stepPlaced
}
