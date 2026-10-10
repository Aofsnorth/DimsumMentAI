package gathering

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/movement/animation"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type BlockMiner struct {
	rg     *ResourceGatherer
	logger *slog.Logger
}

func NewBlockMiner(rg *ResourceGatherer, logger *slog.Logger) *BlockMiner {
	return &BlockMiner{
		rg:     rg,
		logger: logger,
	}
}

// GatherBlock mines a block type and reports the outcome as an action the
// player can see.
func (bm *BlockMiner) GatherBlock(ctx context.Context, blockName string, targetCount int) {
	collected, _ := bm.gatherBlocks(ctx, blockName, targetCount)
	bm.rg.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "mine",
		Item:    bm.resolveFuzzyName(blockName),
		Count:   collected,
		Success: true,
	})
}

// gatherBlocks does the mining and returns how much was COLLECTED (the
// inventory delta) and how many blocks were actually MINED, without reporting
// an action status. The pair matters for internal detours: a trip that mined
// six blocks but collected nothing is very different from one that found
// nothing to mine — the first means the drops went somewhere this detour
// cannot follow, and retrying is the wander the scaffold stock used to die
// in.
func (bm *BlockMiner) gatherBlocks(ctx context.Context, blockName string, targetCount int) (collected int, mined int) {
	bot := bm.rg.bot
	if targetCount <= 0 {
		targetCount = 1
	}

	resolvedName := bm.resolveFuzzyName(blockName)
	bm.logger.Debug("Starting block gathering", "name", blockName, "resolved", resolvedName, "target", targetCount)

	startCount := bm.inventoryCount(resolvedName)
	currentCount := startCount
	minedBlocks := 0
	failedAttempts := 0
	dugPositions := make(map[string]bool)

	for currentCount-startCount < targetCount && failedAttempts < 8 {
		select {
		case <-ctx.Done():
			got := currentCount - startCount
			if got < 0 {
				got = 0
			}
			return got, minedBlocks
		default:
		}

		step, minedName, foundCandidate := bm.findBestMineStep(resolvedName, dugPositions)
		if !foundCandidate {
			bm.logger.Warn("No matching block found nearby", "name", resolvedName)
			break
		}

		botPos := bot.GetCoords()
		dist := bm.distance(botPos, step.Aim)
		if dist > 4.0 {
			reached := bot.NavigateToBlock(step.Position.X(), step.Position.Y(), step.Position.Z(), 3.0)
			if !reached {
				dugPositions[mineKey(step.Position)] = true
				failedAttempts++
				continue
			}
			// Halt the legs only. This detour runs INSIDE another job (the chop
			// stocking its scaffolder); StopMovement cancels the activity context,
			// which is the very job paying for this mining trip.
			haltWalkOnly(bot)
		}

		beforeCount := bm.inventoryCount(resolvedName)
		if !bm.breakBlock(ctx, step, minedName) {
			dugPositions[mineKey(step.Position)] = true
			failedAttempts++
			continue
		}

		dugPositions[mineKey(step.Position)] = true
		minedBlocks++

		// Program-based timing: brief best-effort sweep that exits the instant
		// inventory count rises. No waiting for server pickup confirmation.
		bm.rg.looter.CollectMatchingDropsUntil(ctx, 6.0, resolvedName, beforeCount, 900*time.Millisecond)
		currentCount = bm.inventoryCount(resolvedName)
		// The failure counter only resets when the goal actually advanced. A dig
		// that breaks the block but never yields a pickup used to wipe it, so a
		// mine-sweep-fail cycle could run forever and the < 8 guard never fired.
		if currentCount > beforeCount {
			failedAttempts = 0
		} else if step.CountsTowardTarget {
			failedAttempts++
			bm.logger.Debug("inventory did not rise after sweep, moving on", "name", resolvedName, "pos", step.Position)
		}
	}

	got := currentCount - startCount
	if got < 0 {
		got = 0
	}
	bm.logger.Info("block gathering complete", "requested", resolvedName, "mined_blocks", minedBlocks, "collected", got)
	return got, minedBlocks
}

func (bm *BlockMiner) findBestMineStep(resolvedName string, dugPositions map[string]bool) (MineStep, string, bool) {
	bot := bm.rg.bot
	botPos := bot.GetCoords()
	world := bot.GetLocalWorldModel()
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	var bestStep MineStep
	bestBlockName := ""
	bestScore := float32(math.MaxFloat32)
	foundCandidate := false

	for dx := int32(-12); dx <= 12; dx++ {
		for dy := int32(-3); dy <= 5; dy++ {
			for dz := int32(-12); dz <= 12; dz++ {
				tx, ty, tz := bx+dx, by+dy, bz+dz
				target := protocol.BlockPos{tx, ty, tz}
				step, stepName, ok := bm.evaluateMineCandidate(target, botPos, bx, by, bz, resolvedName, dugPositions, world)
				if !ok {
					continue
				}

				dist := bm.distance(botPos, mgl32.Vec3{float32(tx) + 0.5, float32(ty) + 0.5, float32(tz) + 0.5})
				score := dist
				if !step.CountsTowardTarget {
					score += 18
				}
				if score < bestScore {
					bestScore = score
					bestStep = step
					bestBlockName = stepName
					foundCandidate = true
				}
			}
		}
	}

	return bestStep, bestBlockName, foundCandidate
}

// UnderminesFooting reports whether mining a candidate cell would hollow out
// the ground the bot stands on or the rim of that platform: any cell at or
// below the feet level within one block of the bot's own column. The live run
// dug the two surface blocks of the diagonally adjacent column (dy -1 and -2,
// dx +1, dz +1) — the old guard only excluded the bot's exact column — the
// drops fell into the shaft, and the chase that followed fought the server's
// position corrections until the job died with zero logs collected. Exported
// so the footing rule is testable without a live bot.
func UnderminesFooting(dx, dz int32, targetY, feetY int32) bool {
	if targetY > feetY-1 {
		return false // at or above feet level: not part of the footing
	}
	adx, adz := dx, dz
	if adx < 0 {
		adx = -adx
	}
	if adz < 0 {
		adz = -adz
	}
	return adx <= 1 && adz <= 1
}

func (bm *BlockMiner) evaluateMineCandidate(target protocol.BlockPos, botPos mgl32.Vec3, bx, by, bz int32, resolvedName string, dugPositions map[string]bool, world entity.WorldModel) (MineStep, string, bool) {
	if dugPositions[mineKey(target)] {
		return MineStep{}, "", false
	}
	if target.X() == bx && target.Z() == bz && (target.Y() == by || target.Y() == by-1) {
		return MineStep{}, "", false
	}
	if UnderminesFooting(target.X()-bx, target.Z()-bz, target.Y(), by) {
		return MineStep{}, "", false
	}
	if !world.IsSolid(target.X(), target.Y(), target.Z()) {
		return MineStep{}, "", false
	}

	name, ok := bm.rg.bot.GetBlockName(target.X(), target.Y(), target.Z())
	if !ok || !blockNameMatches(name, resolvedName) {
		return MineStep{}, "", false
	}

	step, ok := PlanMineStep(botMineWorld{bot: bm.rg.bot, model: world}, botPos, target)
	if !ok || dugPositions[mineKey(step.Position)] {
		return MineStep{}, "", false
	}

	stepName := name
	if step.Position != target {
		var stepNameOK bool
		stepName, stepNameOK = bm.rg.bot.GetBlockName(step.Position.X(), step.Position.Y(), step.Position.Z())
		if !stepNameOK || strings.EqualFold(stepName, "minecraft:bedrock") {
			return MineStep{}, "", false
		}
	}

	return step, stepName, true
}

func (bm *BlockMiner) breakBlock(ctx context.Context, step MineStep, blockName string) bool {
	bot := bm.rg.bot
	visibleStep, visible := PlanMineStep(
		botMineWorld{bot: bot, model: bot.GetLocalWorldModel()},
		bot.GetCoords(),
		step.Position,
	)
	if !visible {
		return false
	}
	return bm.mineSingle(ctx, visibleStep, blockName)
}

// mineSingle performs one break (no recursion, no obstruction check). Used
// internally by breakBlock and by the obstruction-clearing loop.
func (bm *BlockMiner) mineSingle(ctx context.Context, step MineStep, blockName string) bool {
	bot := bm.rg.bot
	bm.equipBestTool(blockName)
	// The held tool goes to the log on purpose: the live run dug dirt with a
	// hoe and nothing in the output said why.
	bm.logger.Debug("mining block", "block", blockName, "tool", bm.equippedToolName())

	bot.LookAt(step.Aim)
	if !sleepContext(ctx, 30*time.Millisecond) {
		return false
	}

	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})

	breakTime := sabdBreakDuration(serverAuthBreaking(bot), blockName, bm.equippedToolName())

	// The shared swing rhythm, not a local tick: a fixed 150 ms step restarts
	// the viewer's arm-swing cycle before it finishes, so the arm reads as a
	// machine twitching. See animation/rhythm.go.
	for i, beat := range animation.Beats(breakTime, step.Aim) {
		if !sleepContext(ctx, beat.Wait) {
			return false
		}
		if i == 0 {
			// The first beat is the wind-up; the arm is still being raised.
			continue
		}
		_ = bot.WritePacket(animation.MineSwing(bot.GetEntityRuntimeID()))
		bot.LookAt(beat.Aim)
	}

	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionCrackBreak,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionPredictDestroyBlock,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})
	// Explicitly stop breaking so the server clears destroy-progress at this
	// position. Without this the server still thinks we're mid-break there and
	// a freshly placed block gets insta-broken.
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStopBreak,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})

	changed := bm.waitForBlockChanged(ctx, step.Position, blockName, 400*time.Millisecond)
	// Program-based: optimistically clear the block in our world model so the
	// next pathfind doesn't try to step through it. If the server actually
	// kept it, the world cache update from server will resolidify on the
	// next chunk diff.
	bot.GetLocalWorldModel().SetSolid(step.Position.X(), step.Position.Y(), step.Position.Z(), false)
	if !changed {
		bm.logger.Debug("server did not confirm block break (assuming success)", "name", blockName, "pos", step.Position)
	}
	return true
}

func (bm *BlockMiner) waitForBlockChanged(ctx context.Context, pos protocol.BlockPos, oldName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		world := bm.rg.bot.GetLocalWorldModel()
		name, ok := bm.rg.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
		if !world.IsSolid(pos.X(), pos.Y(), pos.Z()) || !ok || !strings.EqualFold(name, oldName) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		if !sleepContext(ctx, 50*time.Millisecond) {
			return false
		}
	}
}

func (bm *BlockMiner) inventoryCount(resolvedName string) int {
	return inventoryCountMatching(bm.rg.bot.GetInventorySlots(), bm.rg.bot.GetItemNames(), resolvedName)
}

func mineKey(pos protocol.BlockPos) string {
	return fmt.Sprintf("%d,%d,%d", pos.X(), pos.Y(), pos.Z())
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
