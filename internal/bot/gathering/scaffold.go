package gathering

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/scaffold"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type Scaffolder struct {
	rg     *ResourceGatherer
	logger *slog.Logger
}

func NewScaffolder(rg *ResourceGatherer, logger *slog.Logger) *Scaffolder {
	return &Scaffolder{
		rg:     rg,
		logger: logger,
	}
}

func (s *Scaffolder) FindScaffoldItem() (uint32, protocol.ItemStack, bool) {
	inv := s.rg.bot.GetInventorySlots()
	names := s.rg.bot.GetItemNames()

	priority := []string{"dirt", "cobblestone", "stone", "netherrack", "sand", "gravel", "clay", "mud"}
	for _, p := range priority {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := names[item.NetworkID]
			if strings.Contains(strings.ToLower(name), p) {
				return slot, item, true
			}
		}
	}
	return 0, protocol.ItemStack{}, false
}

// Scaffold tuning constants, exported for tests.
const (
	// ScaffoldSettle is how long the bot waits after aiming before placing.
	// The look ease converges at ~0.22/tick and needs the better part of a
	// second; 50ms was far too short and caused placements at the wrong angle.
	ScaffoldSettle = 300 * time.Millisecond

	// ScaffoldJumpTicks is how many ticks the jump emote is held. At 20Hz,
	// 8 ticks = 400ms, enough for the bot to leave the ground before the
	// block is placed underneath.
	ScaffoldJumpTicks = 8
)

// ScaffoldPlaceAim returns the aim point for placing a block under the bot's
// feet: the centre of the top face of the block directly below. This is half
// a block under the feet, horizontally centred in the same column.
func ScaffoldPlaceAim(feet mgl32.Vec3) mgl32.Vec3 {
	bx := math.Floor(float64(feet.X()))
	bz := math.Floor(float64(feet.Z()))
	return mgl32.Vec3{
		float32(bx) + 0.5,
		feet.Y() - 0.5,
		float32(bz) + 0.5,
	}
}

// scaffoldStockTarget is how many blocks the bot tries to have on hand before
// it starts towering. Small on purpose: mining is a detour, and the tower only
// needs a few blocks to finish the trunk.
const scaffoldStockTarget = 6

// scaffoldStockBlocks are the blocks mined when the bot has nothing to tower
// with, in preference order.
var scaffoldStockBlocks = []string{"dirt", "cobblestone"}

func (s *Scaffolder) TowerUpTo(ctx context.Context, targetY float32) {
	bot := s.rg.bot
	s.logger.Debug("Towering up", "target_y", targetY)
	stocked := false
	// A placement that is refused can be refused because the aim had not
	// settled or because the cell was still occupied. Both clear up in well
	// under a second, so a couple of retries turns a "sometimes the block does
	// not appear" into a tower that climbs. Past that, something is actually
	// wrong and retrying just burns the stack.
	const maxAttempts = 3
	stalled := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		curPos := bot.GetCoords()
		if curPos.Y() >= targetY-0.5 {
			break
		}

		slot, item, ok := s.FindScaffoldItem()
		if !ok {
			// Nothing to build with. Mine a small stock before giving up: the
			// alternative was standing at the foot of a tree staring at logs that
			// are out of reach, then reporting the whole gather as failed. Only
			// ever stock once per tower, so a world without dirt cannot spin here.
			if !stocked {
				stocked = true
				s.ensureScaffoldStock(ctx)
				continue
			}
			s.logger.Warn("No scaffold items found, aborting tower up")
			break
		}

		if err := bot.EquipItem(slot); err != nil {
			break
		}

		placed, reason := s.placeOneBlock(ctx, bot, curPos, item)
		if placed {
			stalled = 0
			continue
		}
		s.logger.Warn("scaffold: could not place a block", "reason", reason, "y", curPos.Y())
		stalled++
		if stalled >= maxAttempts {
			s.logger.Warn("scaffold: giving up on the tower", "attempts", stalled, "y", curPos.Y())
			break
		}
	}
}

// placeOneBlock clears the cell under the body, leaves the ground, and puts a
// block down — reporting only what the server confirms.
//
// The order is the whole fix. Clear first, because a block cannot be placed into
// a cell that holds anything, and on a hill that something is a tuft of grass.
// Then really jump, because the emote the old code used is not a jump and a
// placement aimed at the cell the body is standing in is refused. Then place, and
// read the result back from the world instead of asserting it.
func (s *Scaffolder) placeOneBlock(ctx context.Context, bot Bot, curPos mgl32.Vec3, item protocol.ItemStack) (bool, string) {
	ref, cell := scaffold.TowerColumn(curPos)

	// Clear whatever is in the way first, while the body is still down and the
	// cell is reachable.
	// TierHand on purpose: a tower has already committed to this column and has
	// nowhere to detour to, so refusing a block it is standing under would leave
	// it in the column rather than out of it. The pathfinder passes the bot's
	// real tier, because there it does have somewhere else to go.
	if ok, reason := scaffold.ClearCell(ctx, bot, cell, true, scaffold.TierHand); !ok {
		return false, "could not clear the cell: " + reason
	}

	// PlaceVerified jumps when the body is in the cell's way and sends the
	// placement the moment the body clears it.
	return scaffold.PlaceVerified(ctx, bot, ref, item)
}

// ensureScaffoldStock mines a few blocks to tower with. It stops as soon as
// anything usable is on hand, so a bot that already carries cobblestone never
// goes mining for dirt.
func (s *Scaffolder) ensureScaffoldStock(ctx context.Context) {
	for _, blockName := range scaffoldStockBlocks {
		if _, _, ok := s.FindScaffoldItem(); ok {
			return
		}
		s.logger.Info("No scaffold blocks on hand, mining a small stock",
			"block", blockName, "target", scaffoldStockTarget)
		// Quiet on purpose: this is an internal detour inside another action, and
		// a "mine" status here would show the player a task they never asked for.
		s.rg.miner.gatherBlocks(ctx, blockName, scaffoldStockTarget)
	}
}

func (s *Scaffolder) DescendFromTower(ctx context.Context, targetY float32) {
	bot := s.rg.bot
	s.logger.Debug("Descending from tower", "target_y", targetY)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		curPos := bot.GetCoords()
		if curPos.Y() <= targetY+0.5 {
			break
		}

		refPos := protocol.BlockPos{
			int32(math.Floor(float64(curPos.X()))),
			int32(math.Floor(float64(curPos.Y()))) - 1,
			int32(math.Floor(float64(curPos.Z()))),
		}

		world := bot.GetLocalWorldModel()
		if !world.IsSolid(refPos.X(), refPos.Y(), refPos.Z()) {
			break
		}

		// Under server-auth block breaking the host honours PredictDestroy only
		// after the full vanilla break time elapsed; the fixed 400ms predates that
		// mode and leaves scaffold blocks standing.
		breakTime := 400 * time.Millisecond
		if serverAuthBreaking(bot) {
			breakTime = sabdBreakDuration(true, "dirt", "")
		}

		// The break is now confirmed against the world rather than assumed. The
		// old sequence sent PredictDestroy, told the local model the block was
		// gone, and slept — so on a host that rejected the early destroy the bot
		// stepped off a block that was still there, and the next descent started
		// from a lie. BreakAndWait watches the block actually leave.
		cleared, reason := scaffold.BreakAndWait(ctx, bot, refPos, breakTime)
		if !cleared {
			s.logger.Warn("scaffold: could not clear the block to descend onto",
				"pos", refPos, "reason", reason)
			return
		}

		// Only now is it honest to tell the world model the block is gone: the
		// server has been seen to do it.
		world.SetSolid(refPos.X(), refPos.Y(), refPos.Z(), false)

		// Step off. The break alone does not move the body: the block under the
		// feet is gone and the bot is still standing at the height of the tower,
		// with every drop it just made lying on the ground below it. That is what
		// the sweep ran into — the looter looks for drops within a few blocks,
		// and a body six blocks up a tree cannot see the ground under it.
		//
		// The body has to actually come down before the sweep looks, otherwise
		// the drops are on the ground and the bot is in the canopy. Breaking
		// the block and waiting does not move it: the loop used to break the
		// whole column out from under itself and the body arrived at the bottom
		// in one fall, if at all.
		bot.NavigateTo(mgl32.Vec3{
			float32(refPos.X()) + 0.5,
			float32(refPos.Y()) + 1,
			float32(refPos.Z()) + 0.5,
		})

		if !sleepContext(ctx, 350*time.Millisecond) {
			return
		}
	}
}
